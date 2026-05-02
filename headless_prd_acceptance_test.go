package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestHeadlessPRDAcceptanceSuite(t *testing.T) {
	t.Run("show displays 26x26 grid on focused monitor and grabs keyboard", func(t *testing.T) {
		h := newAcceptanceHarness(t, acceptanceHarnessOptions{})

		response := h.show()
		if !response.Active || response.State != string(DaemonStateOverlayShown) {
			h.failf("show response = %+v, want active overlay", response)
		}

		expectedHash := acceptanceMainGridHash(t, h.focused, h.config, h.atlas, DefaultMainGridHUD, nil)
		h.assertEventOrder(
			eventPattern{Source: "trace", Action: "ipc_command"},
			eventPattern{Source: "trace", Action: "show_requested"},
			eventPattern{Source: "wayland", Action: "surface_create"},
			eventPattern{Source: "wayland", Action: "configure"},
			eventPattern{Source: "wayland", Action: "keyboard_grab"},
			eventPattern{Source: "renderer", Action: "present"},
			eventPattern{Source: "wayland", Action: "render"},
			eventPattern{Source: "trace", Action: "overlay_shown"},
		)
		h.assertWaylandCount("surface_create", 1)
		h.assertWaylandCount("keyboard_grab", 1)
		h.assertWaylandCount("destroy", 0)
		h.assertTraceField("state", "overlay_shown", "output", h.focused.Name)
		h.assertTraceField("state", "overlay_shown", "scale", h.focused.Scale)
		h.assertLastRendererHash(expectedHash)

		create := h.requireWaylandEvent("surface_create")
		if create.OutputName != h.focused.Name || create.Width != h.focused.Width || create.Height != h.focused.Height {
			h.failf("created surface = %+v, want focused monitor %+v", create, h.focused)
		}
		grab := h.requireWaylandEvent("keyboard_grab")
		if grab.KeyboardInteractivity != "exclusive" {
			h.failf("keyboard grab interactivity = %q, want exclusive", grab.KeyboardInteractivity)
		}
		if h.config.Grid.Size != 26 {
			h.failf("grid size = %d, want PRD 26x26", h.config.Grid.Size)
		}
	})

	t.Run("two coordinate letters plus Enter completes a left click in 3-5 keystrokes", func(t *testing.T) {
		h := newAcceptanceHarness(t, acceptanceHarnessOptions{})
		h.show()

		cell := h.sendMainCoordinate('M', 'K')
		h.waitForTraceAction("fsm", "subgrid_shown")
		h.sendKeys("Return")
		h.waitForTraceAction("fsm", "left_click_pending")
		if clicks := acceptanceClickCount(h.pointer, PointerButtonLeft); clicks != 0 {
			h.failf("left clicks before double-click timeout = %d, want 0", clicks)
		}

		h.clock.Advance(time.Duration(h.config.Behavior.DoubleClickTimeoutMS) * time.Millisecond)
		h.waitForClickCount(PointerButtonLeft, 1)
		h.waitForTraceAction("state", "stay_active_reset")

		keyboardSends := h.events.Filter(func(event acceptanceEvent) bool {
			return event.Source == "keyboard" && event.Action == "send"
		})
		if len(keyboardSends) != 3 {
			h.failf("keyboard sends for left-click flow = %d, want M/K/Return", len(keyboardSends))
		}
		triggerToClickKeystrokes := 1 + len(keyboardSends)
		if triggerToClickKeystrokes < 3 || triggerToClickKeystrokes > 5 {
			h.failf("trigger-to-click keystrokes = %d, want 3-5", triggerToClickKeystrokes)
		}
		h.assertPointerButtonsAt(PointerButtonLeft, cell.Center(), 1)
		h.assertLastPointerMotion(cell.Center())
		h.assertTracePointerClick(PointerButtonLeft, 1, cell.Center())
		h.assertTraceAfter("io", "pointer_click", "state", "overlay_unmapped_for_click")
		h.assertTraceAfter("state", "stay_active_reset", "io", "pointer_click")
		h.waitForLastRendererHash(acceptanceMainGridHash(t, h.focused, h.config, h.atlas, DefaultMainGridHUD, nil))
		h.assertWaylandCount("surface_create", 2)
		h.assertWaylandCount("destroy", 1)
	})

	t.Run("Enter Enter double-click keeps the same committed cursor and does not reopen main grid between clicks", func(t *testing.T) {
		h := newAcceptanceHarness(t, acceptanceHarnessOptions{})
		h.show()

		cell := h.sendMainCoordinate('M', 'K')
		h.waitForTraceAction("fsm", "subgrid_shown")
		mainGridHash := acceptanceMainGridHash(t, h.focused, h.config, h.atlas, DefaultMainGridHUD, nil)

		h.sendKeys("Return")
		h.waitForTraceAction("fsm", "left_click_pending")
		if clicks := acceptanceClickCount(h.pointer, PointerButtonLeft); clicks != 0 {
			h.failf("left clicks after first Enter = %d, want 0 while waiting for double-click timeout", clicks)
		}

		h.sendKeys("Return")
		h.waitForClickCount(PointerButtonLeft, 2)
		h.waitForTraceAction("state", "stay_active_reset")
		h.clock.Advance(time.Duration(h.config.Behavior.DoubleClickTimeoutMS) * time.Millisecond)
		if clicks := acceptanceClickCount(h.pointer, PointerButtonLeft); clicks != 2 {
			h.failf("left clicks after canceled double-click timeout = %d, want exactly 2", clicks)
		}

		h.assertPointerButtonsAt(PointerButtonLeft, cell.Center(), 2)
		h.assertTracePointerClick(PointerButtonLeft, 2, cell.Center())
		h.assertTraceAfter("io", "pointer_click", "state", "overlay_unmapped_for_click")
		h.assertNoMainGridReopenBetweenFirstTwoLeftClicks(mainGridHash)
		h.waitForLastRendererHash(mainGridHash)
		h.assertWaylandCount("surface_create", 2)
		h.assertWaylandCount("destroy", 1)
	})

	t.Run("focused monitor targeting works with a non-zero virtual-layout origin", func(t *testing.T) {
		monitors := fakeMonitorFixtures()
		focused := fakeFocusedMonitorFixture()
		layout, err := MonitorLayoutBounds(monitors)
		if err != nil {
			t.Fatalf("monitor layout bounds: %v", err)
		}
		h := newAcceptanceHarness(t, acceptanceHarnessOptions{
			monitors:        monitors,
			focused:         focused,
			pointerFallback: true,
		})
		h.show()

		cell := h.sendMainCoordinate('M', 'K')
		h.waitForTraceAction("fsm", "subgrid_shown")
		h.sendKeys("Return")
		h.waitForTraceAction("fsm", "left_click_pending")
		h.clock.Advance(time.Duration(h.config.Behavior.DoubleClickTimeoutMS) * time.Millisecond)
		h.waitForClickCount(PointerButtonLeft, 1)

		h.assertFocusedOverlayTarget(focused)
		h.assertFallbackMotion(cell.Center(), focused, layout)
		h.assertPointerButtonsAt(PointerButtonLeft, cell.Center(), 1)
		h.assertTracePointerClick(PointerButtonLeft, 1, cell.Center())
	})

	t.Run("grid and pointer targeting remain correct with scale not equal to 1", func(t *testing.T) {
		monitors := fakeMonitorFixtures()
		focused := fakeFocusedMonitorFixture()
		h := newAcceptanceHarness(t, acceptanceHarnessOptions{
			monitors: monitors,
			focused:  focused,
		})
		h.show()

		expectedGridHash := acceptanceMainGridHash(t, focused, h.config, h.atlas, DefaultMainGridHUD, nil)
		h.assertLastRendererHash(expectedGridHash)
		h.assertFocusedOverlayTarget(focused)
		if focused.Scale == 1 {
			h.failf("focused fixture scale = %g, want scale != 1", focused.Scale)
		}

		cell := h.sendMainCoordinate('M', 'K')
		h.waitForTraceAction("fsm", "subgrid_shown")
		h.assertWithOutputMotion(cell.Center(), focused)

		presentation := h.lastPresentation()
		if presentation.Width != focused.Width || presentation.Height != focused.Height {
			h.failf("scaled presentation size = %dx%d, want logical %dx%d", presentation.Width, presentation.Height, focused.Width, focused.Height)
		}
	})

	t.Run("Escape exits without clicking and overrides stay_active", func(t *testing.T) {
		h := newAcceptanceHarness(t, acceptanceHarnessOptions{})
		h.show()
		cell := h.sendMainCoordinate('M', 'K')
		h.waitForTraceAction("fsm", "subgrid_shown")
		h.sendKeys("Return")
		h.waitForTraceAction("fsm", "left_click_pending")

		h.sendKeys("Escape")
		h.waitForTraceAction("state", "overlay_hidden")
		h.clock.Advance(time.Duration(h.config.Behavior.DoubleClickTimeoutMS) * time.Millisecond)

		if h.controller.State() != DaemonStateInactive {
			h.failf("state after Escape = %q, want inactive", h.controller.State())
		}
		if clicks := acceptanceClickCount(h.pointer, PointerButtonLeft); clicks != 0 {
			h.failf("left clicks after Escape and timeout = %d, want 0", clicks)
		}
		h.assertLastPointerMotion(cell.Center())
		h.assertWaylandCount("destroy", 1)
		h.assertTraceAbsent("state", "stay_active_reset")
	})
}

