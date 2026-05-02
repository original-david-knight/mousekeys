package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"sync"
	"time"

	wlclient "github.com/rajveermalviya/go-wayland/wayland/client"
	"golang.org/x/sys/unix"

	"mousekeys/internal/waylandprotocols/wlrlayershell"
)

const (
	layerShellNamespace = "mousekeys"
)

func RenderPlaceholderOverlay(buffer ARGBBuffer) {
	if buffer.Width <= 0 || buffer.Height <= 0 || buffer.Stride < buffer.Width {
		return
	}
	for y := 0; y < buffer.Height; y++ {
		row := buffer.Pixels[y*buffer.Stride : y*buffer.Stride+buffer.Width]
		for x := range row {
			row[x] = 0x66080c10
		}
	}
}

type LayerShellOverlayBackend struct {
	client *WaylandClient
}

func NewLayerShellOverlayBackend(client *WaylandClient) *LayerShellOverlayBackend {
	return &LayerShellOverlayBackend{client: client}
}

func (b *LayerShellOverlayBackend) Outputs(ctx context.Context) ([]Monitor, error) {
	if b == nil || b.client == nil {
		return nil, fmt.Errorf("Wayland layer-shell backend is not configured")
	}
	return b.client.Outputs(ctx)
}

func (b *LayerShellOverlayBackend) CreateSurface(ctx context.Context, monitor Monitor) (OverlaySurface, error) {
	if b == nil || b.client == nil {
		return nil, fmt.Errorf("Wayland layer-shell backend is not configured")
	}
	if err := validateSurfaceConfig(SurfaceConfig{
		OutputName: monitor.Name,
		Width:      monitor.Width,
		Height:     monitor.Height,
		Scale:      monitor.Scale,
	}); err != nil {
		return nil, err
	}

	b.client.StartDispatchLoop(ctx)

	compositor := b.client.Compositor()
	if compositor == nil {
		return nil, fmt.Errorf("Wayland compositor is not bound")
	}
	layerShell := b.client.LayerShell()
	if layerShell == nil {
		return nil, fmt.Errorf("wlr layer-shell global is not bound")
	}
	output, ok := b.client.OutputHandle(monitor.Name)
	if !ok {
		return nil, fmt.Errorf("Wayland output %q has no bound wl_output handle", monitor.Name)
	}

	surfaceCtx, cancel := context.WithCancel(ctx)
	s := &layerShellSurface{
		client:     b.client,
		outputName: monitor.Name,
		config: SurfaceConfig{
			OutputName: monitor.Name,
			Width:      monitor.Width,
			Height:     monitor.Height,
			Scale:      monitor.Scale,
		},
		bufferScale: waylandBufferScale(monitor.Scale),
		ctx:         surfaceCtx,
		cancel:      cancel,
		closed:      make(chan struct{}),
		events:      make(chan layerShellSurfaceEvent, 32),
		retired:     map[*waylandSHMBuffer]struct{}{},
	}

	if err := b.client.withProtocolLock(func() error {
		var err error
		s.wlSurface, err = compositor.CreateSurface()
		if err != nil {
			return fmt.Errorf("create wl_surface: %w", err)
		}
		s.id = fmt.Sprintf("layer-shell-surface-%d", s.wlSurface.ID())
		s.layerSurface, err = layerShell.GetLayerSurface(
			s.wlSurface,
			output,
			uint32(wlrlayershell.LayerShellLayerOverlay),
			layerShellNamespace,
		)
		if err != nil {
			return fmt.Errorf("create wlr layer surface: %w", err)
		}
		s.layerSurface.SetConfigureHandler(func(event wlrlayershell.LayerSurfaceConfigureEvent) {
			s.enqueue(layerShellSurfaceEvent{
				kind:   layerShellSurfaceEventConfigure,
				serial: event.Serial,
				width:  int(event.Width),
				height: int(event.Height),
			})
		})
		s.layerSurface.SetClosedHandler(func(wlrlayershell.LayerSurfaceClosedEvent) {
			s.enqueue(layerShellSurfaceEvent{kind: layerShellSurfaceEventClosed})
		})
		return nil
	}); err != nil {
		cancel()
		if s.layerSurface != nil || s.wlSurface != nil {
			_ = s.Destroy(ctx)
		}
		return nil, err
	}

	b.client.registerOutputChangeListener(monitor.Name, s)
	return s, nil
}

