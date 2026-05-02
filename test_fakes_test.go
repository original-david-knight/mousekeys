package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func fakeMonitorFixtures() []Monitor {
	return []Monitor{
		{Name: "DP-1", X: 0, Y: 0, Width: 1920, Height: 1080, Scale: 1.0},
		{Name: "eDP-1", X: 1920, Y: -120, Width: 1706, Height: 960, Scale: 1.25, Focused: true},
	}
}

func fakeFocusedMonitorFixture() Monitor {
	for _, monitor := range fakeMonitorFixtures() {
		if monitor.Focused {
			return monitor
		}
	}
	panic("fake monitor fixtures contain no focused monitor")
}

type fakeFocusedMonitorLookup struct {
	monitor Monitor
	err     error
	calls   int
}

func (f *fakeFocusedMonitorLookup) FocusedMonitor(context.Context) (Monitor, error) {
	f.calls++
	if f.err != nil {
		return Monitor{}, f.err
	}
	return f.monitor, nil
}

type fakeHyprlandIPCResponder struct {
	t          *testing.T
	socketPath string
	monitors   []Monitor
	listener   net.Listener
	mu         sync.Mutex
	requests   []string
}

func newFakeHyprlandIPCResponder(t *testing.T, monitors []Monitor) *fakeHyprlandIPCResponder {
	t.Helper()
	return newFakeHyprlandIPCResponderAtPath(t, filepath.Join(t.TempDir(), HyprlandCommandSocketName), monitors)
}

func newFakeHyprlandIPCResponderAtPath(t *testing.T, socketPath string, monitors []Monitor) *fakeHyprlandIPCResponder {
	t.Helper()
	return &fakeHyprlandIPCResponder{
		t:          t,
		socketPath: socketPath,
		monitors:   append([]Monitor(nil), monitors...),
	}
}

func (r *fakeHyprlandIPCResponder) Start() string {
	r.t.Helper()
	if err := os.MkdirAll(filepath.Dir(r.socketPath), 0o700); err != nil {
		r.t.Fatalf("create fake Hyprland IPC socket directory: %v", err)
	}
	listener, err := net.Listen("unix", r.socketPath)
	if err != nil {
		r.t.Fatalf("listen on fake Hyprland IPC socket: %v", err)
	}
	r.listener = listener
	r.t.Cleanup(func() {
		_ = listener.Close()
		_ = os.Remove(r.socketPath)
	})

	go r.acceptLoop()
	return r.socketPath
}

func (r *fakeHyprlandIPCResponder) acceptLoop() {
	for {
		conn, err := r.listener.Accept()
		if err != nil {
			return
		}
		go r.handle(conn)
	}
}

func (r *fakeHyprlandIPCResponder) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil && err != io.EOF {
		return
	}
	request := strings.TrimSpace(string(buf[:n]))

	r.mu.Lock()
	r.requests = append(r.requests, request)
	r.mu.Unlock()

	switch request {
	case "j/monitors", "monitors":
		_ = json.NewEncoder(conn).Encode(hyprlandMonitorFixtures(r.monitors))
	default:
		_, _ = fmt.Fprintf(conn, "unknown fake Hyprland IPC request: %s\n", request)
	}
}

func (r *fakeHyprlandIPCResponder) Requests() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.requests...)
}

func hyprlandMonitorFixtures(monitors []Monitor) []map[string]any {
	out := make([]map[string]any, 0, len(monitors))
	for _, monitor := range monitors {
		physicalWidth := monitor.Width
		physicalHeight := monitor.Height
		if monitor.Scale > 0 {
			physicalWidth = int(math.Round(float64(monitor.Width) * monitor.Scale))
			physicalHeight = int(math.Round(float64(monitor.Height) * monitor.Scale))
		}
		out = append(out, map[string]any{
			"name":      monitor.Name,
			"x":         monitor.X,
			"y":         monitor.Y,
			"width":     physicalWidth,
			"height":    physicalHeight,
			"scale":     monitor.Scale,
			"transform": 0,
			"focused":   monitor.Focused,
		})
	}
	return out
}

type fakeWaylandBackend struct {
	mu       sync.Mutex
	outputs  []Monitor
	events   []fakeWaylandEvent
	nextID   int
	observer fakeEventObserver
}

type fakeWaylandEvent struct {
	Kind                  string
	SurfaceID             string
	OutputName            string
	Width                 int
	Height                int
	Scale                 float64
	KeyboardInteractivity string
	BufferHash            string
}

func newFakeWaylandBackend(outputs ...Monitor) *fakeWaylandBackend {
	return &fakeWaylandBackend{outputs: append([]Monitor(nil), outputs...)}
}

