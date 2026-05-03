package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"sync"
	"time"

	"mousekeys/internal/wayland/wlr"

	"github.com/rajveermalviya/go-wayland/wayland/client"
	"golang.org/x/sys/unix"
)

const (
	layerShellOverlayNamespace = "mousekeys"
)

type realWaylandOverlayBackend struct {
	base     *WaylandClientBase
	driver   *realWaylandBaseDriver
	trace    *TraceRecorder
	wlMu     sync.Mutex
	fatalMu  sync.Mutex
	fatalErr error
}

func newRealWaylandOverlayBackend(base *WaylandClientBase, trace *TraceRecorder) (*realWaylandOverlayBackend, error) {
	if base == nil {
		return nil, fmt.Errorf("Wayland client base is nil")
	}
	driver, ok := base.driver.(*realWaylandBaseDriver)
	if !ok || driver == nil {
		return nil, fmt.Errorf("Wayland overlay requires the real Wayland client driver")
	}
	backend := &realWaylandOverlayBackend{
		base:   base,
		driver: driver,
		trace:  trace,
	}
	base.mu.RLock()
	display := base.display
	base.mu.RUnlock()
	if display != nil {
		display.SetErrorHandler(func(ev client.DisplayErrorEvent) {
			backend.setFatal(fmt.Errorf("Wayland protocol fatal error on object %d: code=%d message=%q", proxyID(ev.ObjectId), ev.Code, ev.Message))
		})
	}
	return backend, nil
}