type layerShellSurfaceEventKind int

const (
	layerShellSurfaceEventConfigure layerShellSurfaceEventKind = iota + 1
	layerShellSurfaceEventOutputChanged
	layerShellSurfaceEventBufferReleased
	layerShellSurfaceEventClosed
)

type layerShellSurfaceEvent struct {
	kind    layerShellSurfaceEventKind
	serial  uint32
	width   int
	height  int
	monitor Monitor
	buffer  *waylandSHMBuffer
}

type layerShellSurface struct {
	client       *WaylandClient
	wlSurface    *wlclient.Surface
	layerSurface *wlrlayershell.LayerSurface
	outputName   string
	id           string

	ctx    context.Context
	cancel context.CancelFunc
	closed chan struct{}

	events    chan layerShellSurfaceEvent
	eventOnce sync.Once

	mu                sync.Mutex
	config            SurfaceConfig
	bufferScale       int
	rerenderer        SurfaceRerenderFunc
	current           *waylandSHMBuffer
	retired           map[*waylandSHMBuffer]struct{}
	keyboardExclusive bool
	destroyed         bool

	destroyOnce sync.Once
	closeOnce   sync.Once
	destroyErr  error
}

func (s *layerShellSurface) ID() string {
	if s == nil {
		return ""
	}
	return s.id
}

func (s *layerShellSurface) SetRerenderer(rerenderer SurfaceRerenderFunc) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rerenderer = rerenderer
}