func (f *fakeWaylandBackend) Outputs(context.Context) ([]Monitor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Monitor(nil), f.outputs...), nil
}

func (f *fakeWaylandBackend) CreateSurface(_ context.Context, monitor Monitor) (OverlaySurface, error) {
	f.mu.Lock()
	f.nextID++
	id := fmt.Sprintf("surface-%d", f.nextID)
	event := fakeWaylandEvent{
		Kind:       "surface_create",
		SurfaceID:  id,
		OutputName: monitor.Name,
		Width:      monitor.Width,
		Height:     monitor.Height,
		Scale:      monitor.Scale,
	}
	f.events = append(f.events, event)
	observer := f.observer
	f.mu.Unlock()
	observeFakeEvent(observer, "wayland", event.Kind, fakeWaylandEventFields(event))
	return &fakeOverlaySurface{backend: f, id: id, closed: make(chan struct{})}, nil
}

func (f *fakeWaylandBackend) Events() []fakeWaylandEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeWaylandEvent(nil), f.events...)
}

func (f *fakeWaylandBackend) Count(kind string) int {
	count := 0
	for _, event := range f.Events() {
		if event.Kind == kind {
			count++
		}
	}
	return count
}

func (f *fakeWaylandBackend) record(event fakeWaylandEvent) {
	f.mu.Lock()
	f.events = append(f.events, event)
	observer := f.observer
	f.mu.Unlock()
	observeFakeEvent(observer, "wayland", event.Kind, fakeWaylandEventFields(event))
}

type fakeOverlaySurface struct {
	backend      *fakeWaylandBackend
	id           string
	config       SurfaceConfig
	rerenderer   SurfaceRerenderFunc
	bufferActive bool
	closed       chan struct{}
	closeOnce    sync.Once
}

func (s *fakeOverlaySurface) ID() string {
	return s.id
}

func (s *fakeOverlaySurface) SetRerenderer(rerenderer SurfaceRerenderFunc) {
	s.rerenderer = rerenderer
}

func (s *fakeOverlaySurface) Configure(_ context.Context, config SurfaceConfig) error {
	s.config = config
	s.backend.record(fakeWaylandEvent{
		Kind:       "configure",
		SurfaceID:  s.id,
		OutputName: config.OutputName,
		Width:      config.Width,
		Height:     config.Height,
		Scale:      config.Scale,
	})
	return nil
}

func (s *fakeOverlaySurface) GrabKeyboard(context.Context) error {
	s.backend.record(fakeWaylandEvent{
		Kind:                  "keyboard_grab",
		SurfaceID:             s.id,
		KeyboardInteractivity: "exclusive",
	})
	return nil
}

func (s *fakeOverlaySurface) Render(_ context.Context, buffer ARGBBuffer) error {
	hash, err := ARGBHash(buffer)
	if err != nil {
		return err
	}
	if s.bufferActive {
		s.backend.record(fakeWaylandEvent{Kind: "buffer_destroy", SurfaceID: s.id})
	}
	s.bufferActive = true
	s.backend.record(fakeWaylandEvent{
		Kind:       "render",
		SurfaceID:  s.id,
		Width:      buffer.Width,
		Height:     buffer.Height,
		BufferHash: hash,
	})
	return nil
}

func (s *fakeOverlaySurface) Destroy(context.Context) error {
	if s.bufferActive {
		s.backend.record(fakeWaylandEvent{Kind: "buffer_destroy", SurfaceID: s.id})
		s.bufferActive = false
	}
	s.backend.record(fakeWaylandEvent{Kind: "destroy", SurfaceID: s.id})
	s.closeOnce.Do(func() {
		close(s.closed)
	})
	return nil
}

func (s *fakeOverlaySurface) Closed() <-chan struct{} {
	return s.closed
}

func (s *fakeOverlaySurface) SimulateCompositorClosed(ctx context.Context) error {
	s.backend.record(fakeWaylandEvent{Kind: "closed", SurfaceID: s.id})
	return s.Destroy(ctx)
}

func (s *fakeOverlaySurface) SimulateConfigure(ctx context.Context, width int, height int) error {
	config := s.config
	config.Width = width
	config.Height = height
	s.backend.record(fakeWaylandEvent{
		Kind:       "configure_event",
		SurfaceID:  s.id,
		OutputName: config.OutputName,
		Width:      width,
		Height:     height,
		Scale:      config.Scale,
	})
	if err := s.Configure(ctx, config); err != nil {
		return err
	}
	buffer, err := s.renderBufferForConfig(config)
	if err != nil {
		return err
	}
	return s.Render(ctx, buffer)
}

