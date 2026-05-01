package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	return &fakeHyprlandIPCResponder{
		t:          t,
		socketPath: filepath.Join(t.TempDir(), ".socket.sock"),
		monitors:   append([]Monitor(nil), monitors...),
	}
}

func (r *fakeHyprlandIPCResponder) Start() string {
	r.t.Helper()
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
		out = append(out, map[string]any{
			"name":    monitor.Name,
			"x":       monitor.X,
			"y":       monitor.Y,
			"width":   monitor.Width,
			"height":  monitor.Height,
			"scale":   monitor.Scale,
			"focused": monitor.Focused,
		})
	}
	return out
}

type fakeWaylandBackend struct {
	mu      sync.Mutex
	outputs []Monitor
	events  []fakeWaylandEvent
	nextID  int
}

type fakeWaylandEvent struct {
	Kind       string
	SurfaceID  string
	OutputName string
	Width      int
	Height     int
	Scale      float64
	BufferHash string
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
	defer f.mu.Unlock()
	f.nextID++
	id := fmt.Sprintf("surface-%d", f.nextID)
	f.events = append(f.events, fakeWaylandEvent{
		Kind:       "surface_create",
		SurfaceID:  id,
		OutputName: monitor.Name,
		Width:      monitor.Width,
		Height:     monitor.Height,
		Scale:      monitor.Scale,
	})
	return &fakeOverlaySurface{backend: f, id: id}, nil
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
	defer f.mu.Unlock()
	f.events = append(f.events, event)
}

type fakeOverlaySurface struct {
	backend *fakeWaylandBackend
	id      string
}

func (s *fakeOverlaySurface) ID() string {
	return s.id
}

func (s *fakeOverlaySurface) Configure(_ context.Context, config SurfaceConfig) error {
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
	s.backend.record(fakeWaylandEvent{Kind: "keyboard_grab", SurfaceID: s.id})
	return nil
}

func (s *fakeOverlaySurface) Render(_ context.Context, buffer ARGBBuffer) error {
	hash, err := ARGBHash(buffer)
	if err != nil {
		return err
	}
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
	s.backend.record(fakeWaylandEvent{Kind: "destroy", SurfaceID: s.id})
	return nil
}

type fakeRendererSink struct {
	mu            sync.Mutex
	presentations []fakeRenderPresentation
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
	defer f.mu.Unlock()
	f.presentations = append(f.presentations, fakeRenderPresentation{
		SurfaceID: surfaceID,
		Width:     buffer.Width,
		Height:    buffer.Height,
		Hash:      hash,
		Snapshot:  append([]byte(nil), snapshot...),
	})
	return nil
}

func (f *fakeRendererSink) Presentations() []fakeRenderPresentation {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeRenderPresentation(nil), f.presentations...)
}

type virtualPointerRecorder struct {
	mu     sync.Mutex
	events []recordedPointerEvent
}

type recordedPointerEvent struct {
	Kind       string
	OutputName string
	X          int
	Y          int
	Button     PointerButton
	State      ButtonState
	Time       time.Time
	GroupID    string
}

func (r *virtualPointerRecorder) Motion(_ context.Context, event PointerMotion) error {
	r.record(recordedPointerEvent{
		Kind:       "motion",
		OutputName: event.OutputName,
		X:          event.X,
		Y:          event.Y,
		Time:       event.Time,
		GroupID:    event.GroupID,
	})
	return nil
}

func (r *virtualPointerRecorder) Button(_ context.Context, event PointerButtonEvent) error {
	r.record(recordedPointerEvent{
		Kind:       "button",
		OutputName: event.OutputName,
		X:          event.X,
		Y:          event.Y,
		Button:     event.Button,
		State:      event.State,
		Time:       event.Time,
		GroupID:    event.GroupID,
	})
	return nil
}

func (r *virtualPointerRecorder) Frame(_ context.Context, event PointerFrame) error {
	r.record(recordedPointerEvent{
		Kind:       "frame",
		OutputName: event.OutputName,
		X:          event.X,
		Y:          event.Y,
		Time:       event.Time,
		GroupID:    event.GroupID,
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
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

type fakeKeyboardEventSource struct {
	ch chan KeyboardEvent
}

func newFakeKeyboardEventSource(buffer int) *fakeKeyboardEventSource {
	return &fakeKeyboardEventSource{ch: make(chan KeyboardEvent, buffer)}
}

func (f *fakeKeyboardEventSource) Events(context.Context) (<-chan KeyboardEvent, error) {
	return f.ch, nil
}

func (f *fakeKeyboardEventSource) Send(event KeyboardEvent) {
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
