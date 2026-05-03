package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

func focusedMonitorFixtures() []Monitor {
	return []Monitor{
		{
			Name:          "DP-1",
			OriginX:       1920,
			OriginY:       120,
			LogicalWidth:  2560,
			LogicalHeight: 1440,
			Scale:         1.25,
		},
		{
			Name:          "HDMI-A-1",
			OriginX:       -1280,
			OriginY:       -360,
			LogicalWidth:  1280,
			LogicalHeight: 720,
			Scale:         1.0,
		},
		{
			Name:          "eDP-1",
			OriginX:       320,
			OriginY:       0,
			LogicalWidth:  1600,
			LogicalHeight: 1000,
			Scale:         1.5,
		},
	}
}

type fakeHyprlandIPC struct {
	mu       sync.Mutex
	fixtures []Monitor
	next     int
	err      error
}

func newFakeHyprlandIPC(fixtures ...Monitor) *fakeHyprlandIPC {
	return &fakeHyprlandIPC{fixtures: fixtures}
}

func (f *fakeHyprlandIPC) FocusedMonitor(ctx context.Context) (Monitor, error) {
	if err := ctx.Err(); err != nil {
		return Monitor{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return Monitor{}, f.err
	}
	if len(f.fixtures) == 0 {
		return Monitor{}, errors.New("fake Hyprland IPC has no focused monitor fixture")
	}
	monitor := f.fixtures[f.next%len(f.fixtures)]
	f.next++
	return monitor, monitor.Validate()
}

type fakeWaylandEvent struct {
	Kind       string
	SurfaceID  int
	Monitor    Monitor
	Width      int
	Height     int
	Scale      float64
	BufferHash string
	OutputName string
}

type fakeWaylandBackend struct {
	mu       sync.Mutex
	trace    *TraceRecorder
	events   []fakeWaylandEvent
	nextID   int
	keyboard *fakeKeyboardEventSource
}

func newFakeWaylandBackend(trace *TraceRecorder) *fakeWaylandBackend {
	return &fakeWaylandBackend{
		trace:    trace,
		keyboard: newFakeKeyboardEventSource(trace),
	}
}

func (f *fakeWaylandBackend) CreateSurface(ctx context.Context, monitor Monitor) (OverlaySurface, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := monitor.Validate(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.nextID++
	id := f.nextID
	f.events = append(f.events, fakeWaylandEvent{Kind: "surface_create", SurfaceID: id, Monitor: monitor, OutputName: monitor.Name})
	f.mu.Unlock()
	f.trace.Record(traceOverlaySurfaceCreate, map[string]any{"surface_id": id, "monitor": monitor})
	return &fakeOverlaySurface{backend: f, id: id, monitor: monitor}, nil
}

func (f *fakeWaylandBackend) OutputChanged(ctx context.Context, monitor Monitor) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.record(fakeWaylandEvent{Kind: "output_change", Monitor: monitor, OutputName: monitor.Name})
	f.trace.Record(traceOverlayOutputChange, map[string]any{"monitor": monitor})
	return nil
}

func (f *fakeWaylandBackend) Close(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.record(fakeWaylandEvent{Kind: "compositor_close"})
	f.trace.Record(traceOverlayClose, nil)
	return nil
}

func (f *fakeWaylandBackend) record(event fakeWaylandEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, event)
}

func (f *fakeWaylandBackend) Events() []fakeWaylandEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeWaylandEvent, len(f.events))
	copy(out, f.events)
	return out
}

type fakeOverlaySurface struct {
	backend *fakeWaylandBackend
	id      int
	monitor Monitor
}

func (s *fakeOverlaySurface) Configure(ctx context.Context, width, height int, scale float64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.backend.record(fakeWaylandEvent{
		Kind:      "configure",
		SurfaceID: s.id,
		Monitor:   s.monitor,
		Width:     width,
		Height:    height,
		Scale:     scale,
	})
	s.backend.trace.Record(traceOverlayConfigure, map[string]any{
		"surface_id": s.id,
		"width":      width,
		"height":     height,
		"scale":      scale,
	})
	return nil
}

func (s *fakeOverlaySurface) Render(ctx context.Context, buffer ARGBSnapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	hash := buffer.StraightHash()
	s.backend.record(fakeWaylandEvent{
		Kind:       "render",
		SurfaceID:  s.id,
		Monitor:    s.monitor,
		Width:      buffer.Width,
		Height:     buffer.Height,
		BufferHash: hash,
	})
	s.backend.trace.Record(traceOverlayRender, map[string]any{
		"surface_id": s.id,
		"width":      buffer.Width,
		"height":     buffer.Height,
		"hash":       hash,
	})
	return nil
}