func (s *fakeOverlaySurface) SimulateScale(ctx context.Context, scale float64) error {
	config := s.config
	config.Scale = scale
	s.backend.record(fakeWaylandEvent{
		Kind:       "scale_event",
		SurfaceID:  s.id,
		OutputName: config.OutputName,
		Width:      config.Width,
		Height:     config.Height,
		Scale:      scale,
	})
	if err := s.Configure(ctx, config); err != nil {
		return err
	}
	buffer, err := s.renderBufferForConfig(config)
	if err != nil {
		return err
	}
	return s.Render(ctx, buffer)
}

func (s *fakeOverlaySurface) renderBufferForConfig(config SurfaceConfig) (ARGBBuffer, error) {
	if s.rerenderer != nil {
		return s.rerenderer(config)
	}
	buffer, err := NewARGBBuffer(config.Width, config.Height)
	if err != nil {
		return ARGBBuffer{}, err
	}
	RenderPlaceholderOverlay(buffer)
	return buffer, nil
}

type fakeRendererSink struct {
	mu            sync.Mutex
	presentations []fakeRenderPresentation
	observer      fakeEventObserver
}

type fakeRenderPresentation struct {
	SurfaceID string
	Width     int
	Height    int
	Hash      string
	Snapshot  []byte
}

func (f *fakeRendererSink) Present(_ context.Context, surfaceID string, buffer ARGBBuffer) error {
	snapshot, err := ARGBSnapshot(buffer)
	if err != nil {
		return err
	}
	hash, err := ARGBHash(buffer)
	if err != nil {
		return err
	}

	f.mu.Lock()
	f.presentations = append(f.presentations, fakeRenderPresentation{
		SurfaceID: surfaceID,
		Width:     buffer.Width,
		Height:    buffer.Height,
		Hash:      hash,
		Snapshot:  append([]byte(nil), snapshot...),
	})
	observer := f.observer
	f.mu.Unlock()
	observeFakeEvent(observer, "renderer", "present", map[string]any{
		"surface_id": surfaceID,
		"width":      buffer.Width,
		"height":     buffer.Height,
		"hash":       hash,
	})
	return nil
}

func (f *fakeRendererSink) Presentations() []fakeRenderPresentation {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeRenderPresentation(nil), f.presentations...)
}

type virtualPointerRecorder struct {
	mu          sync.Mutex
	clock       Clock
	mappingMode PointerMappingMode
	layout      Rect
	current     PointerMotion
	haveCurrent bool
	events      []recordedPointerEvent
	observer    fakeEventObserver
}

type recordedPointerEvent struct {
	Kind       string
	OutputName string
	X          int
	Y          int
	ProtocolX  uint32
	ProtocolY  uint32
	XExtent    uint32
	YExtent    uint32
	Mapping    PointerMappingMode
	Button     PointerButton
	State      ButtonState
	Time       time.Time
	GroupID    string
}

func newVirtualPointerRecorder(clock Clock) *virtualPointerRecorder {
	return &virtualPointerRecorder{
		clock:       clock,
		mappingMode: PointerMappingWithOutput,
	}
}

func newFallbackVirtualPointerRecorder(clock Clock, layout Rect) *virtualPointerRecorder {
	return &virtualPointerRecorder{
		clock:       clock,
		mappingMode: PointerMappingFallback,
		layout:      layout,
	}
}

func (r *virtualPointerRecorder) MoveAbsolute(_ context.Context, x int, y int, output Monitor) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	clock := r.clock
	if clock == nil {
		clock = systemClock{}
	}
	mode := r.mappingMode
	if mode == "" {
		mode = PointerMappingWithOutput
	}
	motion, err := pointerMotionFromLogical(clock, output, x, y, mode, r.layout)
	if err != nil {
		return err
	}
	r.current = motion
	r.haveCurrent = true
	r.record(recordedPointerEvent{
		Kind:       "motion",
		OutputName: motion.OutputName,
		X:          motion.X,
		Y:          motion.Y,
		ProtocolX:  motion.ProtocolX,
		ProtocolY:  motion.ProtocolY,
		XExtent:    motion.XExtent,
		YExtent:    motion.YExtent,
		Mapping:    motion.Mapping,
		Time:       motion.Time,
	})
	r.record(recordedPointerEvent{
		Kind:       "frame",
		OutputName: motion.OutputName,
		X:          motion.X,
		Y:          motion.Y,
		ProtocolX:  motion.ProtocolX,
		ProtocolY:  motion.ProtocolY,
		XExtent:    motion.XExtent,
		YExtent:    motion.YExtent,
		Mapping:    motion.Mapping,
		Time:       motion.Time,
	})
	return nil
}

