package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"mousekeys/internal/wayland/wlr"

	"github.com/rajveermalviya/go-wayland/wayland/client"
)

const (
	linuxInputButtonLeft  = 0x110
	linuxInputButtonRight = 0x111
)

type virtualPointerBindingMode string

const (
	virtualPointerBindingFallback   virtualPointerBindingMode = "manager_v1_fallback"
	virtualPointerBindingWithOutput virtualPointerBindingMode = "manager_v2_with_output"
)

type virtualPointerDevice interface {
	MotionAbsolute(time, x, y, xExtent, yExtent uint32) error
	Button(time, button, state uint32) error
	Frame() error
	Destroy() error
}

type virtualPointerManagerClient interface {
	CreateVirtualPointer(seat *client.Seat) (virtualPointerDevice, error)
	CreateVirtualPointerWithOutput(seat *client.Seat, output *client.Output) (virtualPointerDevice, error)
}

type wlrVirtualPointerManagerClient struct {
	manager *wlr.VirtualPointerManager
}

func (m wlrVirtualPointerManagerClient) CreateVirtualPointer(seat *client.Seat) (virtualPointerDevice, error) {
	if m.manager == nil {
		return nil, fmt.Errorf("wlr virtual pointer manager is nil")
	}
	return m.manager.CreateVirtualPointer(seat)
}

func (m wlrVirtualPointerManagerClient) CreateVirtualPointerWithOutput(seat *client.Seat, output *client.Output) (virtualPointerDevice, error) {
	if m.manager == nil {
		return nil, fmt.Errorf("wlr virtual pointer manager is nil")
	}
	return m.manager.CreateVirtualPointerWithOutput(seat, output)
}

type virtualPointerLayout struct {
	X      int
	Y      int
	Width  int
	Height int
}

type virtualPointerBinding struct {
	Device virtualPointerDevice
	Mode   virtualPointerBindingMode
	Output WaylandOutputInfo
	Layout virtualPointerLayout
}

type virtualPointerFactory interface {
	CreateVirtualPointer(ctx context.Context, output Monitor) (virtualPointerBinding, error)
}

type virtualPointerSynthesizer struct {
	mu      sync.Mutex
	factory virtualPointerFactory
	trace   *TraceRecorder
	now     func() time.Time

	binding    virtualPointerBinding
	hasBinding bool

	lastPosition PointerPosition
	hasPosition  bool
	clickGroup   int
}

func newVirtualPointerSynthesizer(factory virtualPointerFactory, trace *TraceRecorder, now func() time.Time) (*virtualPointerSynthesizer, error) {
	if factory == nil {
		return nil, fmt.Errorf("virtual pointer factory is required")
	}
	if now == nil {
		now = time.Now
	}
	return &virtualPointerSynthesizer{
		factory: factory,
		trace:   trace,
		now:     now,
	}, nil
}

func newProductionPointerSynthesizer(getenv getenvFunc, trace *TraceRecorder) (PointerActionSynthesizer, error) {
	return newVirtualPointerSynthesizer(&lazyWaylandVirtualPointerFactory{
		getenv: getenv,
	}, trace, time.Now)
}

func (s *virtualPointerSynthesizer) MoveAbsolute(ctx context.Context, x, y float64, output Monitor) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := output.Validate(); err != nil {
		return err
	}
	if !isFinitePointerCoordinate(x) || !isFinitePointerCoordinate(y) {
		return fmt.Errorf("pointer absolute target for output %q must be finite, got %v,%v", output.Name, x, y)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	binding, err := s.ensureBindingLocked(ctx, output)
	if err != nil {
		return err
	}
	request, err := absoluteMotionRequest(x, y, binding)
	if err != nil {
		return err
	}
	at := s.now()
	ms := pointerEventTime(at)
	if err := binding.Device.MotionAbsolute(ms, request.X, request.Y, request.XExtent, request.YExtent); err != nil {
		return fmt.Errorf("send virtual pointer absolute motion: %w", err)
	}
	if err := binding.Device.Frame(); err != nil {
		return fmt.Errorf("send virtual pointer motion frame: %w", err)
	}

	position := PointerPosition{X: x, Y: y, OutputName: binding.Output.Name}
	s.lastPosition = position
	s.hasPosition = true
	s.trace.Record(tracePointerMotion, map[string]any{
		"position": position,
		"at":       at,
		"protocol": map[string]any{
			"mode":     binding.Mode,
			"x":        request.X,
			"y":        request.Y,
			"x_extent": request.XExtent,
			"y_extent": request.YExtent,
		},
	})
	s.trace.Record(tracePointerFrame, map[string]any{"output_name": binding.Output.Name, "at": at})
	return nil
}