func (s *layerShellSurface) Configure(ctx context.Context, config SurfaceConfig) error {
	if s == nil {
		return fmt.Errorf("layer-shell surface is nil")
	}
	if err := validateSurfaceConfig(config); err != nil {
		return err
	}

	s.mu.Lock()
	if s.destroyed {
		s.mu.Unlock()
		return fmt.Errorf("layer-shell surface is destroyed")
	}
	s.config = config
	s.bufferScale = waylandBufferScale(config.Scale)
	s.mu.Unlock()

	if err := s.client.withProtocolLock(func() error {
		s.mu.Lock()
		destroyed := s.destroyed
		wlSurface := s.wlSurface
		layerSurface := s.layerSurface
		s.mu.Unlock()
		if destroyed || wlSurface == nil || layerSurface == nil {
			return nil
		}
		if err := layerSurface.SetSize(uint32(config.Width), uint32(config.Height)); err != nil {
			return fmt.Errorf("set layer surface size: %w", err)
		}
		if err := layerSurface.SetAnchor(layerSurfaceAnchorAllEdges()); err != nil {
			return fmt.Errorf("anchor layer surface: %w", err)
		}
		if err := layerSurface.SetExclusiveZone(-1); err != nil {
			return fmt.Errorf("set layer surface exclusive zone: %w", err)
		}
		if err := layerSurface.SetMargin(0, 0, 0, 0); err != nil {
			return fmt.Errorf("clear layer surface margins: %w", err)
		}
		if err := s.setEmptyInputRegionLocked(wlSurface); err != nil {
			return err
		}
		if err := wlSurface.Commit(); err != nil {
			return fmt.Errorf("commit initial layer surface state: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}

	event, err := s.waitForInitialConfigure(ctx)
	if err != nil {
		return err
	}
	return s.ackConfigure(event)
}

func (s *layerShellSurface) GrabKeyboard(ctx context.Context) error {
	if s == nil {
		return fmt.Errorf("layer-shell surface is nil")
	}
	s.mu.Lock()
	if s.destroyed {
		s.mu.Unlock()
		return fmt.Errorf("layer-shell surface is destroyed")
	}
	s.keyboardExclusive = true
	s.mu.Unlock()

	return s.client.withProtocolLock(func() error {
		s.mu.Lock()
		destroyed := s.destroyed
		layerSurface := s.layerSurface
		s.mu.Unlock()
		if destroyed || layerSurface == nil {
			return nil
		}
		if err := layerSurface.SetKeyboardInteractivity(uint32(wlrlayershell.LayerSurfaceKeyboardInteractivityExclusive)); err != nil {
			return fmt.Errorf("set keyboard_interactivity=exclusive: %w", err)
		}
		return nil
	})
}

func (s *layerShellSurface) Render(ctx context.Context, buffer ARGBBuffer) error {
	if s == nil {
		return fmt.Errorf("layer-shell surface is nil")
	}
	if err := buffer.Validate(); err != nil {
		return err
	}
	if err := s.renderBuffer(ctx, buffer); err != nil {
		return err
	}
	s.startEventLoop()
	return nil
}

func (s *layerShellSurface) Closed() <-chan struct{} {
	if s == nil {
		return nil
	}
	return s.closed
}

func (s *layerShellSurface) Destroy(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.destroyOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		s.closeOnce.Do(func() {
			if s.closed != nil {
				close(s.closed)
			}
		})
		s.client.unregisterOutputChangeListener(s.outputName, s)

		s.mu.Lock()
		s.destroyed = true
		buffers := make([]*waylandSHMBuffer, 0, 1+len(s.retired))
		if s.current != nil {
			buffers = append(buffers, s.current)
			s.current = nil
		}
		for buffer := range s.retired {
			buffers = append(buffers, buffer)
		}
		s.retired = map[*waylandSHMBuffer]struct{}{}
		s.mu.Unlock()

		s.destroyErr = s.client.withProtocolLock(func() error {
			var firstErr error
			if s.wlSurface != nil && len(buffers) > 0 {
				if err := s.wlSurface.Attach(nil, 0, 0); err != nil && firstErr == nil {
					firstErr = fmt.Errorf("detach layer-shell buffer: %w", err)
				}
				if err := s.wlSurface.Commit(); err != nil && firstErr == nil {
					firstErr = fmt.Errorf("commit layer-shell unmap: %w", err)
				}
			}
			if s.layerSurface != nil {
				if err := s.layerSurface.Destroy(); err != nil && firstErr == nil {
					firstErr = fmt.Errorf("destroy layer surface: %w", err)
				}
				s.layerSurface = nil
			}
			if s.wlSurface != nil {
				if err := s.wlSurface.Destroy(); err != nil && firstErr == nil {
					firstErr = fmt.Errorf("destroy wl_surface: %w", err)
				}
				s.wlSurface = nil
			}
			for _, buffer := range buffers {
				if err := buffer.destroyProtocolLocked(); err != nil && firstErr == nil {
					firstErr = err
				}
			}
			return firstErr
		})
	})
	return s.destroyErr
}

func (s *layerShellSurface) WaylandOutputChanged(monitor Monitor) {
	if s == nil {
		return
	}
	s.enqueue(layerShellSurfaceEvent{
		kind:    layerShellSurfaceEventOutputChanged,
		monitor: monitor,
	})
}

func (s *layerShellSurface) waitForInitialConfigure(ctx context.Context) (layerShellSurfaceEvent, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		select {
		case <-ctx.Done():
			return layerShellSurfaceEvent{}, fmt.Errorf("wait for initial layer-shell configure: %w", ctx.Err())
		case <-s.ctx.Done():
			return layerShellSurfaceEvent{}, fmt.Errorf("wait for initial layer-shell configure: %w", s.ctx.Err())
		case event := <-s.events:
			switch event.kind {
			case layerShellSurfaceEventConfigure:
				return event, nil
			case layerShellSurfaceEventClosed:
				return layerShellSurfaceEvent{}, fmt.Errorf("layer-shell surface closed before initial configure")
			}
		}
	}
}

func (s *layerShellSurface) ackConfigure(event layerShellSurfaceEvent) error {
	s.mu.Lock()
	if event.width > 0 {
		s.config.Width = event.width
	}
	if event.height > 0 {
		s.config.Height = event.height
	}
	s.mu.Unlock()

	return s.client.withProtocolLock(func() error {
		s.mu.Lock()
		destroyed := s.destroyed
		layerSurface := s.layerSurface
		s.mu.Unlock()
		if destroyed || layerSurface == nil {
			return nil
		}
		if err := layerSurface.AckConfigure(event.serial); err != nil {
			return fmt.Errorf("ack layer-shell configure: %w", err)
		}
		return nil
	})
}

func (s *layerShellSurface) startEventLoop() {
	s.eventOnce.Do(func() {
		go s.runEventLoop()
	})
}

func (s *layerShellSurface) runEventLoop() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case event := <-s.events:
			switch event.kind {
			case layerShellSurfaceEventConfigure:
				if err := s.ackConfigure(event); err == nil {
					_ = s.renderForCurrentConfig(s.ctx)
				}
			case layerShellSurfaceEventOutputChanged:
				_ = s.applyOutputChange(s.ctx, event.monitor)
			case layerShellSurfaceEventBufferReleased:
				_ = s.releaseBuffer(event.buffer)
			case layerShellSurfaceEventClosed:
				_ = s.Destroy(s.ctx)
			}
		}
	}
}

