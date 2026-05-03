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
	surfaces map[int]*fakeOverlaySurface
	wait     chan struct{}
}

func newFakeWaylandBackend(trace *TraceRecorder) *fakeWaylandBackend {
	return &fakeWaylandBackend{
		trace:    trace,
		keyboard: newFakeKeyboardEventSource(trace),
		surfaces: make(map[int]*fakeOverlaySurface),
		wait:     make(chan struct{}),
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
	surface := &fakeOverlaySurface{backend: f, id: id, monitor: monitor, lifecycle: newFakeOverlayLifecycleEventSource()}
	f.surfaces[id] = surface
	f.events = append(f.events, fakeWaylandEvent{Kind: "surface_create", SurfaceID: id, Monitor: monitor, OutputName: monitor.Name})
	f.wakeLocked()
	f.mu.Unlock()
	f.trace.Record(traceOverlaySurfaceCreate, map[string]any{"surface_id": id, "monitor": monitor})
	return surface, nil
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
	f.wakeLocked()
}

func (f *fakeWaylandBackend) Events() []fakeWaylandEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeWaylandEvent, len(f.events))
	copy(out, f.events)
	return out
}

func (f *fakeWaylandBackend) LastSurface() *fakeOverlaySurface {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.nextID == 0 {
		return nil
	}
	return f.surfaces[f.nextID]
}

func (f *fakeWaylandBackend) WaitForEventCount(ctx context.Context, count int) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		f.mu.Lock()
		if len(f.events) >= count {
			f.mu.Unlock()
			return nil
		}
		wait := f.wait
		f.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wait:
		}
	}
}

func (f *fakeWaylandBackend) wakeLocked() {
	close(f.wait)
	f.wait = make(chan struct{})
}

type fakeOverlaySurface struct {
	backend   *fakeWaylandBackend
	id        int
	monitor   Monitor
	lifecycle *fakeOverlayLifecycleEventSource
	mu        sync.Mutex
	destroyed bool
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
	s.mu.Lock()
	if s.destroyed {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	s.backend.record(fakeWaylandEvent{Kind: "unmap", SurfaceID: s.id, Monitor: s.monitor})
	s.backend.trace.Record(traceOverlayUnmap, map[string]any{"surface_id": s.id})
	return nil
}

func (s *fakeOverlaySurface) Destroy(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.destroyed {
		s.mu.Unlock()
		return nil
	}
	s.destroyed = true
	s.mu.Unlock()
	if s.lifecycle != nil {
		_ = s.lifecycle.Close()
	}
	s.backend.record(fakeWaylandEvent{Kind: "destroy", SurfaceID: s.id, Monitor: s.monitor})
	s.backend.trace.Record(traceOverlayDestroy, map[string]any{"surface_id": s.id})
	return nil
}

func (s *fakeOverlaySurface) LifecycleEvents() OverlayLifecycleEventSource {
	return s.lifecycle
}

func (s *fakeOverlaySurface) EnqueueLifecycle(events ...OverlayLifecycleEvent) {
	for _, event := range events {
		s.lifecycle.enqueue(event)
	}
}

type fakeOverlayLifecycleEventSource struct {
	queue *overlayEventQueue[OverlayLifecycleEvent]
}

func newFakeOverlayLifecycleEventSource() *fakeOverlayLifecycleEventSource {
	return &fakeOverlayLifecycleEventSource{queue: newOverlayEventQueue[OverlayLifecycleEvent]()}
}

func (s *fakeOverlayLifecycleEventSource) NextOverlayEvent(ctx context.Context) (OverlayLifecycleEvent, error) {
	return s.queue.pop(ctx, nil)
}

func (s *fakeOverlayLifecycleEventSource) Close() error {
	return s.queue.close()
}

func (s *fakeOverlayLifecycleEventSource) enqueue(event OverlayLifecycleEvent) {
	s.queue.push(event)
}

type fakeKeyboardEventSource struct {
	mu             sync.Mutex
	trace          *TraceRecorder
	events         []KeyboardEvent
	showCount      int
	closed         bool
	blockWhenEmpty bool
	wait           chan struct{}
}

func newFakeKeyboardEventSource(trace *TraceRecorder) *fakeKeyboardEventSource {
	return &fakeKeyboardEventSource{trace: trace, wait: make(chan struct{})}
}

func (f *fakeKeyboardEventSource) BeginShow() {
	f.mu.Lock()
	f.showCount++
	f.closed = false
	if f.wait == nil {
		f.wait = make(chan struct{})
	}
	f.wakeLocked()
	f.mu.Unlock()
}

func (f *fakeKeyboardEventSource) ShowCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.showCount
}

func (f *fakeKeyboardEventSource) WaitForShowCount(ctx context.Context, count int) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		f.mu.Lock()
		if f.showCount >= count {
			f.mu.Unlock()
			return nil
		}
		wait := f.wait
		f.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wait:
		}
	}
}

func (f *fakeKeyboardEventSource) Enqueue(events ...KeyboardEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, events...)
	f.wakeLocked()
}

func (f *fakeKeyboardEventSource) SetBlocking(block bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blockWhenEmpty = block
	f.wakeLocked()
}

func (f *fakeKeyboardEventSource) PendingEvents() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.events)
}

func (f *fakeKeyboardEventSource) WaitForPendingEvents(ctx context.Context, count int) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		f.mu.Lock()
		if len(f.events) == count {
			f.mu.Unlock()
			return nil
		}
		wait := f.wait
		f.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wait:
		}
	}
}