type fakeEventObserver interface {
	ObserveFakeEvent(source string, action string, fields map[string]any)
}

func observeFakeEvent(observer fakeEventObserver, source string, action string, fields map[string]any) {
	if observer == nil {
		return
	}
	observer.ObserveFakeEvent(source, action, fields)
}

type acceptanceHarnessOptions struct {
	monitors        []Monitor
	focused         Monitor
	pointerFallback bool
}

type acceptanceHarness struct {
	t          *testing.T
	ctx        context.Context
	cancel     context.CancelFunc
	socketPath string

	config Config
	atlas  *FontAtlas
	clock  *fakeClock

	focused  Monitor
	wayland  *fakeWaylandBackend
	keyboard *fakeKeyboardEventSource
	renderer *fakeRendererSink
	pointer  *virtualPointerRecorder
	trace    *acceptanceTraceRecorder
	events   *acceptanceEventLog

	controller *DaemonController
	server     *IPCServer
}

func newAcceptanceHarness(t *testing.T, opts acceptanceHarnessOptions) *acceptanceHarness {
	t.Helper()

	config := DefaultConfig()
	config.Behavior.StayActive = true
	config.Behavior.DoubleClickTimeoutMS = 250
	atlas, err := NewFontAtlasFromConfig(config)
	if err != nil {
		t.Fatalf("font atlas: %v", err)
	}

	monitors := append([]Monitor(nil), opts.monitors...)
	if len(monitors) == 0 {
		monitors = []Monitor{{Name: "HEADLESS-1", Width: 520, Height: 520, Scale: 1, Focused: true}}
	}
	focused := opts.focused
	if focused.Name == "" {
		focused = monitors[0]
		for _, monitor := range monitors {
			if monitor.Focused {
				focused = monitor
				break
			}
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	clock := newFakeClock(time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC))
	events := &acceptanceEventLog{}
	wayland := newFakeWaylandBackend(monitors...)
	wayland.observer = events
	keyboard := newFakeKeyboardEventSource(32)
	keyboard.observer = events
	renderer := &fakeRendererSink{observer: events}
	var pointer *virtualPointerRecorder
	if opts.pointerFallback {
		layout, err := MonitorLayoutBounds(monitors)
		if err != nil {
			t.Fatalf("monitor layout bounds: %v", err)
		}
		pointer = newFallbackVirtualPointerRecorder(clock, layout)
	} else {
		pointer = newVirtualPointerRecorder(clock)
	}
	pointer.observer = events
	trace := &acceptanceTraceRecorder{observer: events}

	controller := NewDaemonController(DaemonDeps{
		MonitorLookup: &fakeFocusedMonitorLookup{monitor: focused},
		Overlay:       wayland,
		Keyboard:      keyboard,
		Renderer:      renderer,
		Config:        &config,
		FontAtlas:     atlas,
		Pointer:       pointer,
		Clock:         clock,
		Trace:         trace,
	})

	socketPath := filepath.Join(t.TempDir(), IPCSocketName)
	server, err := NewIPCServer(socketPath, controller, &logger{out: io.Discard}, trace)
	if err != nil {
		cancel()
		t.Fatalf("new IPC server: %v", err)
	}
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(ctx)
	}()

	h := &acceptanceHarness{
		t:          t,
		ctx:        ctx,
		cancel:     cancel,
		socketPath: socketPath,
		config:     config,
		atlas:      atlas,
		clock:      clock,
		focused:    focused,
		wayland:    wayland,
		keyboard:   keyboard,
		renderer:   renderer,
		pointer:    pointer,
		trace:      trace,
		events:     events,
		controller: controller,
		server:     server,
	}

	t.Cleanup(func() {
		cancel()
		_ = server.Close()
		keyboard.Close()
		select {
		case err := <-serveErr:
			if err != nil {
				t.Errorf("IPC server returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("IPC server did not stop")
		}
	})
	return h
}

func (h *acceptanceHarness) show() IPCResponse {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(h.ctx, 2*time.Second)
	defer cancel()
	response, err := SendIPCCommandToPath(ctx, h.socketPath, "show")
	if err != nil {
		h.failf("show through IPC: %v", err)
	}
	return response
}

func (h *acceptanceHarness) sendMainCoordinate(col byte, row byte) Rect {
	h.t.Helper()
	h.sendKeys(string([]byte{col}), string([]byte{row}))
	h.waitForTraceAction("fsm", "main_coordinate_selected")
	bounds, err := GridCellBounds(h.focused, h.config.Grid.Size, int(col-'A'), int(row-'A'))
	if err != nil {
		h.failf("expected grid cell %c%c: %v", col, row, err)
	}
	return bounds
}

func (h *acceptanceHarness) sendKeys(keys ...string) {
	h.t.Helper()
	for _, key := range keys {
		h.keyboard.Send(KeyboardEvent{
			Kind:    KeyboardEventKey,
			Key:     key,
			Pressed: true,
			Time:    h.clock.Now(),
		})
	}
}

func (h *acceptanceHarness) waitForTraceAction(kind string, action string) acceptanceTraceEvent {
	h.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if event, ok := h.trace.Find(kind, action); ok {
			return event
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.failf("trace action %s/%s not observed", kind, action)
	return acceptanceTraceEvent{}
}

func (h *acceptanceHarness) waitForClickCount(button PointerButton, want int) {
	h.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := acceptanceClickCount(h.pointer, button); got == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.failf("%s click count = %d, want %d", button, acceptanceClickCount(h.pointer, button), want)
}

func (h *acceptanceHarness) assertEventOrder(patterns ...eventPattern) {
	h.t.Helper()
	events := h.events.Events()
	start := 0
	for _, pattern := range patterns {
		found := -1
		for i := start; i < len(events); i++ {
			if pattern.Matches(events[i]) {
				found = i
				break
			}
		}
		if found < 0 {
			h.failf("event pattern %+v not found in order after index %d", pattern, start)
		}
		start = found + 1
	}
}

func (h *acceptanceHarness) assertWaylandCount(kind string, want int) {
	h.t.Helper()
	if got := h.wayland.Count(kind); got != want {
		h.failf("wayland %s count = %d, want %d", kind, got, want)
	}
}

func (h *acceptanceHarness) requireWaylandEvent(kind string) fakeWaylandEvent {
	h.t.Helper()
	for _, event := range h.wayland.Events() {
		if event.Kind == kind {
			return event
		}
	}
	h.failf("wayland event %q not found", kind)
	return fakeWaylandEvent{}
}

func (h *acceptanceHarness) assertTraceField(kind string, action string, key string, want any) {
	h.t.Helper()
	event := h.waitForTraceAction(kind, action)
	if got := event.Fields[key]; got != want {
		h.failf("trace %s/%s field %q = %#v, want %#v", kind, action, key, got, want)
	}
}

func (h *acceptanceHarness) assertTraceAbsent(kind string, action string) {
	h.t.Helper()
	if event, ok := h.trace.Find(kind, action); ok {
		h.failf("unexpected trace %s/%s present: %+v", kind, action, event)
	}
}

func (h *acceptanceHarness) assertTraceAfter(afterKind string, afterAction string, beforeKind string, beforeAction string) {
	h.t.Helper()
	after, ok := h.events.Find(func(event acceptanceEvent) bool {
		return event.Source == "trace" && event.Action == afterAction && event.Fields["trace_kind"] == afterKind
	})
	if !ok {
		h.failf("trace %s/%s not found", afterKind, afterAction)
	}
	before, ok := h.events.Find(func(event acceptanceEvent) bool {
		return event.Source == "trace" && event.Action == beforeAction && event.Fields["trace_kind"] == beforeKind
	})
	if !ok {
		h.failf("trace %s/%s not found", beforeKind, beforeAction)
	}
	if after.Seq <= before.Seq {
		h.failf("trace %s/%s seq=%d, want after %s/%s seq=%d", afterKind, afterAction, after.Seq, beforeKind, beforeAction, before.Seq)
	}
}

func (h *acceptanceHarness) assertTracePointerClick(button PointerButton, count int, point Point) {
	h.t.Helper()
	event, ok := h.trace.FindMatching("io", "pointer_click", func(fields map[string]any) bool {
		return fields["button"] == string(button) &&
			fields["click_count"] == count &&
			fields["output"] == h.focused.Name &&
			fields["x"] == point.X &&
			fields["y"] == point.Y
	})
	if !ok {
		h.failf("trace pointer_click not found for button=%s count=%d point=%+v", button, count, point)
	}
	if event.Fields["group_id"] == "" {
		h.failf("pointer_click trace lacks group_id: %+v", event)
	}
}

func (h *acceptanceHarness) assertLastRendererHash(want string) {
	h.t.Helper()
	presentation := h.lastPresentation()
	if presentation.Hash != want {
		h.failf("last renderer hash = %s, want %s", presentation.Hash, want)
	}
}

func (h *acceptanceHarness) waitForLastRendererHash(want string) {
	h.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		presentations := h.renderer.Presentations()
		if len(presentations) > 0 && presentations[len(presentations)-1].Hash == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.assertLastRendererHash(want)
}

func (h *acceptanceHarness) lastPresentation() fakeRenderPresentation {
	h.t.Helper()
	presentations := h.renderer.Presentations()
	if len(presentations) == 0 {
		h.failf("renderer has no presentations")
	}
	return presentations[len(presentations)-1]
}

func (h *acceptanceHarness) assertPointerButtonsAt(button PointerButton, point Point, clickCount int) {
	h.t.Helper()
	var downs int
	var ups int
	for _, event := range h.pointer.Events() {
		if event.Kind != "button" || event.Button != button {
			continue
		}
		if event.OutputName != h.focused.Name || event.X != point.X || event.Y != point.Y {
			h.failf("pointer button event = %+v, want output=%s point=%+v", event, h.focused.Name, point)
		}
		switch event.State {
		case ButtonDown:
			downs++
		case ButtonUp:
			ups++
		}
	}
	if downs != clickCount || ups != clickCount {
		h.failf("%s button down/up = %d/%d, want %d/%d", button, downs, ups, clickCount, clickCount)
	}
}

func (h *acceptanceHarness) assertLastPointerMotion(point Point) {
	h.t.Helper()
	for i := len(h.pointer.Events()) - 1; i >= 0; i-- {
		event := h.pointer.Events()[i]
		if event.Kind != "motion" {
			continue
		}
		if event.OutputName != h.focused.Name || event.X != point.X || event.Y != point.Y {
			h.failf("last pointer motion = %+v, want output=%s point=%+v", event, h.focused.Name, point)
		}
		return
	}
	h.failf("no pointer motion events recorded")
}

func (h *acceptanceHarness) assertNoMainGridReopenBetweenFirstTwoLeftClicks(mainGridHash string) {
	h.t.Helper()
	buttonDowns := h.events.Filter(func(event acceptanceEvent) bool {
		return event.Source == "pointer" &&
			event.Action == "button" &&
			event.Fields["button"] == string(PointerButtonLeft) &&
			event.Fields["state"] == string(ButtonDown)
	})
	if len(buttonDowns) != 2 {
		h.failf("left button down event count = %d, want 2", len(buttonDowns))
	}
	firstSeq := buttonDowns[0].Seq
	secondSeq := buttonDowns[1].Seq
	for _, event := range h.events.Events() {
		if event.Seq <= firstSeq || event.Seq >= secondSeq {
			continue
		}
		if event.Source == "renderer" && event.Action == "present" && event.Fields["hash"] == mainGridHash {
			h.failf("main-grid renderer presentation reopened between double-click button events: %+v", event)
		}
		if event.Source == "wayland" && event.Action == "render" && event.Fields["buffer_hash"] == mainGridHash {
			h.failf("main-grid surface render reopened between double-click button events: %+v", event)
		}
		if event.Source == "trace" && event.Action == "stay_active_reset" {
			h.failf("stay-active reset occurred between double-click button events: %+v", event)
		}
	}
}

func (h *acceptanceHarness) assertFocusedOverlayTarget(focused Monitor) {
	h.t.Helper()
	create := h.requireWaylandEvent("surface_create")
	configure := h.requireWaylandEvent("configure")
	if create.OutputName != focused.Name || configure.OutputName != focused.Name {
		h.failf("overlay target create/configure outputs = %q/%q, want %q", create.OutputName, configure.OutputName, focused.Name)
	}
	if configure.Width != focused.Width || configure.Height != focused.Height || configure.Scale != focused.Scale {
		h.failf("overlay configure = %+v, want focused logical geometry %+v", configure, focused)
	}
	h.assertTraceField("state", "overlay_shown", "output", focused.Name)
	h.assertTraceField("state", "overlay_shown", "x", focused.X)
	h.assertTraceField("state", "overlay_shown", "y", focused.Y)
}

func (h *acceptanceHarness) assertFallbackMotion(point Point, focused Monitor, layout Rect) {
	h.t.Helper()
	motion := h.lastPointerMotionEvent()
	virtual := focused.LocalToVirtual(point)
	wantProtocolX := uint32(virtual.X - layout.X)
	wantProtocolY := uint32(virtual.Y - layout.Y)
	if motion.Mapping != PointerMappingFallback ||
		motion.OutputName != focused.Name ||
		motion.X != point.X ||
		motion.Y != point.Y ||
		motion.ProtocolX != wantProtocolX ||
		motion.ProtocolY != wantProtocolY ||
		motion.XExtent != uint32(layout.Width) ||
		motion.YExtent != uint32(layout.Height) {
		h.failf("fallback motion = %+v, want local=%+v virtual=%+v protocol=%d,%d layout=%+v output=%s", motion, point, virtual, wantProtocolX, wantProtocolY, layout, focused.Name)
	}
}

func (h *acceptanceHarness) assertWithOutputMotion(point Point, focused Monitor) {
	h.t.Helper()
	motion := h.lastPointerMotionEvent()
	if motion.Mapping != PointerMappingWithOutput ||
		motion.OutputName != focused.Name ||
		motion.X != point.X ||
		motion.Y != point.Y ||
		motion.ProtocolX != uint32(point.X) ||
		motion.ProtocolY != uint32(point.Y) ||
		motion.XExtent != uint32(focused.Width) ||
		motion.YExtent != uint32(focused.Height) {
		h.failf("with-output motion = %+v, want focused logical point=%+v output=%+v", motion, point, focused)
	}
}

func (h *acceptanceHarness) lastPointerMotionEvent() recordedPointerEvent {
	h.t.Helper()
	events := h.pointer.Events()
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind == "motion" {
			return events[i]
		}
	}
	h.failf("no pointer motion events recorded")
	return recordedPointerEvent{}
}

func (h *acceptanceHarness) failf(format string, args ...any) {
	h.t.Helper()
	message := fmt.Sprintf(format, args...)
	h.t.Fatalf("%s\nacceptance diagnostics:\n%s", message, h.diagnostics())
}

func (h *acceptanceHarness) diagnostics() string {
	diag := map[string]any{
		"focused_monitor":        h.focused,
		"config_grid_size":       h.config.Grid.Size,
		"double_click_timeout":   h.config.Behavior.DoubleClickTimeoutMS,
		"wayland_counts":         acceptanceWaylandCounts(h.wayland.Events()),
		"wayland_events":         h.wayland.Events(),
		"renderer_presentations": summarizePresentations(h.renderer.Presentations()),
		"pointer_events":         h.pointer.Events(),
		"left_click_count":       acceptanceClickCount(h.pointer, PointerButtonLeft),
		"right_click_count":      acceptanceClickCount(h.pointer, PointerButtonRight),
		"trace_events":           h.trace.Events(),
		"event_sequence":         h.events.Events(),
	}
	data, err := json.MarshalIndent(diag, "", "  ")
	if err != nil {
		return fmt.Sprintf("failed to marshal diagnostics: %v", err)
	}
	return string(data)
}

type acceptanceTraceRecorder struct {
	mu       sync.Mutex
	events   []acceptanceTraceEvent
	observer fakeEventObserver
}

type acceptanceTraceEvent struct {
	Kind   string         `json:"kind"`
	Action string         `json:"action"`
	Fields map[string]any `json:"fields,omitempty"`
}

func (r *acceptanceTraceRecorder) Record(kind string, action string, fields map[string]any) {
	if r == nil {
		return
	}
	event := acceptanceTraceEvent{Kind: kind, Action: action, Fields: copyAcceptanceFields(fields)}
	r.mu.Lock()
	r.events = append(r.events, event)
	r.mu.Unlock()

	observedFields := copyAcceptanceFields(fields)
	observedFields["trace_kind"] = kind
	observeFakeEvent(r.observer, "trace", action, observedFields)
}

func (r *acceptanceTraceRecorder) Events() []acceptanceTraceEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]acceptanceTraceEvent(nil), r.events...)
}