func (s *layerShellSurface) applyOutputChange(ctx context.Context, monitor Monitor) error {
	if monitor.Name != s.outputName {
		return nil
	}
	if err := validateSurfaceConfig(SurfaceConfig{
		OutputName: monitor.Name,
		Width:      monitor.Width,
		Height:     monitor.Height,
		Scale:      monitor.Scale,
	}); err != nil {
		return err
	}

	s.mu.Lock()
	if s.destroyed {
		s.mu.Unlock()
		return nil
	}
	s.config = SurfaceConfig{
		OutputName: monitor.Name,
		Width:      monitor.Width,
		Height:     monitor.Height,
		Scale:      monitor.Scale,
	}
	s.bufferScale = waylandBufferScale(monitor.Scale)
	s.mu.Unlock()

	return s.renderForCurrentConfig(ctx)
}

func (s *layerShellSurface) renderForCurrentConfig(ctx context.Context) error {
	s.mu.Lock()
	config := s.config
	rerenderer := s.rerenderer
	s.mu.Unlock()

	var buffer ARGBBuffer
	var err error
	if rerenderer != nil {
		buffer, err = rerenderer(config)
	} else {
		buffer, err = NewARGBBuffer(config.Width, config.Height)
		if err == nil {
			RenderPlaceholderOverlay(buffer)
		}
	}
	if err != nil {
		return err
	}
	return s.renderBuffer(ctx, buffer)
}