func (f *fakeKeyboardEventSource) NextKeyboardEvent(ctx context.Context) (KeyboardEvent, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return KeyboardEvent{}, err
	}
	var event KeyboardEvent
	for {
		f.mu.Lock()
		if f.closed {
			f.mu.Unlock()
			return KeyboardEvent{}, io.ErrClosedPipe
		}
		if len(f.events) > 0 {
			event = f.events[0]
			f.events = f.events[1:]
			f.wakeLocked()
			f.mu.Unlock()
			break
		}
		if !f.blockWhenEmpty {
			f.mu.Unlock()
			return KeyboardEvent{}, io.EOF
		}
		wait := f.wait
		f.mu.Unlock()

		select {
		case <-ctx.Done():
			return KeyboardEvent{}, ctx.Err()
		case <-wait:
		}
	}

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
	f.wakeLocked()
	return nil
}

func (f *fakeKeyboardEventSource) wakeLocked() {
	close(f.wait)
	f.wait = make(chan struct{})
}

type recordedPointerEvent struct {
	Kind       string
	Motion     PointerMotion
	Button     PointerButtonEvent
	Frame      PointerFrame
	OrderIndex int
}

type pointerRecorder struct {
	mu           sync.Mutex
	trace        *TraceRecorder
	events       []recordedPointerEvent
	wait         chan struct{}
	now          func() time.Time
	lastPosition PointerPosition
	hasPosition  bool
	clickGroup   int
}

func newPointerRecorder(trace *TraceRecorder) *pointerRecorder {
	return &pointerRecorder{trace: trace, wait: make(chan struct{}), now: time.Now}
}

func (p *pointerRecorder) MovePointer(ctx context.Context, motion PointerMotion) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	p.events = append(p.events, recordedPointerEvent{Kind: "motion", Motion: motion, OrderIndex: len(p.events)})
	p.lastPosition = motion.Position
	p.hasPosition = true
	p.wakeLocked()
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
	p.wakeLocked()
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
	p.wakeLocked()
	p.mu.Unlock()
	p.trace.Record(tracePointerFrame, map[string]any{"output_name": frame.OutputName, "at": frame.At})
	return nil
}

func (p *pointerRecorder) MoveAbsolute(ctx context.Context, x, y float64, output Monitor) error {
	if err := output.Validate(); err != nil {
		return err
	}
	at := p.clockNow()
	position := PointerPosition{X: x, Y: y, OutputName: output.Name}
	if err := p.MovePointer(ctx, PointerMotion{Position: position, At: at}); err != nil {
		return err
	}
	return p.Frame(ctx, PointerFrame{OutputName: output.Name, At: at})
}

func (p *pointerRecorder) LeftClick(ctx context.Context) error {
	return p.click(ctx, PointerButtonLeft, 1)
}

func (p *pointerRecorder) RightClick(ctx context.Context) error {
	return p.click(ctx, PointerButtonRight, 1)
}

func (p *pointerRecorder) DoubleClick(ctx context.Context) error {
	return p.click(ctx, PointerButtonLeft, 2)
}

func (p *pointerRecorder) click(ctx context.Context, button PointerButton, count int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if count <= 0 {
		return fmt.Errorf("click count must be positive, got %d", count)
	}
	p.mu.Lock()
	if !p.hasPosition {
		p.mu.Unlock()
		return fmt.Errorf("pointer recorder click requires a prior MoveAbsolute target")
	}
	p.clickGroup++
	clickGroup := p.clickGroup
	position := p.lastPosition
	p.mu.Unlock()

	p.trace.Record(traceClickGroupStart, map[string]any{"click_group": clickGroup, "click_count": count, "position": position})
	for sequence := 1; sequence <= count; sequence++ {
		if err := p.Button(ctx, PointerButtonEvent{
			Button:     button,
			State:      PointerButtonDown,
			Position:   position,
			ClickGroup: clickGroup,
			ClickCount: count,
			Sequence:   sequence,
			At:         p.clockNow(),
		}); err != nil {
			return err
		}
		if err := p.Button(ctx, PointerButtonEvent{
			Button:     button,
			State:      PointerButtonUp,
			Position:   position,
			ClickGroup: clickGroup,
			ClickCount: count,
			Sequence:   sequence,
			At:         p.clockNow(),
		}); err != nil {
			return err
		}
		if err := p.Frame(ctx, PointerFrame{OutputName: position.OutputName, At: p.clockNow()}); err != nil {
			return err
		}
	}
	p.trace.Record(traceClickGroupComplete, map[string]any{"click_group": clickGroup, "click_count": count})
	return nil
}

func (p *pointerRecorder) clockNow() time.Time {
	p.mu.Lock()
	now := p.now
	p.mu.Unlock()
	if now == nil {
		return time.Now()
	}
	return now()
}

func (p *pointerRecorder) Events() []recordedPointerEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]recordedPointerEvent, len(p.events))
	copy(out, p.events)
	return out
}

func (p *pointerRecorder) WaitForEventCount(ctx context.Context, count int) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		p.mu.Lock()
		if len(p.events) >= count {
			p.mu.Unlock()
			return nil
		}
		wait := p.wait
		p.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wait:
		}
	}
}

func (p *pointerRecorder) wakeLocked() {
	close(p.wait)
	p.wait = make(chan struct{})
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

func (c *fakeClock) TimerCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.timers)
}

func (c *fakeClock) ActiveTimerCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	var active int
	for _, timer := range c.timers {
		if timer.active {
			active++
		}
	}
	return active
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