func (r *acceptanceTraceRecorder) Find(kind string, action string) (acceptanceTraceEvent, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, event := range r.events {
		if event.Kind == kind && event.Action == action {
			return event, true
		}
	}
	return acceptanceTraceEvent{}, false
}

func (r *acceptanceTraceRecorder) FindMatching(kind string, action string, match func(map[string]any) bool) (acceptanceTraceEvent, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, event := range r.events {
		if event.Kind == kind && event.Action == action && match(event.Fields) {
			return event, true
		}
	}
	return acceptanceTraceEvent{}, false
}

type acceptanceEventLog struct {
	mu     sync.Mutex
	next   int
	events []acceptanceEvent
}

type acceptanceEvent struct {
	Seq    int            `json:"seq"`
	Source string         `json:"source"`
	Action string         `json:"action"`
	Fields map[string]any `json:"fields,omitempty"`
}

func (l *acceptanceEventLog) ObserveFakeEvent(source string, action string, fields map[string]any) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.next++
	l.events = append(l.events, acceptanceEvent{
		Seq:    l.next,
		Source: source,
		Action: action,
		Fields: copyAcceptanceFields(fields),
	})
}

func (l *acceptanceEventLog) Events() []acceptanceEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]acceptanceEvent(nil), l.events...)
}