func (s *layerShellSurface) renderBuffer(ctx context.Context, buffer ARGBBuffer) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	if s.destroyed {
		s.mu.Unlock()
		return fmt.Errorf("layer-shell surface is destroyed")
	}
	scale := s.bufferScale
	keyboardExclusive := s.keyboardExclusive
	s.config.Width = buffer.Width
	s.config.Height = buffer.Height
	config := s.config
	s.mu.Unlock()

	if scale <= 0 {
		scale = 1
	}
	var created *waylandSHMBuffer
	var old *waylandSHMBuffer
	if err := s.client.withProtocolLock(func() error {
		s.mu.Lock()
		destroyed := s.destroyed
		wlSurface := s.wlSurface
		layerSurface := s.layerSurface
		s.mu.Unlock()
		if destroyed || wlSurface == nil || layerSurface == nil {
			return nil
		}

		var err error
		created, err = newWaylandSHMBufferLocked(s.client.Shm(), buffer.Width*scale, buffer.Height*scale)
		if err != nil {
			return err
		}
		copyARGBToWaylandSHM(created.data, buffer, scale)
		created.buffer.SetReleaseHandler(func(wlclient.BufferReleaseEvent) {
			s.enqueue(layerShellSurfaceEvent{kind: layerShellSurfaceEventBufferReleased, buffer: created})
		})

		if err := layerSurface.SetSize(uint32(config.Width), uint32(config.Height)); err != nil {
			return fmt.Errorf("set layer surface size before render: %w", err)
		}
		if keyboardExclusive {
			if err := layerSurface.SetKeyboardInteractivity(uint32(wlrlayershell.LayerSurfaceKeyboardInteractivityExclusive)); err != nil {
				return fmt.Errorf("set keyboard_interactivity=exclusive before render: %w", err)
			}
		}
		if err := wlSurface.SetBufferScale(int32(scale)); err != nil {
			return fmt.Errorf("set layer-shell buffer scale: %w", err)
		}
		if err := wlSurface.Attach(created.buffer, 0, 0); err != nil {
			return fmt.Errorf("attach layer-shell shm buffer: %w", err)
		}
		if err := wlSurface.DamageBuffer(0, 0, int32(created.width), int32(created.height)); err != nil {
			return fmt.Errorf("damage layer-shell shm buffer: %w", err)
		}
		if err := wlSurface.Commit(); err != nil {
			return fmt.Errorf("commit layer-shell shm buffer: %w", err)
		}

		s.mu.Lock()
		old = s.current
		s.current = created
		if old != nil {
			s.retired[old] = struct{}{}
		}
		s.mu.Unlock()
		return nil
	}); err != nil {
		if created != nil {
			_ = s.destroyBuffer(created)
		}
		return err
	}
	return nil
}

func (s *layerShellSurface) releaseBuffer(buffer *waylandSHMBuffer) error {
	if buffer == nil {
		return nil
	}
	s.mu.Lock()
	if s.current == buffer {
		s.current = nil
	}
	delete(s.retired, buffer)
	s.mu.Unlock()
	return s.destroyBuffer(buffer)
}

func (s *layerShellSurface) destroyBuffer(buffer *waylandSHMBuffer) error {
	return s.client.withProtocolLock(func() error {
		return buffer.destroyProtocolLocked()
	})
}

func (s *layerShellSurface) setEmptyInputRegionLocked(surface *wlclient.Surface) error {
	compositor := s.client.Compositor()
	if compositor == nil {
		return fmt.Errorf("Wayland compositor is not bound")
	}
	if surface == nil {
		return nil
	}
	region, err := compositor.CreateRegion()
	if err != nil {
		return fmt.Errorf("create empty pointer input region: %w", err)
	}
	if err := surface.SetInputRegion(region); err != nil {
		_ = region.Destroy()
		return fmt.Errorf("set empty pointer input region: %w", err)
	}
	if err := region.Destroy(); err != nil {
		return fmt.Errorf("destroy temporary pointer input region: %w", err)
	}
	return nil
}

func (s *layerShellSurface) enqueue(event layerShellSurfaceEvent) {
	s.mu.Lock()
	destroyed := s.destroyed
	events := s.events
	ctx := s.ctx
	s.mu.Unlock()
	if destroyed || events == nil {
		return
	}
	if ctx == nil {
		events <- event
		return
	}
	select {
	case events <- event:
	case <-ctx.Done():
	}
}

type waylandSHMBuffer struct {
	buffer *wlclient.Buffer
	pool   *wlclient.ShmPool
	file   *os.File
	data   []byte
	width  int
	height int
	size   int
	once   sync.Once
}

func newWaylandSHMBufferLocked(shm *wlclient.Shm, width, height int) (*waylandSHMBuffer, error) {
	if shm == nil {
		return nil, fmt.Errorf("Wayland shm global is not bound")
	}
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid shm buffer size %dx%d", width, height)
	}
	stride := width * 4
	size := stride * height
	file, err := createWaylandSHMFile(size)
	if err != nil {
		return nil, err
	}

	data, err := unix.Mmap(int(file.Fd()), 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("mmap Wayland shm buffer: %w", err)
	}

	pool, err := shm.CreatePool(int(file.Fd()), int32(size))
	if err != nil {
		_ = unix.Munmap(data)
		_ = file.Close()
		return nil, fmt.Errorf("create Wayland shm pool: %w", err)
	}
	buffer, err := pool.CreateBuffer(0, int32(width), int32(height), int32(stride), uint32(wlclient.ShmFormatArgb8888))
	if err != nil {
		_ = pool.Destroy()
		_ = unix.Munmap(data)
		_ = file.Close()
		return nil, fmt.Errorf("create Wayland shm buffer: %w", err)
	}

	return &waylandSHMBuffer{
		buffer: buffer,
		pool:   pool,
		file:   file,
		data:   data,
		width:  width,
		height: height,
		size:   size,
	}, nil
}