func (b *realWaylandOverlayBackend) CreateSurface(ctx context.Context, monitor Monitor) (OverlaySurface, error) {
	if err := monitor.Validate(); err != nil {
		return nil, err
	}

	compositor, layerShell, output, err := b.overlayObjects(monitor)
	if err != nil {
		return nil, err
	}

	surface := &realLayerShellSurface{
		backend:   b,
		monitor:   monitor,
		lifecycle: newRealOverlayLifecycleEventSource(b),
	}

	err = b.withWayland(ctx, func() error {
		wlSurface, err := compositor.CreateSurface()
		if err != nil {
			return fmt.Errorf("create wl_surface: %w", err)
		}
		layerSurface, err := layerShell.GetLayerSurface(wlSurface, output, uint32(wlr.LayerShellLayerTop), layerShellOverlayNamespace)
		if err != nil {
			return fmt.Errorf("create wlr layer surface: %w", err)
		}
		surface.wlSurface = wlSurface
		surface.layerSurface = layerSurface

		layerSurface.SetConfigureHandler(func(ev wlr.LayerSurfaceConfigureEvent) {
			surface.handleConfigure(ev)
		})
		layerSurface.SetClosedHandler(func(wlr.LayerSurfaceClosedEvent) {
			surface.handleClosed()
		})
		wlSurface.SetEnterHandler(func(client.SurfaceEnterEvent) {
			surface.handleOutputChange()
		})
		wlSurface.SetLeaveHandler(func(client.SurfaceLeaveEvent) {
			surface.handleOutputChange()
		})

		anchor := uint32(wlr.LayerSurfaceAnchorTop | wlr.LayerSurfaceAnchorBottom | wlr.LayerSurfaceAnchorLeft | wlr.LayerSurfaceAnchorRight)
		if err := layerSurface.SetSize(uint32(monitor.LogicalWidth), uint32(monitor.LogicalHeight)); err != nil {
			return fmt.Errorf("set layer surface size: %w", err)
		}
		if err := layerSurface.SetAnchor(anchor); err != nil {
			return fmt.Errorf("set layer surface anchor: %w", err)
		}
		if err := layerSurface.SetExclusiveZone(-1); err != nil {
			return fmt.Errorf("set layer surface exclusive zone: %w", err)
		}
		if err := layerSurface.SetMargin(0, 0, 0, 0); err != nil {
			return fmt.Errorf("set layer surface margin: %w", err)
		}
		if err := layerSurface.SetKeyboardInteractivity(uint32(wlr.LayerSurfaceKeyboardInteractivityExclusive)); err != nil {
			return fmt.Errorf("set layer surface keyboard interactivity: %w", err)
		}
		if err := wlSurface.Commit(); err != nil {
			return fmt.Errorf("commit initial layer surface state: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	b.trace.Record(traceOverlaySurfaceCreate, map[string]any{
		"monitor":                  monitor,
		"layer":                    wlr.LayerShellLayerTop.Name(),
		"anchor":                   "top|bottom|left|right",
		"keyboard_interactivity":   wlr.LayerSurfaceKeyboardInteractivityExclusive.Name(),
		"cursor_visibility_change": false,
	})
	return surface, nil
}

func (b *realWaylandOverlayBackend) OutputChanged(ctx context.Context, monitor Monitor) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.trace.Record(traceOverlayOutputChange, map[string]any{"monitor": monitor})
	return nil
}

func (b *realWaylandOverlayBackend) Close(ctx context.Context) error {
	b.trace.Record(traceOverlayClose, nil)
	if b.base == nil {
		return nil
	}
	return b.base.Close(ctx)
}

func (b *realWaylandOverlayBackend) overlayObjects(monitor Monitor) (*client.Compositor, *wlr.LayerShell, *client.Output, error) {
	outputInfo, err := b.base.OutputForMonitor(monitor)
	if err != nil {
		return nil, nil, nil, err
	}
	b.base.mu.RLock()
	defer b.base.mu.RUnlock()
	var output *client.Output
	if state := b.base.outputs[outputInfo.GlobalName]; state != nil {
		output = state.proxy
	}
	if b.base.compositor == nil {
		return nil, nil, nil, fmt.Errorf("Wayland compositor global is not bound")
	}
	if b.base.layerShell == nil {
		return nil, nil, nil, fmt.Errorf("wlr layer-shell global is not bound")
	}
	if output == nil {
		return nil, nil, nil, fmt.Errorf("Wayland output %q is matched but has no bound wl_output proxy", outputInfo.Name)
	}
	return b.base.compositor, b.base.layerShell, output, nil
}

func (b *realWaylandOverlayBackend) keyboard() (*client.Keyboard, error) {
	b.base.mu.RLock()
	defer b.base.mu.RUnlock()
	if b.base.keyboard == nil {
		return nil, fmt.Errorf("Wayland keyboard is not bound")
	}
	return b.base.keyboard, nil
}

func (b *realWaylandOverlayBackend) shm() (*client.Shm, error) {
	b.base.mu.RLock()
	defer b.base.mu.RUnlock()
	if b.base.shm == nil {
		return nil, fmt.Errorf("Wayland shm is not bound")
	}
	return b.base.shm, nil
}

func (b *realWaylandOverlayBackend) withWayland(ctx context.Context, fn func() error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.fatal(); err != nil {
		return err
	}
	b.wlMu.Lock()
	defer b.wlMu.Unlock()
	if err := b.fatal(); err != nil {
		return err
	}
	if err := fn(); err != nil {
		return err
	}
	return b.fatal()
}

func (b *realWaylandOverlayBackend) dispatchOnce(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.fatal(); err != nil {
		return err
	}
	b.wlMu.Lock()
	defer b.wlMu.Unlock()
	if err := b.fatal(); err != nil {
		return err
	}
	err := b.driver.dispatchOnce(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err = fmt.Errorf("Wayland event dispatch failed: %w", err)
		b.setFatal(err)
		return err
	}
	return b.fatal()
}

func (b *realWaylandOverlayBackend) fatal() error {
	b.fatalMu.Lock()
	defer b.fatalMu.Unlock()
	return b.fatalErr
}

func (b *realWaylandOverlayBackend) setFatal(err error) {
	if err == nil {
		return
	}
	b.fatalMu.Lock()
	if b.fatalErr == nil {
		b.fatalErr = err
	}
	b.fatalMu.Unlock()
	b.trace.Record(traceOverlayError, map[string]any{"error": err.Error()})
}

type realLayerShellSurface struct {
	backend      *realWaylandOverlayBackend
	monitor      Monitor
	wlSurface    *client.Surface
	layerSurface *wlr.LayerSurface
	lifecycle    *realOverlayLifecycleEventSource

	mu          sync.Mutex
	configured  bool
	closed      bool
	destroyed   bool
	width       int
	height      int
	scale       float64
	buffers     []*realWaylandSHMBuffer
	keyboardSrc *realWaylandKeyboardEventSource
}

func (s *realLayerShellSurface) Configure(ctx context.Context, width, height int, scale float64) error {
	if err := s.waitForConfigure(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	if s.width > 0 {
		width = s.width
	}
	if s.height > 0 {
		height = s.height
	}
	if scale > 0 {
		s.scale = scale
	}
	s.monitor.LogicalWidth = width
	s.monitor.LogicalHeight = height
	s.monitor.Scale = scale
	s.mu.Unlock()
	s.backend.trace.Record(traceOverlayConfigure, map[string]any{
		"width":  width,
		"height": height,
		"scale":  scale,
	})
	return nil
}

func (s *realLayerShellSurface) Render(ctx context.Context, buffer ARGBSnapshot) error {
	if buffer.Width <= 0 || buffer.Height <= 0 {
		return fmt.Errorf("cannot render empty overlay buffer %dx%d", buffer.Width, buffer.Height)
	}
	s.mu.Lock()
	if s.destroyed {
		s.mu.Unlock()
		return io.ErrClosedPipe
	}
	scale := waylandIntegerBufferScale(s.monitor.Scale)
	s.mu.Unlock()

	scaled := scaleARGBSnapshotNearest(buffer, scale)
	shmBuffer, err := s.backend.newSHMBuffer(ctx, scaled, s)
	if err != nil {
		return err
	}

	err = s.backend.withWayland(ctx, func() error {
		if err := s.wlSurface.SetBufferScale(int32(scale)); err != nil {
			return fmt.Errorf("set wl_surface buffer scale: %w", err)
		}
		if err := s.wlSurface.Attach(shmBuffer.buffer, 0, 0); err != nil {
			return fmt.Errorf("attach wl_buffer: %w", err)
		}
		if err := s.wlSurface.DamageBuffer(0, 0, int32(scaled.Width), int32(scaled.Height)); err != nil {
			return fmt.Errorf("damage wl_surface buffer: %w", err)
		}
		if err := s.wlSurface.Commit(); err != nil {
			return fmt.Errorf("commit wl_surface buffer: %w", err)
		}
		return nil
	})
	if err != nil {
		_ = shmBuffer.destroy(context.Background())
		return err
	}

	s.mu.Lock()
	s.buffers = append(s.buffers, shmBuffer)
	s.mu.Unlock()
	s.cleanupReleasedBuffers(ctx)
	s.backend.trace.Record(traceOverlayRender, map[string]any{
		"width":         buffer.Width,
		"height":        buffer.Height,
		"buffer_width":  scaled.Width,
		"buffer_height": scaled.Height,
		"buffer_scale":  scale,
		"hash":          buffer.StraightHash(),
	})
	return nil
}

func (s *realLayerShellSurface) GrabKeyboard(ctx context.Context) (KeyboardEventSource, error) {
	keyboard, err := s.backend.keyboard()
	if err != nil {
		return nil, err
	}
	source := newRealWaylandKeyboardEventSource(s.backend)
	keyboard.SetKeymapHandler(func(ev client.KeyboardKeymapEvent) {
		source.handleKeymap(ev)
	})
	keyboard.SetEnterHandler(func(client.KeyboardEnterEvent) {
		source.enqueue(KeyboardEvent{Kind: KeyboardEventEnter})
	})
	keyboard.SetLeaveHandler(func(client.KeyboardLeaveEvent) {
		source.enqueue(KeyboardEvent{Kind: KeyboardEventLeave})
	})
	keyboard.SetKeyHandler(func(ev client.KeyboardKeyEvent) {
		source.enqueue(KeyboardEvent{
			Kind:  KeyboardEventKey,
			Key:   waylandKeyName(ev.Key),
			State: waylandKeyState(ev.State),
			At:    time.Now(),
		})
	})
	keyboard.SetModifiersHandler(func(ev client.KeyboardModifiersEvent) {
		source.enqueue(KeyboardEvent{
			Kind:      KeyboardEventModifiers,
			Modifiers: waylandModifiers(ev),
		})
	})
	keyboard.SetRepeatInfoHandler(func(ev client.KeyboardRepeatInfoEvent) {
		source.enqueue(KeyboardEvent{
			Kind:        KeyboardEventRepeat,
			RepeatRate:  int(ev.Rate),
			RepeatDelay: time.Duration(ev.Delay) * time.Millisecond,
		})
	})
	s.mu.Lock()
	s.keyboardSrc = source
	s.mu.Unlock()
	s.backend.trace.Record(traceOverlayKeyboardGrab, nil)
	return source, nil
}

func (s *realLayerShellSurface) Unmap(ctx context.Context) error {
	s.mu.Lock()
	if s.destroyed || s.wlSurface == nil {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	err := s.backend.withWayland(ctx, func() error {
		if err := s.wlSurface.Attach(nil, 0, 0); err != nil {
			return fmt.Errorf("attach null buffer for unmap: %w", err)
		}
		return s.wlSurface.Commit()
	})
	s.backend.trace.Record(traceOverlayUnmap, nil)
	return err
}

func (s *realLayerShellSurface) Destroy(ctx context.Context) error {
	s.mu.Lock()
	if s.destroyed {
		s.mu.Unlock()
		return nil
	}
	s.destroyed = true
	layerSurface := s.layerSurface
	wlSurface := s.wlSurface
	keyboardSrc := s.keyboardSrc
	lifecycle := s.lifecycle
	buffers := s.buffers
	s.buffers = nil
	s.mu.Unlock()

	var errs []error
	if keyboardSrc != nil {
		errs = append(errs, keyboardSrc.Close())
	}
	if lifecycle != nil {
		errs = append(errs, lifecycle.Close())
	}
	errs = append(errs, s.backend.withWayland(ctx, func() error {
		var waylandErrs []error
		if layerSurface != nil {
			waylandErrs = append(waylandErrs, layerSurface.Destroy())
		}
		if wlSurface != nil {
			waylandErrs = append(waylandErrs, wlSurface.Destroy())
		}
		return errors.Join(waylandErrs...)
	}))
	for _, buffer := range buffers {
		errs = append(errs, buffer.destroy(ctx))
	}
	s.backend.trace.Record(traceOverlayDestroy, nil)
	return errors.Join(errs...)
}

func (s *realLayerShellSurface) LifecycleEvents() OverlayLifecycleEventSource {
	if s == nil {
		return nil
	}
	return s.lifecycle
}

func (s *realLayerShellSurface) waitForConfigure(ctx context.Context) error {
	for {
		s.mu.Lock()
		configured := s.configured
		closed := s.closed
		destroyed := s.destroyed
		s.mu.Unlock()
		if configured {
			return nil
		}
		if closed {
			return fmt.Errorf("layer surface closed before configure")
		}
		if destroyed {
			return io.ErrClosedPipe
		}
		if err := s.backend.dispatchOnce(ctx); err != nil {
			return err
		}
	}
}

func (s *realLayerShellSurface) handleConfigure(ev wlr.LayerSurfaceConfigureEvent) {
	if err := s.layerSurface.AckConfigure(ev.Serial); err != nil {
		s.backend.setFatal(fmt.Errorf("ack layer surface configure %d: %w", ev.Serial, err))
		return
	}
	width := int(ev.Width)
	height := int(ev.Height)
	s.mu.Lock()
	first := !s.configured
	if width > 0 {
		s.width = width
	}
	if height > 0 {
		s.height = height
	}
	s.configured = true
	scale := s.monitor.Scale
	s.mu.Unlock()
	s.backend.trace.Record(traceOverlayConfigure, map[string]any{
		"serial": ev.Serial,
		"width":  width,
		"height": height,
		"scale":  scale,
	})
	if !first {
		s.lifecycle.enqueue(OverlayLifecycleEvent{
			Kind:   OverlayLifecycleConfigure,
			Width:  width,
			Height: height,
			Scale:  scale,
		})
	}
}

func (s *realLayerShellSurface) handleClosed() {
	s.mu.Lock()
	s.closed = true
	keyboardSrc := s.keyboardSrc
	s.mu.Unlock()
	s.backend.trace.Record(traceOverlayClose, nil)
	s.lifecycle.enqueue(OverlayLifecycleEvent{Kind: OverlayLifecycleCompositorClose})
	if keyboardSrc != nil {
		keyboardSrc.enqueue(KeyboardEvent{Kind: KeyboardEventDestroy})
	}
}

func (s *realLayerShellSurface) handleOutputChange() {
	s.mu.Lock()
	monitor := s.monitor
	s.mu.Unlock()
	s.lifecycle.enqueue(OverlayLifecycleEvent{Kind: OverlayLifecycleOutputChange, Monitor: monitor})
}

func (s *realLayerShellSurface) handleBufferRelease(buffer *realWaylandSHMBuffer) {
	if buffer == nil {
		return
	}
	buffer.markReleased()
	s.backend.trace.Record(traceOverlayRelease, map[string]any{
		"width":  buffer.width,
		"height": buffer.height,
	})
	s.lifecycle.enqueue(OverlayLifecycleEvent{Kind: OverlayLifecycleRelease, Width: buffer.width, Height: buffer.height})
}

func (s *realLayerShellSurface) cleanupReleasedBuffers(ctx context.Context) {
	s.mu.Lock()
	var keep []*realWaylandSHMBuffer
	var released []*realWaylandSHMBuffer
	for _, buffer := range s.buffers {
		if buffer.released() {
			released = append(released, buffer)
			continue
		}
		keep = append(keep, buffer)
	}
	s.buffers = keep
	s.mu.Unlock()
	for _, buffer := range released {
		_ = buffer.destroy(ctx)
	}
}

type realOverlayLifecycleEventSource struct {
	backend *realWaylandOverlayBackend
	queue   *overlayEventQueue[OverlayLifecycleEvent]
}

func newRealOverlayLifecycleEventSource(backend *realWaylandOverlayBackend) *realOverlayLifecycleEventSource {
	return &realOverlayLifecycleEventSource{
		backend: backend,
		queue:   newOverlayEventQueue[OverlayLifecycleEvent](),
	}
}

func (s *realOverlayLifecycleEventSource) NextOverlayEvent(ctx context.Context) (OverlayLifecycleEvent, error) {
	if s == nil {
		return OverlayLifecycleEvent{}, io.ErrClosedPipe
	}
	return s.queue.pop(ctx, s.backend.dispatchOnce)
}

func (s *realOverlayLifecycleEventSource) Close() error {
	if s == nil {
		return nil
	}
	return s.queue.close()
}

func (s *realOverlayLifecycleEventSource) enqueue(event OverlayLifecycleEvent) {
	if s == nil {
		return
	}
	s.queue.push(event)
}

type realWaylandKeyboardEventSource struct {
	backend *realWaylandOverlayBackend
	queue   *overlayEventQueue[KeyboardEvent]
}

func newRealWaylandKeyboardEventSource(backend *realWaylandOverlayBackend) *realWaylandKeyboardEventSource {
	return &realWaylandKeyboardEventSource{
		backend: backend,
		queue:   newOverlayEventQueue[KeyboardEvent](),
	}
}

func (s *realWaylandKeyboardEventSource) NextKeyboardEvent(ctx context.Context) (KeyboardEvent, error) {
	if s == nil {
		return KeyboardEvent{}, io.ErrClosedPipe
	}
	return s.queue.pop(ctx, s.backend.dispatchOnce)
}

func (s *realWaylandKeyboardEventSource) Close() error {
	if s == nil {
		return nil
	}
	return s.queue.close()
}

func (s *realWaylandKeyboardEventSource) enqueue(event KeyboardEvent) {
	if s == nil {
		return
	}
	switch event.Kind {
	case KeyboardEventKeymap:
		size := int64(0)
		if event.Keymap != nil {
			size = event.Keymap.Size
		}
		s.backend.trace.Record(traceKeyboardKeymap, map[string]any{"size": size})
	case KeyboardEventEnter:
		s.backend.trace.Record(traceKeyboardEnter, nil)
	case KeyboardEventLeave:
		s.backend.trace.Record(traceKeyboardLeave, nil)
	case KeyboardEventDestroy:
		s.backend.trace.Record(traceKeyboardDestroy, nil)
	case KeyboardEventKey:
		s.backend.trace.Record(traceKeyboardKey, map[string]any{"key": event.Key, "state": event.State})
	case KeyboardEventModifiers:
		s.backend.trace.Record(traceKeyboardModifiers, map[string]any{"modifiers": event.Modifiers})
	case KeyboardEventRepeat:
		s.backend.trace.Record(traceKeyboardRepeat, map[string]any{"repeat_rate": event.RepeatRate, "repeat_delay": event.RepeatDelay.String()})
	}
	s.queue.push(event)
}

func (s *realWaylandKeyboardEventSource) handleKeymap(ev client.KeyboardKeymapEvent) {
	if ev.Fd < 0 {
		s.queue.fatal(fmt.Errorf("Wayland keymap event did not include a file descriptor"))
		return
	}
	file := io.NewSectionReader(newFDReader(ev.Fd), 0, int64(ev.Size))
	data := make([]byte, int(ev.Size))
	_, err := io.ReadFull(file, data)
	_ = unix.Close(ev.Fd)
	if err != nil {
		s.queue.fatal(fmt.Errorf("read Wayland keymap fd: %w", err))
		return
	}
	s.enqueue(KeyboardEvent{
		Kind: KeyboardEventKeymap,
		Keymap: &KeyboardKeymapFD{
			Data: data,
			Size: int64(len(data)),
		},
	})
}

type fdReader int

func newFDReader(fd int) fdReader {
	return fdReader(fd)
}

func (r fdReader) ReadAt(p []byte, off int64) (int, error) {
	n, err := unix.Pread(int(r), p, off)
	if err != nil {
		return n, err
	}
	if n == 0 && len(p) > 0 {
		return n, io.EOF
	}
	return n, nil
}

type realWaylandSHMBuffer struct {
	backend      *realWaylandOverlayBackend
	buffer       *client.Buffer
	data         []byte
	width        int
	height       int
	mu           sync.Mutex
	done         bool
	releasedFlag bool
}

func (b *realWaylandOverlayBackend) newSHMBuffer(ctx context.Context, snapshot ARGBSnapshot, surface *realLayerShellSurface) (*realWaylandSHMBuffer, error) {
	shm, err := b.shm()
	if err != nil {
		return nil, err
	}
	data := snapshot.PremultipliedForWaylandBytes()
	size := len(data)
	if size <= 0 {
		return nil, fmt.Errorf("cannot create zero-length Wayland shm buffer")
	}

	fd, err := unix.MemfdCreate("mousekeys-overlay", unix.MFD_CLOEXEC)
	if err != nil {
		return nil, fmt.Errorf("create anonymous shm file: %w", err)
	}
	closeFD := true
	defer func() {
		if closeFD {
			_ = unix.Close(fd)
		}
	}()
	if err := unix.Ftruncate(fd, int64(size)); err != nil {
		return nil, fmt.Errorf("resize anonymous shm file: %w", err)
	}
	mapped, err := unix.Mmap(fd, 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("mmap anonymous shm file: %w", err)
	}
	copy(mapped, data)

	shmBuffer := &realWaylandSHMBuffer{
		backend: b,
		data:    mapped,
		width:   snapshot.Width,
		height:  snapshot.Height,
	}
	err = b.withWayland(ctx, func() error {
		pool, err := shm.CreatePool(fd, int32(size))
		if err != nil {
			return fmt.Errorf("create wl_shm pool: %w", err)
		}
		buffer, err := pool.CreateBuffer(0, int32(snapshot.Width), int32(snapshot.Height), int32(snapshot.Width*4), uint32(client.ShmFormatArgb8888))
		if destroyErr := pool.Destroy(); destroyErr != nil {
			err = errors.Join(err, fmt.Errorf("destroy wl_shm pool: %w", destroyErr))
		}
		if err != nil {
			return fmt.Errorf("create wl_shm buffer: %w", err)
		}
		shmBuffer.buffer = buffer
		buffer.SetReleaseHandler(func(client.BufferReleaseEvent) {
			surface.handleBufferRelease(shmBuffer)
		})
		return nil
	})
	closeFD = false
	_ = unix.Close(fd)
	if err != nil {
		_ = unix.Munmap(mapped)
		return nil, err
	}
	return shmBuffer, nil
}

func (b *realWaylandSHMBuffer) markReleased() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.releasedFlag = true
}

func (b *realWaylandSHMBuffer) released() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.releasedFlag
}

func (b *realWaylandSHMBuffer) destroy(ctx context.Context) error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	if b.done {
		b.mu.Unlock()
		return nil
	}
	b.done = true
	buffer := b.buffer
	data := b.data
	b.buffer = nil
	b.data = nil
	b.mu.Unlock()

	var errs []error
	if buffer != nil {
		errs = append(errs, b.backend.withWayland(ctx, buffer.Destroy))
	}
	if data != nil {
		errs = append(errs, unix.Munmap(data))
	}
	return errors.Join(errs...)
}