func (l *acceptanceEventLog) Find(match func(acceptanceEvent) bool) (acceptanceEvent, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, event := range l.events {
		if match(event) {
			return event, true
		}
	}
	return acceptanceEvent{}, false
}

func (l *acceptanceEventLog) Filter(match func(acceptanceEvent) bool) []acceptanceEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []acceptanceEvent
	for _, event := range l.events {
		if match(event) {
			out = append(out, event)
		}
	}
	return out
}

type eventPattern struct {
	Source string
	Action string
}

func (p eventPattern) Matches(event acceptanceEvent) bool {
	if p.Source != "" && event.Source != p.Source {
		return false
	}
	if p.Action != "" && event.Action != p.Action {
		return false
	}
	return true
}

func fakeWaylandEventFields(event fakeWaylandEvent) map[string]any {
	fields := map[string]any{
		"surface_id": event.SurfaceID,
		"output":     event.OutputName,
		"width":      event.Width,
		"height":     event.Height,
		"scale":      event.Scale,
	}
	if event.KeyboardInteractivity != "" {
		fields["keyboard_interactivity"] = event.KeyboardInteractivity
	}
	if event.BufferHash != "" {
		fields["buffer_hash"] = event.BufferHash
	}
	return fields
}

func recordedPointerEventFields(event recordedPointerEvent) map[string]any {
	fields := map[string]any{
		"output":     event.OutputName,
		"x":          event.X,
		"y":          event.Y,
		"protocol_x": event.ProtocolX,
		"protocol_y": event.ProtocolY,
		"x_extent":   event.XExtent,
		"y_extent":   event.YExtent,
		"mapping":    string(event.Mapping),
		"time":       event.Time.UTC().Format(time.RFC3339Nano),
	}
	if event.Button != "" {
		fields["button"] = string(event.Button)
	}
	if event.State != "" {
		fields["state"] = string(event.State)
	}
	if event.GroupID != "" {
		fields["group_id"] = event.GroupID
	}
	return fields
}