func (b *waylandSHMBuffer) destroyProtocolLocked() error {
	if b == nil {
		return nil
	}
	var err error
	b.once.Do(func() {
		if b.buffer != nil {
			if destroyErr := b.buffer.Destroy(); destroyErr != nil && err == nil {
				err = fmt.Errorf("destroy Wayland shm buffer: %w", destroyErr)
			}
			b.buffer = nil
		}
		if b.pool != nil {
			if destroyErr := b.pool.Destroy(); destroyErr != nil && err == nil {
				err = fmt.Errorf("destroy Wayland shm pool: %w", destroyErr)
			}
			b.pool = nil
		}
		if b.data != nil {
			if destroyErr := unix.Munmap(b.data); destroyErr != nil && err == nil {
				err = fmt.Errorf("munmap Wayland shm buffer: %w", destroyErr)
			}
			b.data = nil
		}
		if b.file != nil {
			if destroyErr := b.file.Close(); destroyErr != nil && err == nil {
				err = fmt.Errorf("close Wayland shm file: %w", destroyErr)
			}
			b.file = nil
		}
	})
	return err
}

func createWaylandSHMFile(size int) (*os.File, error) {
	if size <= 0 {
		return nil, fmt.Errorf("invalid Wayland shm file size %d", size)
	}
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		return nil, fmt.Errorf("XDG_RUNTIME_DIR is required for Wayland shm buffers")
	}
	file, err := os.CreateTemp(runtimeDir, "mousekeys-shm-*")
	if err != nil {
		return nil, fmt.Errorf("create Wayland shm temp file: %w", err)
	}
	if err := file.Truncate(int64(size)); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, fmt.Errorf("resize Wayland shm temp file: %w", err)
	}
	if err := os.Remove(file.Name()); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("unlink Wayland shm temp file: %w", err)
	}
	return file, nil
}

func copyARGBToWaylandSHM(dst []byte, src ARGBBuffer, scale int) {
	if scale <= 0 {
		scale = 1
	}
	for y := 0; y < src.Height*scale; y++ {
		srcY := y / scale
		for x := 0; x < src.Width*scale; x++ {
			srcX := x / scale
			pixel := premultiplyARGB(src.Pixels[srcY*src.Stride+srcX])
			offset := ((y * src.Width * scale) + x) * 4
			binary.LittleEndian.PutUint32(dst[offset:offset+4], pixel)
		}
	}
}

func premultiplyARGB(pixel uint32) uint32 {
	// wl_shm ARGB buffers are consumed as premultiplied alpha by compositors.
	alpha := int((pixel >> 24) & 0xff)
	if alpha == 0 {
		return 0
	}
	if alpha == 255 {
		return pixel
	}
	red := div255(int((pixel>>16)&0xff) * alpha)
	green := div255(int((pixel>>8)&0xff) * alpha)
	blue := div255(int(pixel&0xff) * alpha)
	return uint32(alpha<<24 | red<<16 | green<<8 | blue)
}

func validateSurfaceConfig(config SurfaceConfig) error {
	if config.OutputName == "" {
		return fmt.Errorf("layer-shell output name is required")
	}
	if config.Width <= 0 || config.Height <= 0 {
		return fmt.Errorf("invalid layer-shell surface size %dx%d", config.Width, config.Height)
	}
	if config.Scale <= 0 {
		return fmt.Errorf("invalid layer-shell output scale %.2f", config.Scale)
	}
	return nil
}