func (s *virtualPointerSynthesizer) LeftClick(ctx context.Context) error {
	return s.click(ctx, PointerButtonLeft, 1)
}

func (s *virtualPointerSynthesizer) RightClick(ctx context.Context) error {
	return s.click(ctx, PointerButtonRight, 1)
}

func (s *virtualPointerSynthesizer) DoubleClick(ctx context.Context) error {
	return s.click(ctx, PointerButtonLeft, 2)
}

func (s *virtualPointerSynthesizer) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	binding := s.binding
	hasBinding := s.hasBinding
	s.binding = virtualPointerBinding{}
	s.hasBinding = false
	s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
	}
	var err error
	if hasBinding && binding.Device != nil {
		err = binding.Device.Destroy()
	}
	if closer, ok := s.factory.(interface{ Close(context.Context) error }); ok {
		err = errors.Join(err, closer.Close(ctx))
	}
	return err
}

func (s *virtualPointerSynthesizer) ensureBindingLocked(ctx context.Context, output Monitor) (virtualPointerBinding, error) {
	if s.hasBinding && bindingMatchesMonitor(s.binding, output) {
		return s.binding, nil
	}
	if s.hasBinding && s.binding.Device != nil {
		if err := s.binding.Device.Destroy(); err != nil {
			return virtualPointerBinding{}, fmt.Errorf("destroy stale virtual pointer: %w", err)
		}
		s.binding = virtualPointerBinding{}
		s.hasBinding = false
	}
	binding, err := s.factory.CreateVirtualPointer(ctx, output)
	if err != nil {
		return virtualPointerBinding{}, err
	}
	if binding.Device == nil {
		return virtualPointerBinding{}, fmt.Errorf("virtual pointer factory returned nil device")
	}
	if binding.Output.Name == "" {
		binding.Output.Name = output.Name
	}
	s.binding = binding
	s.hasBinding = true
	return binding, nil
}

func (s *virtualPointerSynthesizer) click(ctx context.Context, button PointerButton, count int) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if count <= 0 {
		return fmt.Errorf("click count must be positive, got %d", count)
	}
	code, err := pointerButtonCode(button)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasBinding || s.binding.Device == nil {
		return fmt.Errorf("virtual pointer click requires a pointer created by MoveAbsolute")
	}
	if !s.hasPosition {
		return fmt.Errorf("virtual pointer click requires a prior MoveAbsolute target")
	}

	s.clickGroup++
	clickGroup := s.clickGroup
	position := s.lastPosition
	s.trace.Record(traceClickGroupStart, map[string]any{"click_group": clickGroup, "click_count": count, "position": position})
	for sequence := 1; sequence <= count; sequence++ {
		if err := s.emitButtonLocked(ctx, code, button, PointerButtonDown, position, clickGroup, count, sequence); err != nil {
			return err
		}
		if err := s.emitButtonLocked(ctx, code, button, PointerButtonUp, position, clickGroup, count, sequence); err != nil {
			return err
		}
		at := s.now()
		if err := s.binding.Device.Frame(); err != nil {
			return fmt.Errorf("send virtual pointer button frame: %w", err)
		}
		s.trace.Record(tracePointerFrame, map[string]any{"output_name": position.OutputName, "at": at})
	}
	s.trace.Record(traceClickGroupComplete, map[string]any{"click_group": clickGroup, "click_count": count})
	return nil
}

func (s *virtualPointerSynthesizer) emitButtonLocked(ctx context.Context, code uint32, button PointerButton, state PointerButtonState, position PointerPosition, clickGroup, clickCount, sequence int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	at := s.now()
	protocolState := uint32(client.PointerButtonStateReleased)
	if state == PointerButtonDown {
		protocolState = uint32(client.PointerButtonStatePressed)
	}
	if err := s.binding.Device.Button(pointerEventTime(at), code, protocolState); err != nil {
		return fmt.Errorf("send virtual pointer button %s/%s: %w", button, state, err)
	}
	s.trace.Record(tracePointerButton, map[string]any{
		"button":      button,
		"state":       state,
		"position":    position,
		"click_group": clickGroup,
		"click_count": clickCount,
		"sequence":    sequence,
		"at":          at,
	})
	return nil
}

type realVirtualPointerFactory struct {
	mu   sync.Mutex
	base *WaylandClientBase
}

func newRealVirtualPointerFactory(base *WaylandClientBase) (*realVirtualPointerFactory, error) {
	if base == nil {
		return nil, fmt.Errorf("Wayland client base is nil")
	}
	return &realVirtualPointerFactory{base: base}, nil
}