func copyAcceptanceFields(fields map[string]any) map[string]any {
	out := make(map[string]any, len(fields)+1)
	for k, v := range fields {
		out[k] = v
	}
	return out
}

func acceptanceMainGridHash(t *testing.T, monitor Monitor, config Config, atlas *FontAtlas, hud string, selectedColumn *int) string {
	t.Helper()
	buffer, err := NewARGBBuffer(monitor.Width, monitor.Height)
	if err != nil {
		t.Fatalf("new expected main-grid buffer: %v", err)
	}
	if err := RenderMainGridOverlay(buffer, MainGridRenderOptions{
		GridSize:       config.Grid.Size,
		Appearance:     config.Appearance,
		FontAtlas:      atlas,
		HUD:            hud,
		SelectedColumn: selectedColumn,
	}); err != nil {
		t.Fatalf("render expected main grid: %v", err)
	}
	return mustARGBHash(t, buffer)
}

func acceptanceClickCount(pointer *virtualPointerRecorder, button PointerButton) int {
	count := 0
	for _, event := range pointer.Events() {
		if event.Kind == "button" && event.Button == button && event.State == ButtonDown {
			count++
		}
	}
	return count
}

func acceptanceWaylandCounts(events []fakeWaylandEvent) map[string]int {
	counts := map[string]int{}
	for _, event := range events {
		counts[event.Kind]++
	}
	return counts
}

func summarizePresentations(presentations []fakeRenderPresentation) []map[string]any {
	out := make([]map[string]any, 0, len(presentations))
	for i, presentation := range presentations {
		out = append(out, map[string]any{
			"index":      i,
			"surface_id": presentation.SurfaceID,
			"width":      presentation.Width,
			"height":     presentation.Height,
			"hash":       presentation.Hash,
		})
	}
	return out
}