func (r *virtualPointerRecorder) LeftClick(context.Context) error {
	return r.click(PointerButtonLeft)
}

func (r *virtualPointerRecorder) RightClick(context.Context) error {
	return r.click(PointerButtonRight)
}

func (r *virtualPointerRecorder) DoubleClick(ctx context.Context) error {
	if err := r.LeftClick(ctx); err != nil {
		return err
	}
	return r.LeftClick(ctx)
}

func (r *virtualPointerRecorder) click(button PointerButton) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.haveCurrent {
		return fmt.Errorf("cannot click before MoveAbsolute")
	}
	clock := r.clock
	if clock == nil {
		clock = systemClock{}
	}
	event := PointerButtonEvent{
		OutputName: r.current.OutputName,
		X:          r.current.X,
		Y:          r.current.Y,
		Button:     button,
		Time:       clock.Now(),
	}
	r.record(recordedPointerEvent{
		Kind:       "button",
		OutputName: event.OutputName,
		X:          event.X,
		Y:          event.Y,
		Button:     event.Button,
		State:      ButtonDown,
		Time:       event.Time,
	})
	r.record(recordedPointerEvent{
		Kind:       "button",
		OutputName: event.OutputName,
		X:          event.X,
		Y:          event.Y,
		Button:     event.Button,
		State:      ButtonUp,
		Time:       event.Time,
	})
	r.record(recordedPointerEvent{
		Kind:       "frame",
		OutputName: event.OutputName,
		X:          event.X,
		Y:          event.Y,
		Time:       event.Time,
	})
	return nil
}

func (r *virtualPointerRecorder) Events() []recordedPointerEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedPointerEvent(nil), r.events...)
}

func (r *virtualPointerRecorder) ClickCount(groupID string, button PointerButton) int {
	downs := 0
	ups := 0
	for _, event := range r.Events() {
		if event.Kind != "button" || event.GroupID != groupID || event.Button != button {
			continue
		}
		switch event.State {
		case ButtonDown:
			downs++
		case ButtonUp:
			ups++
		}
	}
	return min(downs, ups)
}

func (r *virtualPointerRecorder) record(event recordedPointerEvent) {
	r.events = append(r.events, event)
	observeFakeEvent(r.observer, "pointer", event.Kind, recordedPointerEventFields(event))
}

type fakeKeyboardEventSource struct {
	ch       chan KeyboardEvent
	observer fakeEventObserver
}

func newFakeKeyboardEventSource(buffer int) *fakeKeyboardEventSource {
	return &fakeKeyboardEventSource{ch: make(chan KeyboardEvent, buffer)}
}

func (f *fakeKeyboardEventSource) Events(context.Context) (<-chan KeyboardEvent, error) {
	return f.ch, nil
}

func (f *fakeKeyboardEventSource) Send(event KeyboardEvent) {
	observeFakeEvent(f.observer, "keyboard", "send", map[string]any{
		"kind":    string(event.Kind),
		"key":     event.Key,
		"pressed": event.Pressed,
		"repeat":  event.Repeat,
		"time":    event.Time.UTC().Format(time.RFC3339Nano),
	})
	f.ch <- event
}

func (f *fakeKeyboardEventSource) Close() {
	close(f.ch)
}

type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers map[*fakeTimer]struct{}
}

func newFakeClock(now time.Time) *fakeClock {
	return &fakeClock{
		now:    now,
		timers: make(map[*fakeTimer]struct{}),
	}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &fakeTimer{
		clock:  c,
		ch:     make(chan time.Time, 1),
		due:    c.now.Add(d),
		active: true,
	}
	c.timers[timer] = struct{}{}
	return timer
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	var due []*fakeTimer
	for timer := range c.timers {
		if timer.active && !timer.due.After(now) {
			timer.active = false
			delete(c.timers, timer)
			due = append(due, timer)
		}
	}
	c.mu.Unlock()

	for _, timer := range due {
		timer.fire(now)
	}
}

type fakeTimer struct {
	clock  *fakeClock
	ch     chan time.Time
	due    time.Time
	active bool
}

func (t *fakeTimer) C() <-chan time.Time {
	return t.ch
}

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	wasActive := t.active
	t.active = false
	delete(t.clock.timers, t)
	return wasActive
}

func (t *fakeTimer) Reset(d time.Duration) bool {
	t.clock.mu.Lock()
	wasActive := t.active
	t.active = true
	t.due = t.clock.now.Add(d)
	t.clock.timers[t] = struct{}{}
	t.clock.mu.Unlock()

	select {
	case <-t.ch:
	default:
	}
	return wasActive
}

func (t *fakeTimer) fire(now time.Time) {
	select {
	case t.ch <- now:
	default:
	}
}