func (f *realVirtualPointerFactory) CreateVirtualPointer(ctx context.Context, output Monitor) (virtualPointerBinding, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return virtualPointerBinding{}, err
	}
	if err := output.Validate(); err != nil {
		return virtualPointerBinding{}, err
	}
	if f == nil || f.base == nil {
		return virtualPointerBinding{}, fmt.Errorf("Wayland client base is nil")
	}

	outputInfo, err := f.base.OutputForMonitor(output)
	if err != nil {
		return virtualPointerBinding{}, err
	}
	layout, err := virtualPointerLayoutFromOutputs(f.base.Outputs())
	if err != nil {
		return virtualPointerBinding{}, err
	}

	f.base.mu.RLock()
	manager := f.base.virtualPointerManager
	version := f.base.virtualPointerVersion
	seat := f.base.seat
	var outputProxy *client.Output
	if state := f.base.outputs[outputInfo.GlobalName]; state != nil {
		outputProxy = state.proxy
	}
	f.base.mu.RUnlock()

	if manager == nil {
		return virtualPointerBinding{}, fmt.Errorf("wlr virtual pointer manager is not bound")
	}
	if seat == nil {
		return virtualPointerBinding{}, fmt.Errorf("Wayland seat is not bound")
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return virtualPointerBinding{}, err
	}
	device, mode, err := createVirtualPointerForManagerVersion(wlrVirtualPointerManagerClient{manager: manager}, version, seat, outputProxy)
	if err != nil {
		return virtualPointerBinding{}, err
	}
	return virtualPointerBinding{
		Device: device,
		Mode:   mode,
		Output: outputInfo,
		Layout: layout,
	}, nil
}

type lazyWaylandVirtualPointerFactory struct {
	mu      sync.Mutex
	getenv  getenvFunc
	base    *WaylandClientBase
	factory *realVirtualPointerFactory
}

func (f *lazyWaylandVirtualPointerFactory) CreateVirtualPointer(ctx context.Context, output Monitor) (virtualPointerBinding, error) {
	factory, err := f.ensure(ctx)
	if err != nil {
		return virtualPointerBinding{}, err
	}
	return factory.CreateVirtualPointer(ctx, output)
}

func (f *lazyWaylandVirtualPointerFactory) Close(ctx context.Context) error {
	f.mu.Lock()
	base := f.base
	f.base = nil
	f.factory = nil
	f.mu.Unlock()
	if base == nil {
		return nil
	}
	return base.Close(ctx)
}

func (f *lazyWaylandVirtualPointerFactory) ensure(ctx context.Context) (*realVirtualPointerFactory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.factory != nil {
		return f.factory, nil
	}
	base, err := OpenWaylandClientBase(ctx, f.getenv)
	if err != nil {
		return nil, err
	}
	factory, err := newRealVirtualPointerFactory(base)
	if err != nil {
		_ = base.Close(context.Background())
		return nil, err
	}
	f.base = base
	f.factory = factory
	return factory, nil
}

func createVirtualPointerForManagerVersion(manager virtualPointerManagerClient, version uint32, seat *client.Seat, output *client.Output) (virtualPointerDevice, virtualPointerBindingMode, error) {
	if manager == nil {
		return nil, "", fmt.Errorf("wlr virtual pointer manager is nil")
	}
	if version >= 2 {
		if output == nil {
			return nil, "", fmt.Errorf("wlr virtual pointer manager v%d supports output mapping, but focused wl_output proxy is nil", version)
		}
		device, err := manager.CreateVirtualPointerWithOutput(seat, output)
		if err != nil {
			return nil, "", fmt.Errorf("create wlr virtual pointer with output: %w", err)
		}
		return device, virtualPointerBindingWithOutput, nil
	}
	device, err := manager.CreateVirtualPointer(seat)
	if err != nil {
		return nil, "", fmt.Errorf("create wlr virtual pointer fallback: %w", err)
	}
	return device, virtualPointerBindingFallback, nil
}

type absoluteMotionProtocolRequest struct {
	X       uint32
	Y       uint32
	XExtent uint32
	YExtent uint32
}