func waylandIntegerBufferScale(scale float64) int {
	if scale <= 1 {
		return 1
	}
	rounded := int(math.Round(scale))
	if rounded < 1 {
		return 1
	}
	return rounded
}

func scaleARGBSnapshotNearest(snapshot ARGBSnapshot, scale int) ARGBSnapshot {
	if scale <= 1 {
		return snapshot
	}
	width := snapshot.Width * scale
	height := snapshot.Height * scale
	pixels := make([]ARGBPixel, width*height)
	for y := 0; y < height; y++ {
		srcY := y / scale
		for x := 0; x < width; x++ {
			pixels[y*width+x] = snapshot.Pixels[srcY*snapshot.Width+x/scale]
		}
	}
	return ARGBSnapshot{Width: width, Height: height, Pixels: pixels}
}

func waylandKeyState(state uint32) KeyState {
	if state == uint32(client.KeyboardKeyStateReleased) {
		return KeyReleased
	}
	return KeyPressed
}

func waylandModifiers(ev client.KeyboardModifiersEvent) ModifierState {
	active := ev.ModsDepressed | ev.ModsLatched | ev.ModsLocked
	return ModifierState{
		Shift: active&1 != 0,
		Ctrl:  active&4 != 0,
		Alt:   active&8 != 0,
		Super: active&64 != 0,
	}
}

func waylandKeyName(key uint32) string {
	if name, ok := waylandEvdevKeyNames[key]; ok {
		return name
	}
	return fmt.Sprintf("evdev:%d", key)
}

var waylandEvdevKeyNames = map[uint32]string{
	1:  "Escape",
	14: "BackSpace",
	30: "A",
	31: "S",
	32: "D",
	33: "F",
	34: "G",
	35: "H",
	36: "J",
	37: "K",
	38: "L",
	44: "Z",
	45: "X",
	46: "C",
	47: "V",
	48: "B",
	49: "N",
	50: "M",
	57: "space",
	16: "Q",
	17: "W",
	18: "E",
	19: "R",
	20: "T",
	21: "Y",
	22: "U",
	23: "I",
	24: "O",
	25: "P",
}

func proxyID(proxy client.Proxy) uint32 {
	if proxy == nil {
		return 0
	}
	return proxy.ID()
}