func (s *fakeOverlaySurface) GrabKeyboard(ctx context.Context) (KeyboardEventSource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.backend.keyboard.BeginShow()
	s.backend.record(fakeWaylandEvent{Kind: "keyboard_grab", SurfaceID: s.id, Monitor: s.monitor})
	s.backend.trace.Record(traceOverlayKeyboardGrab, map[string]any{
		"surface_id": s.id,
		"show_count": s.backend.keyboard.ShowCount(),
	})
	return s.backend.keyboard, nil
}

func (s *fakeOverlaySurface) Unmap(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.backend.record(fakeWaylandEvent{Kind: "unmap", SurfaceID: s.id, Monitor: s.monitor})
	s.backend.trace.Record(traceOverlayUnmap, map[string]any{"surface_id": s.id})
	return nil
}

func (s *fakeOverlaySurface) Destroy(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.backend.record(fakeWaylandEvent{Kind: "destroy", SurfaceID: s.id, Monitor: s.monitor})
	s.backend.trace.Record(traceOverlayDestroy, map[string]any{"surface_id": s.id})
	return nil
}

type fakeKeyboardEventSource struct {
	mu        sync.Mutex
	trace     *TraceRecorder
	events    []KeyboardEvent
	showCount int
	closed    bool
}

func newFakeKeyboardEventSource(trace *TraceRecorder) *fakeKeyboardEventSource {
	return &fakeKeyboardEventSource{trace: trace}
}

func (f *fakeKeyboardEventSource) BeginShow() {
	f.mu.Lock()
	f.showCount++
	f.mu.Unlock()
}

func (f *fakeKeyboardEventSource) ShowCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.showCount
}

func (f *fakeKeyboardEventSource) Enqueue(events ...KeyboardEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, events...)
}

func (f *fakeKeyboardEventSource) NextKeyboardEvent(ctx context.Context) (KeyboardEvent, error) {
	if err := ctx.Err(); err != nil {
		return KeyboardEvent{}, err
	}
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return KeyboardEvent{}, io.ErrClosedPipe
	}
	if len(f.events) == 0 {
		f.mu.Unlock()
		return KeyboardEvent{}, io.EOF
	}
	event := f.events[0]
	f.events = f.events[1:]
	f.mu.Unlock()

	fields := map[string]any{
		"kind":      event.Kind,
		"key":       event.Key,
		"state":     event.State,
		"modifiers": event.Modifiers,
		"repeated":  event.Repeated,
	}
	switch event.Kind {
	case KeyboardEventKeymap:
		fields["offset"] = event.Keymap.Offset
		fields["size"] = event.Keymap.Size
		f.trace.Record(traceKeyboardKeymap, fields)
	case KeyboardEventEnter:
		f.trace.Record(traceKeyboardEnter, fields)
	case KeyboardEventLeave:
		f.trace.Record(traceKeyboardLeave, fields)
	case KeyboardEventDestroy:
		f.trace.Record(traceKeyboardDestroy, fields)
	case KeyboardEventKey:
		f.trace.Record(traceKeyboardKey, fields)
	case KeyboardEventModifiers:
		f.trace.Record(traceKeyboardModifiers, fields)
	case KeyboardEventRepeat:
		fields["repeat_rate"] = event.RepeatRate
		fields["repeat_delay"] = event.RepeatDelay.String()
		f.trace.Record(traceKeyboardRepeat, fields)
	}
	return event, nil
}

func (f *fakeKeyboardEventSource) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

type recordedPointerEvent struct {
	Kind       string
	Motion     PointerMotion
	Button     PointerButtonEvent
	Frame      PointerFrame
	OrderIndex int
}

type pointerRecorder struct {
	mu     sync.Mutex
	trace  *TraceRecorder
	events []recordedPointerEvent
}

func newPointerRecorder(trace *TraceRecorder) *pointerRecorder {
	return &pointerRecorder{trace: trace}
}

func (p *pointerRecorder) MovePointer(ctx context.Context, motion PointerMotion) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	p.events = append(p.events, recordedPointerEvent{Kind: "motion", Motion: motion, OrderIndex: len(p.events)})
	p.mu.Unlock()
	p.trace.Record(tracePointerMotion, map[string]any{"position": motion.Position, "at": motion.At})
	return nil
}