func absoluteMotionRequest(x, y float64, binding virtualPointerBinding) (absoluteMotionProtocolRequest, error) {
	if binding.Output.LogicalWidth <= 0 || binding.Output.LogicalHeight <= 0 {
		return absoluteMotionProtocolRequest{}, fmt.Errorf("Wayland output %q has invalid logical size %dx%d", binding.Output.Name, binding.Output.LogicalWidth, binding.Output.LogicalHeight)
	}
	if x < 0 || x > float64(binding.Output.LogicalWidth) || y < 0 || y > float64(binding.Output.LogicalHeight) {
		return absoluteMotionProtocolRequest{}, fmt.Errorf("pointer target %.3f,%.3f is outside output %q logical bounds %dx%d", x, y, binding.Output.Name, binding.Output.LogicalWidth, binding.Output.LogicalHeight)
	}

	switch binding.Mode {
	case virtualPointerBindingWithOutput:
		reqX, err := roundedUintWithinExtent(x, binding.Output.LogicalWidth)
		if err != nil {
			return absoluteMotionProtocolRequest{}, err
		}
		reqY, err := roundedUintWithinExtent(y, binding.Output.LogicalHeight)
		if err != nil {
			return absoluteMotionProtocolRequest{}, err
		}
		return absoluteMotionProtocolRequest{
			X:       reqX,
			Y:       reqY,
			XExtent: uint32(binding.Output.LogicalWidth),
			YExtent: uint32(binding.Output.LogicalHeight),
		}, nil
	case virtualPointerBindingFallback:
		if binding.Layout.Width <= 0 || binding.Layout.Height <= 0 {
			return absoluteMotionProtocolRequest{}, fmt.Errorf("invalid Wayland output layout extents %dx%d", binding.Layout.Width, binding.Layout.Height)
		}
		reqX, err := roundedUintWithinExtent(float64(binding.Output.LogicalX-binding.Layout.X)+x, binding.Layout.Width)
		if err != nil {
			return absoluteMotionProtocolRequest{}, err
		}
		reqY, err := roundedUintWithinExtent(float64(binding.Output.LogicalY-binding.Layout.Y)+y, binding.Layout.Height)
		if err != nil {
			return absoluteMotionProtocolRequest{}, err
		}
		return absoluteMotionProtocolRequest{
			X:       reqX,
			Y:       reqY,
			XExtent: uint32(binding.Layout.Width),
			YExtent: uint32(binding.Layout.Height),
		}, nil
	default:
		return absoluteMotionProtocolRequest{}, fmt.Errorf("unknown virtual pointer binding mode %q", binding.Mode)
	}
}

func virtualPointerLayoutFromOutputs(outputs []WaylandOutputInfo) (virtualPointerLayout, error) {
	if len(outputs) == 0 {
		return virtualPointerLayout{}, fmt.Errorf("no Wayland outputs available for virtual pointer fallback layout")
	}
	var layout virtualPointerLayout
	for i, output := range outputs {
		if output.LogicalWidth <= 0 || output.LogicalHeight <= 0 {
			return virtualPointerLayout{}, fmt.Errorf("Wayland output %q has invalid logical size %dx%d", output.Name, output.LogicalWidth, output.LogicalHeight)
		}
		minX, minY := output.LogicalX, output.LogicalY
		maxX, maxY := output.LogicalX+output.LogicalWidth, output.LogicalY+output.LogicalHeight
		if i == 0 {
			layout = virtualPointerLayout{X: minX, Y: minY, Width: output.LogicalWidth, Height: output.LogicalHeight}
			continue
		}
		currentMaxX := layout.X + layout.Width
		currentMaxY := layout.Y + layout.Height
		layout.X = minInt(layout.X, minX)
		layout.Y = minInt(layout.Y, minY)
		layout.Width = maxInt(currentMaxX, maxX) - layout.X
		layout.Height = maxInt(currentMaxY, maxY) - layout.Y
	}
	if layout.Width <= 0 || layout.Height <= 0 {
		return virtualPointerLayout{}, fmt.Errorf("invalid Wayland output layout extents %dx%d", layout.Width, layout.Height)
	}
	return layout, nil
}

func bindingMatchesMonitor(binding virtualPointerBinding, output Monitor) bool {
	return binding.Output.Name == output.Name &&
		binding.Output.LogicalX == output.OriginX &&
		binding.Output.LogicalY == output.OriginY &&
		binding.Output.LogicalWidth == output.LogicalWidth &&
		binding.Output.LogicalHeight == output.LogicalHeight
}

func pointerButtonCode(button PointerButton) (uint32, error) {
	switch button {
	case PointerButtonLeft:
		return linuxInputButtonLeft, nil
	case PointerButtonRight:
		return linuxInputButtonRight, nil
	default:
		return 0, fmt.Errorf("unsupported pointer button %q", button)
	}
}

func roundedUintWithinExtent(value float64, extent int) (uint32, error) {
	if extent <= 0 {
		return 0, fmt.Errorf("invalid absolute pointer extent %d", extent)
	}
	rounded := math.Round(value)
	if rounded < 0 || rounded > float64(extent) {
		return 0, fmt.Errorf("absolute pointer coordinate %.3f rounds to %.0f outside extent %d", value, rounded, extent)
	}
	return uint32(rounded), nil
}

func pointerEventTime(t time.Time) uint32 {
	if t.IsZero() {
		return 0
	}
	return uint32(t.UnixNano() / int64(time.Millisecond))
}

func isFinitePointerCoordinate(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}