func waylandBufferScale(scale float64) int {
	if scale <= 1 {
		return 1
	}
	return int(math.Ceil(scale))
}

func layerSurfaceAnchorAllEdges() uint32 {
	return uint32(wlrlayershell.LayerSurfaceAnchorTop |
		wlrlayershell.LayerSurfaceAnchorBottom |
		wlrlayershell.LayerSurfaceAnchorLeft |
		wlrlayershell.LayerSurfaceAnchorRight)
}

type waylandOutputChangeListener interface {
	WaylandOutputChanged(Monitor)
}

type waylandOutputListenerNotification struct {
	monitor   Monitor
	listeners []waylandOutputChangeListener
}

func (c *WaylandClient) registerOutputChangeListener(outputName string, listener waylandOutputChangeListener) {
	if c == nil || outputName == "" || listener == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.outputListeners == nil {
		c.outputListeners = map[string]map[waylandOutputChangeListener]struct{}{}
	}
	listeners := c.outputListeners[outputName]
	if listeners == nil {
		listeners = map[waylandOutputChangeListener]struct{}{}
		c.outputListeners[outputName] = listeners
	}
	listeners[listener] = struct{}{}
}

func (c *WaylandClient) unregisterOutputChangeListener(outputName string, listener waylandOutputChangeListener) {
	if c == nil || outputName == "" || listener == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	listeners := c.outputListeners[outputName]
	if listeners == nil {
		return
	}
	delete(listeners, listener)
	if len(listeners) == 0 {
		delete(c.outputListeners, outputName)
	}
}

func (c *WaylandClient) outputListenerSnapshotLocked(state *waylandOutputState) waylandOutputListenerNotification {
	if c == nil || state == nil {
		return waylandOutputListenerNotification{}
	}
	monitor, err := state.monitor()
	if err != nil {
		return waylandOutputListenerNotification{}
	}
	listenerSet := c.outputListeners[monitor.Name]
	if len(listenerSet) == 0 {
		return waylandOutputListenerNotification{}
	}
	listeners := make([]waylandOutputChangeListener, 0, len(listenerSet))
	for listener := range listenerSet {
		listeners = append(listeners, listener)
	}
	return waylandOutputListenerNotification{monitor: monitor, listeners: listeners}
}

func notifyWaylandOutputListeners(notification waylandOutputListenerNotification) {
	for _, listener := range notification.listeners {
		listener.WaylandOutputChanged(notification.monitor)
	}
}

func (c *WaylandClient) withProtocolLock(fn func() error) error {
	if c == nil || c.display == nil {
		return fmt.Errorf("Wayland client is nil")
	}
	if err := c.protocolError(); err != nil {
		return err
	}
	c.protocolMu.Lock()
	defer c.protocolMu.Unlock()
	if err := c.protocolError(); err != nil {
		return err
	}
	if err := fn(); err != nil {
		return err
	}
	return c.protocolError()
}

func (c *WaylandClient) StartDispatchLoop(ctx context.Context) {
	if c == nil || c.display == nil {
		return
	}
	c.dispatchOnce.Do(func() {
		go c.dispatchLoop(ctx)
	})
}

func (c *WaylandClient) dispatchLoop(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	conn, err := c.unixConn()
	if err != nil {
		c.setProtocolErr(err)
		return
	}

	for {
		if err := ctx.Err(); err != nil {
			return
		}

		c.protocolMu.Lock()
		_ = conn.SetReadDeadline(nextWaylandDispatchDeadline(ctx))
		err := c.display.Context().Dispatch()
		_ = conn.SetReadDeadline(time.Time{})
		c.protocolMu.Unlock()

		if err != nil {
			if isTimeoutError(err) {
				continue
			}
			if isUnknownWaylandSenderError(err) {
				continue
			}
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			c.setProtocolErr(fmt.Errorf("dispatch Wayland events: %w", err))
			return
		}
		if err := c.protocolError(); err != nil {
			return
		}
	}
}