func (p *pointerRecorder) Button(ctx context.Context, button PointerButtonEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	p.events = append(p.events, recordedPointerEvent{Kind: "button", Button: button, OrderIndex: len(p.events)})
	p.mu.Unlock()
	p.trace.Record(tracePointerButton, map[string]any{
		"button":      button.Button,
		"state":       button.State,
		"position":    button.Position,
		"click_group": button.ClickGroup,
		"click_count": button.ClickCount,
		"sequence":    button.Sequence,
		"at":          button.At,
	})
	return nil
}

func (p *pointerRecorder) Frame(ctx context.Context, frame PointerFrame) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	p.events = append(p.events, recordedPointerEvent{Kind: "frame", Frame: frame, OrderIndex: len(p.events)})
	p.mu.Unlock()
	p.trace.Record(tracePointerFrame, map[string]any{"output_name": frame.OutputName, "at": frame.At})
	return nil
}

func (p *pointerRecorder) Events() []recordedPointerEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]recordedPointerEvent, len(p.events))
	copy(out, p.events)
	return out
}

type fakeRendererBufferSink struct {
	mu      sync.Mutex
	trace   *TraceRecorder
	uploads []fakeRendererUpload
}

type fakeRendererUpload struct {
	Target              string
	StraightHash        string
	PremultipliedPixels []ARGBPixel
}

func newFakeRendererBufferSink(trace *TraceRecorder) *fakeRendererBufferSink {
	return &fakeRendererBufferSink{trace: trace}
}

func (s *fakeRendererBufferSink) UploadARGB(ctx context.Context, target string, buffer ARGBSnapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	premultiplied := buffer.PremultipliedForWayland()
	for _, pixel := range premultiplied {
		if !IsPremultipliedARGB(pixel) {
			return fmt.Errorf("upload pixel %#08x is not premultiplied", uint32(pixel))
		}
	}
	upload := fakeRendererUpload{
		Target:              target,
		StraightHash:        buffer.StraightHash(),
		PremultipliedPixels: premultiplied,
	}
	s.mu.Lock()
	s.uploads = append(s.uploads, upload)
	s.mu.Unlock()
	s.trace.Record(traceOverlayRender, map[string]any{
		"target":        target,
		"straight_hash": upload.StraightHash,
		"premultiplied": true,
	})
	return nil
}

func (s *fakeRendererBufferSink) Uploads() []fakeRendererUpload {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]fakeRendererUpload, len(s.uploads))
	copy(out, s.uploads)
	return out
}

type fakeServiceStatusProvider struct {
	status InstalledServiceStatus
	err    error
}

func (f fakeServiceStatusProvider) InstalledServiceStatus(ctx context.Context) (InstalledServiceStatus, error) {
	if err := ctx.Err(); err != nil {
		return InstalledServiceStatus{}, err
	}
	return f.status, f.err
}

type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	nextID int
	timers []*fakeTimer
	trace  *TraceRecorder
}

func newFakeClock(start time.Time, trace *TraceRecorder) *fakeClock {
	return &fakeClock{now: start, trace: trace}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) NewTimer(d time.Duration) TimerHandle {
	c.mu.Lock()
	c.nextID++
	timer := &fakeTimer{
		id:     c.nextID,
		clock:  c,
		due:    c.now.Add(d),
		active: true,
		ch:     make(chan time.Time, 1),
	}
	c.timers = append(c.timers, timer)
	c.mu.Unlock()

	c.trace.Record(traceTimerCreate, map[string]any{"timer_id": timer.id, "duration": d.String(), "due": timer.due})
	return timer
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	var due []*fakeTimer
	for _, timer := range c.timers {
		if timer.active && !timer.due.After(now) {
			timer.active = false
			due = append(due, timer)
		}
	}
	c.mu.Unlock()

	for _, timer := range due {
		timer.ch <- now
		c.trace.Record(traceTimerFire, map[string]any{"timer_id": timer.id, "at": now})
	}
}

type fakeTimer struct {
	id     int
	clock  *fakeClock
	due    time.Time
	active bool
	ch     chan time.Time
}

func (t *fakeTimer) C() <-chan time.Time {
	return t.ch
}

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	wasActive := t.active
	t.active = false
	t.clock.mu.Unlock()

	t.clock.trace.Record(traceTimerStop, map[string]any{"timer_id": t.id, "was_active": wasActive})
	return wasActive
}

func (t *fakeTimer) Reset(d time.Duration) bool {
	t.clock.mu.Lock()
	wasActive := t.active
	t.active = true
	t.due = t.clock.now.Add(d)
	due := t.due
	t.clock.mu.Unlock()

	t.clock.trace.Record(traceTimerReset, map[string]any{
		"timer_id":   t.id,
		"duration":   d.String(),
		"due":        due,
		"was_active": wasActive,
	})
	return wasActive
}
