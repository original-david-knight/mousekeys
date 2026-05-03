package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestHeadlessPRDAcceptanceSuite(t *testing.T) {
	t.Run("show_grid_keyboard_grab_and_snapshot_style", func(t *testing.T) {
		monitor := Monitor{Name: "DP-accept", LogicalWidth: 780, LogicalHeight: 520, Scale: 1}
		h := newHeadlessPRDAcceptanceHarness(t, []Monitor{monitor}, nil)
		h.enqueueKeyboardLifecycle()

		h.show()
		h.waitKeyboardDrained()
		h.waitTraceEvent(traceKeyboardEnter)

		h.assertWaylandPrefix("surface_create", "configure", "render", "keyboard_grab")
		h.assertTraceContains(
			traceOverlaySurfaceCreate,
			traceOverlayConfigure,
			traceOverlayRender,
			traceOverlayKeyboardGrab,
			traceKeyboardKeymap,
			traceKeyboardEnter,
		)

		grid, err := NewGridGeometry(monitor, h.config.Grid.Size)
		if err != nil {
			t.Fatalf("NewGridGeometry returned error: %v", err)
		}
		if grid.Size != 26 || len(grid.Columns) != 26 || len(grid.Rows) != 26 {
			t.Fatalf("grid geometry = %d columns=%d rows=%d, want 26x26", grid.Size, len(grid.Columns), len(grid.Rows))
		}

		render := h.lastRenderEvent()
		if render.Width != monitor.LogicalWidth || render.Height != monitor.LogicalHeight || render.OutputName != monitor.Name {
			t.Fatalf("render event = %+v, want focused monitor logical size and output", render)
		}
		expected, err := h.renderer.RenderMainGrid(monitor, h.config.Grid.Size)
		if err != nil {
			t.Fatalf("RenderMainGrid returned error: %v", err)
		}
		if render.BufferHash != expected.StraightHash() {
			t.Fatalf("render hash = %s, want main-grid hash %s", render.BufferHash, expected.StraightHash())
		}
		assertHeadlessGridSnapshotStyle(t, render.Snapshot, grid)
	})

	t.Run("coordinate_space_timeout_left_click_unmaps_and_stays_active", func(t *testing.T) {
		monitor := Monitor{Name: "DP-left", LogicalWidth: 260, LogicalHeight: 260, Scale: 1}
		h := newHeadlessPRDAcceptanceHarness(t, []Monitor{monitor}, func(config *Config) {
			config.Behavior.StayActive = true
			config.Behavior.DoubleClickTimeoutMS = 250
		})
		h.enqueueKeyboardLifecycle()
		h.enqueueKeystroke("m", ModifierState{})
		h.enqueueKeystroke("k", ModifierState{})
		h.enqueueKeystroke("space", ModifierState{})

		h.show()
		h.waitPointerEvents(2)
		h.waitKeyboardDrained()
		if buttons := recordedButtonEvents(h.pointer.Events()); len(buttons) != 0 {
			t.Fatalf("left click fired before timeout: %+v", buttons)
		}

		h.clock.Advance(249 * time.Millisecond)
		if buttons := recordedButtonEvents(h.pointer.Events()); len(buttons) != 0 {
			t.Fatalf("left click fired before configured timeout: %+v", buttons)
		}
		h.clock.Advance(time.Millisecond)
		h.waitPointerEvents(5)
		h.waitShowCount(2)

		events := h.traceEvents()
		presses := pressedKeyCountBeforeFirstButton(events)
		if presses < 3 || presses > 5 {
			t.Fatalf("pressed key count before first button = %d, want 3-5", presses)
		}
		grid, err := NewGridGeometry(monitor, h.config.Grid.Size)
		if err != nil {
			t.Fatalf("NewGridGeometry returned error: %v", err)
		}
		localX, localY, err := grid.CellCenterLocal(12, 10)
		if err != nil {
			t.Fatalf("CellCenterLocal returned error: %v", err)
		}
		virtualX, virtualY, err := grid.CellCenterVirtual(12, 10)
		if err != nil {
			t.Fatalf("CellCenterVirtual returned error: %v", err)
		}
		selected := requireTraceEvent(t, events, traceCoordinateSelected)
		assertSelectedTraceMonitor(t, selected, monitor)
		assertSelectedTraceCenter(t, selected, localX, localY, virtualX, virtualY)
		h.assertTraceContains(traceCoordinateInput, traceCoordinateSelected, traceTimerCreate, traceTimerFire, traceOverlayUnmappedForClick, tracePointerButton, traceClickGroupComplete, traceStayActiveReset)
		assertOverlayUnmappedForClickBeforePointerButtons(t, events)
		assertStayActiveResetAfterClickComplete(t, events)

		motions := recordedMotionPositions(h.pointer.Events())
		buttons := recordedButtonEvents(h.pointer.Events())
		assertButtonClick(t, buttons, PointerButtonLeft, 1, motions[0])
		assertTraceButtonClick(t, events, PointerButtonLeft, 1, motions[0])
	})

	t.Run("shift_space_right_click_only", func(t *testing.T) {
		monitor := Monitor{Name: "DP-right", LogicalWidth: 260, LogicalHeight: 260, Scale: 1}
		h := newHeadlessPRDAcceptanceHarness(t, []Monitor{monitor}, func(config *Config) {
			config.Behavior.StayActive = false
		})
		h.enqueueKeyboardLifecycle()
		h.enqueueKeystroke("m", ModifierState{})
		h.enqueueKeystroke("k", ModifierState{})
		h.enqueueKey(KeyboardEvent{Kind: KeyboardEventKey, Key: "space", State: KeyPressed, Modifiers: ModifierState{Shift: true}})

		h.show()
		h.waitPointerEvents(5)

		if h.clock.TimerCount() != 0 || h.clock.ActiveTimerCount() != 0 {
			t.Fatalf("Shift-space created left/double-click timer: total=%d active=%d", h.clock.TimerCount(), h.clock.ActiveTimerCount())
		}
		motions := recordedMotionPositions(h.pointer.Events())
		buttons := recordedButtonEvents(h.pointer.Events())
		assertButtonClick(t, buttons, PointerButtonRight, 1, motions[0])
		for _, button := range buttons {
			if button.Button != PointerButtonRight {
				t.Fatalf("Shift-space emitted non-right button: %+v", buttons)
			}
		}
		events := h.traceEvents()
		assertOverlayUnmappedForClickBeforePointerButtons(t, events)
		assertNoTraceEvent(t, events, traceStayActiveReset)
	})

	t.Run("space_space_double_click_same_position_no_main_grid_reopen_between_buttons", func(t *testing.T) {
		monitor := Monitor{Name: "DP-double", LogicalWidth: 260, LogicalHeight: 260, Scale: 1}
		h := newHeadlessPRDAcceptanceHarness(t, []Monitor{monitor}, func(config *Config) {
			config.Behavior.StayActive = true
			config.Behavior.DoubleClickTimeoutMS = 250
		})
		mainGrid, err := h.renderer.RenderMainGrid(monitor, h.config.Grid.Size)
		if err != nil {
			t.Fatalf("RenderMainGrid returned error: %v", err)
		}
		h.enqueueKeyboardLifecycle()
		h.enqueueKeystroke("m", ModifierState{})
		h.enqueueKeystroke("k", ModifierState{})
		h.enqueueKeystroke("space", ModifierState{})
		h.enqueueKeystroke("space", ModifierState{})

		h.show()
		h.waitPointerEvents(8)
		h.waitShowCount(2)
		h.clock.Advance(time.Second)

		motions := recordedMotionPositions(h.pointer.Events())
		buttons := recordedButtonEvents(h.pointer.Events())
		assertButtonClick(t, buttons, PointerButtonLeft, 2, motions[0])
		if got := len(recordedButtonEvents(h.pointer.Events())); got != 4 {
			t.Fatalf("stale double-click timer emitted extra button events: got %d want 4", got)
		}

		events := h.traceEvents()
		h.assertTraceContains(traceOverlayUnmappedForClick, tracePointerButton, traceClickGroupStart, traceClickGroupComplete, traceStayActiveReset)
		assertOverlayUnmappedForClickBeforePointerButtons(t, events)
		assertNoOverlayRenderBetweenPointerButtons(t, events)
		assertNoMainGridRenderBetweenDoubleClickButtons(t, events, h.wayland.Events(), mainGrid.StraightHash())
		assertStayActiveResetAfterClickComplete(t, events)
	})

	t.Run("selected_outline_hidden_movement_leaves_cell_and_clamps", func(t *testing.T) {
		monitor := Monitor{Name: "DP-move", LogicalWidth: 260, LogicalHeight: 260, Scale: 1}
		h := newHeadlessPRDAcceptanceHarness(t, []Monitor{monitor}, nil)
		h.enqueueKeyboardLifecycle()
		h.enqueueKeystroke("m", ModifierState{})
		h.enqueueKeystroke("k", ModifierState{})

		h.show()
		h.waitPointerEvents(2)
		h.waitKeyboardDrained()

		grid, err := NewGridGeometry(monitor, h.config.Grid.Size)
		if err != nil {
			t.Fatalf("NewGridGeometry returned error: %v", err)
		}
		cell, err := grid.Cell(12, 10)
		if err != nil {
			t.Fatalf("Cell returned error: %v", err)
		}
		assertSelectedOutlineSnapshot(t, h.lastRenderEvent().Snapshot, cell)

		h.enqueueKeystroke("L", ModifierState{})
		h.enqueueKeystroke("Right", ModifierState{})
		h.enqueueKeystroke("J", ModifierState{})
		h.enqueueKeystroke("Down", ModifierState{})
		for i := 0; i < 40; i++ {
			h.enqueueKeystroke("H", ModifierState{})
		}
		for i := 0; i < 40; i++ {
			h.enqueueKeystroke("Up", ModifierState{})
		}
		h.waitKeyboardDrained()

		motions := recordedMotionPositions(h.pointer.Events())
		if len(motions) < 7 {
			t.Fatalf("motion count = %d, want selected target plus hidden movements: %+v", len(motions), motions)
		}
		if motions[2].X <= float64(cell.X+cell.Width) {
			t.Fatalf("arrow movement did not leave selected cell: motion=%+v cell=%+v", motions[2], cell)
		}
		if !hasPointerTarget(motions, PointerPosition{X: 0, Y: motions[len(motions)-1].Y, OutputName: monitor.Name}, true, false) {
			t.Fatalf("hidden H movement never clamped to left edge: %+v", motions)
		}
		if !hasPointerTarget(motions, PointerPosition{X: motions[len(motions)-1].X, Y: 0, OutputName: monitor.Name}, false, true) {
			t.Fatalf("hidden Up movement never clamped to top edge: %+v", motions)
		}
		h.assertTraceContains(traceCoordinateSelected, tracePointerMotion, traceTimerCreate, traceTimerStop)
	})

	t.Run("held_direction_repeat_fake_clock_delay_acceleration_and_release_stop", func(t *testing.T) {
		monitor := Monitor{Name: "DP-repeat", LogicalWidth: 260, LogicalHeight: 260, Scale: 1}
		h := newHeadlessPRDAcceptanceHarness(t, []Monitor{monitor}, nil)
		h.enqueueKeyboardLifecycle()
		h.enqueueKeystroke("m", ModifierState{})
		h.enqueueKeystroke("k", ModifierState{})
		h.enqueueKey(KeyboardEvent{Kind: KeyboardEventKey, Key: "L", State: KeyPressed})

		h.show()
		h.waitPointerEvents(4)
		if motions := recordedMotionPositions(h.pointer.Events()); len(motions) != 2 {
			t.Fatalf("motions before repeat delay = %+v, want selected target plus immediate movement", motions)
		}

		h.clock.Advance(349 * time.Millisecond)
		if motions := recordedMotionPositions(h.pointer.Events()); len(motions) != 2 {
			t.Fatalf("repeat fired before initial delay: %+v", motions)
		}
		h.clock.Advance(time.Millisecond)
		h.waitMotionCount(3)
		h.clock.Advance(50 * time.Millisecond)
		h.waitMotionCount(4)
		h.clock.Advance(450 * time.Millisecond)
		h.waitMotionCount(5)
		h.clock.Advance(500 * time.Millisecond)
		h.waitMotionCount(6)
		h.clock.Advance(500 * time.Millisecond)
		h.waitMotionCount(7)

		motions := recordedMotionPositions(h.pointer.Events())
		deltas := []float64{
			motions[1].X - motions[0].X,
			motions[2].X - motions[1].X,
			motions[3].X - motions[2].X,
			motions[4].X - motions[3].X,
			motions[5].X - motions[4].X,
			motions[6].X - motions[5].X,
		}
		wantDeltas := []float64{5, 5, 5, 10, 15, 20}
		if !slices.Equal(deltas, wantDeltas) {
			t.Fatalf("held-repeat target deltas = %v, want %v", deltas, wantDeltas)
		}
		if fires := h.traceEventCount(traceTimerFire); fires != 5 {
			t.Fatalf("timer fires = %d, want one per repeat tick (5)", fires)
		}
		if repeatedTargets := len(motions) - 2; repeatedTargets != h.traceEventCount(traceTimerFire) {
			t.Fatalf("repeat emitted %d targets for %d timer ticks", repeatedTargets, h.traceEventCount(traceTimerFire))
		}
		assertTimerResetDurations(t, h.traceEvents(), "50ms", "35ms", "25ms", "16ms")

		timerStopsBeforeRelease := h.traceEventCount(traceTimerStop)
		h.enqueueKey(KeyboardEvent{Kind: KeyboardEventKey, Key: "L", State: KeyReleased})
		h.waitKeyboardDrained()
		h.waitTraceEventCount(traceTimerStop, timerStopsBeforeRelease+1)
		afterRelease := len(h.pointer.Events())
		h.clock.Advance(2 * time.Second)
		if got := len(h.pointer.Events()); got != afterRelease {
			t.Fatalf("held repeat emitted after release: before=%d after=%d", afterRelease, got)
		}
	})

	t.Run("focused_monitor_offset_negative_origin_and_scaled_targets", func(t *testing.T) {
		fixtures := []Monitor{
			{Name: "DP-offset", OriginX: 1920, OriginY: 120, LogicalWidth: 260, LogicalHeight: 260, Scale: 1.25},
			{Name: "HDMI-negative", OriginX: -1280, OriginY: -360, LogicalWidth: 260, LogicalHeight: 260, Scale: 1},
			{Name: "eDP-scaled", OriginX: 320, OriginY: -80, LogicalWidth: 257, LogicalHeight: 193, Scale: 1.5},
		}
		for _, monitor := range fixtures {
			t.Run(monitor.Name, func(t *testing.T) {
				h := newHeadlessPRDAcceptanceHarness(t, []Monitor{monitor}, nil)
				h.enqueueKeyboardLifecycle()
				h.enqueueKeystroke("m", ModifierState{})
				h.enqueueKeystroke("k", ModifierState{})

				h.show()
				h.waitPointerEvents(2)
				h.waitKeyboardDrained()

				grid, err := NewGridGeometry(monitor, h.config.Grid.Size)
				if err != nil {
					t.Fatalf("NewGridGeometry returned error: %v", err)
				}
				localX, localY, err := grid.CellCenterLocal(12, 10)
				if err != nil {
					t.Fatalf("CellCenterLocal returned error: %v", err)
				}
				motions := recordedMotionPositions(h.pointer.Events())
				want := PointerPosition{X: localX, Y: localY, OutputName: monitor.Name}
				if len(motions) == 0 || motions[0] != want {
					t.Fatalf("focused-monitor pointer target = %+v, want %+v", motions, want)
				}
				h.assertConfiguredForMonitor(monitor)

				selected := requireTraceEvent(t, h.traceEvents(), traceCoordinateSelected)
				if selected.Fields["center_virtual_x"].(float64) != localX+float64(monitor.OriginX) ||
					selected.Fields["center_virtual_y"].(float64) != localY+float64(monitor.OriginY) {
					t.Fatalf("selected trace virtual center = %+v, want local center plus origin %+v", selected.Fields, monitor)
				}
				h.assertTraceContains(traceOverlaySurfaceCreate, traceOverlayRender, traceCoordinateSelected, tracePointerMotion)
			})
		}
	})

	t.Run("escape_exits_without_click_and_overrides_stay_active", func(t *testing.T) {
		monitor := Monitor{Name: "DP-escape", LogicalWidth: 260, LogicalHeight: 260, Scale: 1}
		h := newHeadlessPRDAcceptanceHarness(t, []Monitor{monitor}, func(config *Config) {
			config.Behavior.StayActive = true
			config.Behavior.DoubleClickTimeoutMS = 250
		})
		h.enqueueKeyboardLifecycle()
		h.enqueueKeystroke("m", ModifierState{})
		h.enqueueKeystroke("k", ModifierState{})
		h.enqueueKeystroke("space", ModifierState{})
		h.enqueueKey(KeyboardEvent{Kind: KeyboardEventKey, Key: "Escape", State: KeyPressed})

		h.show()
		h.waitKeyboardDrained()
		h.waitWaylandEventKind("destroy")
		h.clock.Advance(time.Second)

		if h.driver.OverlayActive() {
			t.Fatal("overlay stayed active after Escape")
		}
		if got := len(recordedButtonEvents(h.pointer.Events())); got != 0 {
			t.Fatalf("Escape emitted %d button events", got)
		}
		if got := h.wayland.keyboard.ShowCount(); got != 1 {
			t.Fatalf("Escape recreated stay-active grid: show count = %d want 1", got)
		}
		if h.clock.TimerCount() != 1 || h.clock.ActiveTimerCount() != 0 {
			t.Fatalf("pending click timer after Escape = total %d active %d, want one stopped timer", h.clock.TimerCount(), h.clock.ActiveTimerCount())
		}
		events := h.traceEvents()
		assertNoTraceEvent(t, events, tracePointerButton)
		assertNoTraceEvent(t, events, traceStayActiveReset)
	})
}

type headlessPRDAcceptanceHarness struct {
	t          *testing.T
	ctx        context.Context
	config     Config
	traceBytes *lockedTraceBuffer
	trace      *TraceRecorder
	clock      *fakeClock
	wayland    *fakeWaylandBackend
	pointer    *pointerRecorder
	renderer   *SoftwareRenderer
	driver     *layerShellOverlayDriver
	controller *daemonController
}

func newHeadlessPRDAcceptanceHarness(t *testing.T, monitors []Monitor, configure func(*Config)) *headlessPRDAcceptanceHarness {
	t.Helper()
	if len(monitors) == 0 {
		t.Fatal("headless PRD acceptance harness requires at least one monitor")
	}
	traceBytes := &lockedTraceBuffer{}
	trace := NewTraceRecorder(traceBytes, nil)
	clock := newFakeClock(time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC), trace)
	trace.now = clock.Now
	config := DefaultConfig()
	if configure != nil {
		configure(&config)
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("acceptance config Validate returned error: %v", err)
	}
	renderer, err := NewSoftwareRenderer(config.Appearance)
	if err != nil {
		t.Fatalf("NewSoftwareRenderer returned error: %v", err)
	}
	pointer := newPointerRecorder(trace)
	pointer.now = clock.Now
	wayland := newFakeWaylandBackend(trace)
	wayland.keyboard.SetBlocking(true)
	driver, err := newLayerShellOverlayDriverWithOptions(newFakeHyprlandIPC(monitors...), wayland, renderer, config, trace, layerShellOverlayDriverOptions{
		Pointer: pointer,
		Clock:   clock,
	})
	if err != nil {
		t.Fatalf("newLayerShellOverlayDriverWithOptions returned error: %v", err)
	}
	h := &headlessPRDAcceptanceHarness{
		t:          t,
		ctx:        context.Background(),
		config:     config,
		traceBytes: traceBytes,
		trace:      trace,
		clock:      clock,
		wayland:    wayland,
		pointer:    pointer,
		renderer:   renderer,
		driver:     driver,
		controller: newDaemonController(driver, statusOutput{}),
	}
	t.Cleanup(func() {
		_ = h.driver.CloseOverlay(context.Background())
		if t.Failed() {
			t.Logf("headless_prd_trace=%s", h.traceSummaryJSON())
		}
	})
	return h
}

func (h *headlessPRDAcceptanceHarness) enqueueKeyboardLifecycle() {
	h.t.Helper()
	h.enqueueKey(
		KeyboardEvent{Kind: KeyboardEventKeymap, Keymap: &KeyboardKeymapFD{Data: []byte("xacceptance-keymapx"), Offset: 1, Size: 17}},
		KeyboardEvent{Kind: KeyboardEventEnter},
	)
}

func (h *headlessPRDAcceptanceHarness) enqueueKeystroke(key string, modifiers ModifierState) {
	h.t.Helper()
	h.enqueueKey(
		KeyboardEvent{Kind: KeyboardEventKey, Key: key, State: KeyPressed, Modifiers: modifiers},
		KeyboardEvent{Kind: KeyboardEventKey, Key: key, State: KeyReleased, Modifiers: modifiers},
	)
}

func (h *headlessPRDAcceptanceHarness) enqueueKey(events ...KeyboardEvent) {
	h.t.Helper()
	h.wayland.keyboard.Enqueue(events...)
}

func (h *headlessPRDAcceptanceHarness) show() {
	h.t.Helper()
	response := h.controller.Dispatch(h.ctx, ipcRequest{Command: "show"})
	if !response.OK || !response.Active || response.Action != "shown" {
		h.t.Fatalf("show response = %+v, want active shown", response)
	}
}

func (h *headlessPRDAcceptanceHarness) waitKeyboardDrained() {
	h.t.Helper()
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := h.wayland.keyboard.WaitForPendingEvents(waitCtx, 0); err != nil {
		h.t.Fatalf("keyboard events were not consumed: %v", err)
	}
}

func (h *headlessPRDAcceptanceHarness) waitPointerEvents(count int) {
	h.t.Helper()
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := h.pointer.WaitForEventCount(waitCtx, count); err != nil {
		h.t.Fatalf("pointer event count %d was not reached: %v", count, err)
	}
}

func (h *headlessPRDAcceptanceHarness) waitMotionCount(count int) {
	h.t.Helper()
	waitForCondition(h.t, func() bool {
		return len(recordedMotionPositions(h.pointer.Events())) >= count
	})
}

func (h *headlessPRDAcceptanceHarness) waitShowCount(count int) {
	h.t.Helper()
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := h.wayland.keyboard.WaitForShowCount(waitCtx, count); err != nil {
		h.t.Fatalf("keyboard show count %d was not reached: %v", count, err)
	}
}

func (h *headlessPRDAcceptanceHarness) waitWaylandEventKind(kind string) {
	h.t.Helper()
	waitForCondition(h.t, func() bool {
		for _, event := range h.wayland.Events() {
			if event.Kind == kind {
				return true
			}
		}
		return false
	})
}

func (h *headlessPRDAcceptanceHarness) assertWaylandPrefix(wants ...string) {
	h.t.Helper()
	events := h.wayland.Events()
	if len(events) < len(wants) {
		h.t.Fatalf("Wayland event count = %d, want prefix %v; events=%+v", len(events), wants, events)
	}
	for i, want := range wants {
		if events[i].Kind != want {
			h.t.Fatalf("Wayland event[%d] = %q, want %q; events=%+v", i, events[i].Kind, want, events)
		}
	}
}

func (h *headlessPRDAcceptanceHarness) assertTraceContains(wants ...string) {
	h.t.Helper()
	assertTraceContains(h.t, h.traceEvents(), wants...)
}

func (h *headlessPRDAcceptanceHarness) traceEvents() []TraceEvent {
	h.t.Helper()
	return decodeTraceEvents(h.t, h.traceBytes.String())
}

func (h *headlessPRDAcceptanceHarness) traceEventCount(kind string) int {
	h.t.Helper()
	count := 0
	for _, event := range h.traceEvents() {
		if event.Event == kind {
			count++
		}
	}
	return count
}

func (h *headlessPRDAcceptanceHarness) waitTraceEvent(kind string) {
	h.t.Helper()
	h.waitTraceEventCount(kind, 1)
}

func (h *headlessPRDAcceptanceHarness) waitTraceEventCount(kind string, count int) {
	h.t.Helper()
	waitForCondition(h.t, func() bool {
		return h.traceEventCount(kind) >= count
	})
}

func (h *headlessPRDAcceptanceHarness) lastRenderEvent() fakeWaylandEvent {
	h.t.Helper()
	events := h.wayland.Events()
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind == "render" {
			return events[i]
		}
	}
	h.t.Fatalf("no render event recorded; events=%+v", events)
	return fakeWaylandEvent{}
}

func (h *headlessPRDAcceptanceHarness) assertConfiguredForMonitor(monitor Monitor) {
	h.t.Helper()
	for _, event := range h.wayland.Events() {
		if event.Kind == "configure" && event.OutputName == monitor.Name &&
			event.Width == monitor.LogicalWidth && event.Height == monitor.LogicalHeight && event.Scale == monitor.Scale {
			return
		}
	}
	h.t.Fatalf("no configure event for monitor %+v; events=%+v", monitor, h.wayland.Events())
}

func (h *headlessPRDAcceptanceHarness) traceSummaryJSON() string {
	summary := map[string]any{
		"trace_events":       decodeTraceEventsLenient(h.traceBytes.String()),
		"overlay_lifecycle":  summarizeOverlayLifecycle(h.wayland.Events()),
		"keyboard_lifecycle": filterTraceEvents(decodeTraceEventsLenient(h.traceBytes.String()), "keyboard."),
		"cursor_targets":     recordedMotionPositions(h.pointer.Events()),
		"button_events":      recordedButtonEvents(h.pointer.Events()),
		"timers":             filterTraceEvents(decodeTraceEventsLenient(h.traceBytes.String()), "timer."),
		"output_names":       summarizeOutputNames(h.wayland.Events(), h.pointer.Events()),
		"renderer_hashes":    summarizeRendererHashes(h.wayland.Events()),
	}
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	return string(data)
}

func assertHeadlessGridSnapshotStyle(t *testing.T, snapshot ARGBSnapshot, grid GridGeometry) {
	t.Helper()
	if snapshot.Width != grid.Monitor.LogicalWidth || snapshot.Height != grid.Monitor.LogicalHeight {
		t.Fatalf("snapshot dimensions = %dx%d, want %dx%d", snapshot.Width, snapshot.Height, grid.Monitor.LogicalWidth, grid.Monitor.LogicalHeight)
	}
	boundaryX := grid.Columns[12].End
	lineY := (grid.Rows[10].Start + grid.Rows[10].End) / 2
	core := mustPixelAt(t, snapshot, boundaryX, lineY)
	if core.A() == 0 || core.A() == 255 || core.B() <= core.R() || core.B() <= core.G() {
		t.Fatalf("grid core pixel is not translucent bluish: %#08x", uint32(core))
	}
	halo := mustPixelAt(t, snapshot, boundaryX-1, lineY)
	if halo.A() == 0 || halo.A() == 255 || halo.B() <= halo.G() || halo.G() <= halo.R() {
		t.Fatalf("grid halo pixel is not translucent dark blue: %#08x", uint32(halo))
	}
	cell, err := grid.Cell(13, 10)
	if err != nil {
		t.Fatalf("Cell returned error: %v", err)
	}
	emptyX, emptyY := cell.Center()
	if empty := mustPixelAt(t, snapshot, int(emptyX), int(emptyY)); empty.A() != 0 {
		t.Fatalf("empty grid cell is not transparent: %#08x", uint32(empty))
	}
	_, _, label := findPixel(t, snapshot, Rect{X: 0, Y: 0, Width: snapshot.Width, Height: grid.Rows[0].Size()}, func(pixel ARGBPixel) bool {
		return pixel.A() > 0 && pixel.G() > pixel.R()+20 && pixel.G() > pixel.B()
	})
	if label.A() == 0 {
		t.Fatalf("edge label foreground is not visible: %#08x", uint32(label))
	}
	upload := snapshot.PremultipliedForWayland()
	premultiplied := upload[lineY*snapshot.Width+boundaryX]
	if !IsPremultipliedARGB(premultiplied) {
		t.Fatalf("Wayland upload pixel is not premultiplied: straight=%#08x upload=%#08x", uint32(core), uint32(premultiplied))
	}
	if premultiplied.A() != core.A() || premultiplied.B() <= premultiplied.R() {
		t.Fatalf("premultiplied grid core lost alpha/color requirements: straight=%#08x upload=%#08x", uint32(core), uint32(premultiplied))
	}
}

func assertSelectedOutlineSnapshot(t *testing.T, snapshot ARGBSnapshot, cell Rect) {
	t.Helper()
	edge := mustPixelAt(t, snapshot, cell.X, cell.Y+cell.Height/2)
	if edge.A() == 0 || edge.B() <= edge.R() {
		t.Fatalf("selected-cell outline is not visible/bluish: %#08x", uint32(edge))
	}
	center := mustPixelAt(t, snapshot, cell.X+cell.Width/2, cell.Y+cell.Height/2)
	if center.A() != 0 {
		t.Fatalf("selected-cell outline filled the selected cell center: %#08x", uint32(center))
	}
	outside := mustPixelAt(t, snapshot, 30, 30)
	if outside.A() != 0 {
		t.Fatalf("selected-cell outline did not leave the main grid hidden: outside=%#08x", uint32(outside))
	}
}

func pressedKeyCountBeforeFirstButton(events []TraceEvent) int {
	count := 0
	for _, event := range events {
		if event.Event == tracePointerButton {
			return count
		}
		if event.Event == traceKeyboardKey && event.Fields["state"] == string(KeyPressed) {
			count++
		}
	}
	return count
}

func assertOverlayUnmappedForClickBeforePointerButtons(t *testing.T, events []TraceEvent) {
	t.Helper()
	unmapSeq := uint64(0)
	unmappedForClickSeq := uint64(0)
	for _, event := range events {
		switch event.Event {
		case traceOverlayUnmap:
			if unmapSeq == 0 {
				unmapSeq = event.Seq
			}
		case traceOverlayUnmappedForClick:
			if unmappedForClickSeq == 0 {
				unmappedForClickSeq = event.Seq
			}
		case tracePointerButton:
			if unmapSeq == 0 || unmappedForClickSeq == 0 {
				t.Fatalf("pointer button before overlay unmap-for-click marker: seq=%d fields=%+v events=%+v", event.Seq, event.Fields, events)
			}
			if !(unmapSeq < unmappedForClickSeq && unmappedForClickSeq < event.Seq) {
				t.Fatalf("bad click ordering: overlay.unmap=%d overlay.unmapped_for_click=%d pointer.button=%d", unmapSeq, unmappedForClickSeq, event.Seq)
			}
		}
	}
}

func assertSelectedTraceMonitor(t *testing.T, selected TraceEvent, monitor Monitor) {
	t.Helper()
	fields, ok := selected.Fields["monitor"].(map[string]any)
	if !ok {
		t.Fatalf("selected trace monitor field = %#v, want object for %+v", selected.Fields["monitor"], monitor)
	}
	wants := map[string]any{
		"name":           monitor.Name,
		"origin_x":       float64(monitor.OriginX),
		"origin_y":       float64(monitor.OriginY),
		"logical_width":  float64(monitor.LogicalWidth),
		"logical_height": float64(monitor.LogicalHeight),
		"scale":          monitor.Scale,
	}
	for field, want := range wants {
		if got := fields[field]; got != want {
			t.Fatalf("selected trace monitor[%s] = %#v, want %#v; fields=%+v", field, got, want, fields)
		}
	}
}

func assertSelectedTraceCenter(t *testing.T, selected TraceEvent, localX, localY, virtualX, virtualY float64) {
	t.Helper()
	wants := map[string]float64{
		"center_local_x":   localX,
		"center_local_y":   localY,
		"center_virtual_x": virtualX,
		"center_virtual_y": virtualY,
	}
	for field, want := range wants {
		if got, ok := selected.Fields[field].(float64); !ok || got != want {
			t.Fatalf("selected trace %s = %#v, want %v; fields=%+v", field, selected.Fields[field], want, selected.Fields)
		}
	}
}

func assertTraceButtonClick(t *testing.T, events []TraceEvent, button PointerButton, clickCount int, position PointerPosition) {
	t.Helper()
	var buttons []TraceEvent
	for _, event := range events {
		if event.Event == tracePointerButton {
			buttons = append(buttons, event)
		}
	}
	if len(buttons) != clickCount*2 {
		t.Fatalf("trace pointer.button count = %d, want %d for %d click(s): %+v", len(buttons), clickCount*2, clickCount, buttons)
	}
	clickGroup := buttons[0].Fields["click_group"]
	for i, event := range buttons {
		wantState := string(PointerButtonDown)
		if i%2 == 1 {
			wantState = string(PointerButtonUp)
		}
		wantSequence := float64(i/2 + 1)
		if event.Fields["button"] != string(button) || event.Fields["state"] != wantState || event.Fields["click_group"] != clickGroup || event.Fields["click_count"] != float64(clickCount) || event.Fields["sequence"] != wantSequence {
			t.Fatalf("trace pointer.button[%d] fields = %+v, want %s %s group %v count %d sequence %.0f", i, event.Fields, button, wantState, clickGroup, clickCount, wantSequence)
		}
		assertTracePointerPosition(t, event, position)
	}
}

func assertTracePointerPosition(t *testing.T, event TraceEvent, position PointerPosition) {
	t.Helper()
	fields, ok := event.Fields["position"].(map[string]any)
	if !ok {
		t.Fatalf("trace pointer.button position = %#v, want %+v", event.Fields["position"], position)
	}
	wants := map[string]any{
		"x":           position.X,
		"y":           position.Y,
		"output_name": position.OutputName,
	}
	for field, want := range wants {
		if got := fields[field]; got != want {
			t.Fatalf("trace pointer.button position[%s] = %#v, want %#v; fields=%+v", field, got, want, fields)
		}
	}
}

func assertNoMainGridRenderBetweenDoubleClickButtons(t *testing.T, traceEvents []TraceEvent, waylandEvents []fakeWaylandEvent, mainHash string) {
	t.Helper()
	firstButtonSeq := uint64(0)
	lastButtonSeq := uint64(0)
	for _, event := range traceEvents {
		if event.Event == tracePointerButton {
			if firstButtonSeq == 0 {
				firstButtonSeq = event.Seq
			}
			lastButtonSeq = event.Seq
		}
	}
	if firstButtonSeq == 0 || lastButtonSeq == 0 {
		t.Fatal("no pointer buttons recorded for double-click acceptance")
	}
	for _, event := range traceEvents {
		if event.Seq <= firstButtonSeq || event.Seq >= lastButtonSeq {
			continue
		}
		if event.Event == traceOverlaySurfaceCreate || event.Event == traceOverlayRender || event.Event == traceOverlayKeyboardGrab {
			t.Fatalf("overlay reopened between double-click button events at seq=%d event=%s", event.Seq, event.Event)
		}
	}
	unmapIndex := -1
	for i, event := range waylandEvents {
		if event.Kind == "unmap" {
			unmapIndex = i
			break
		}
	}
	if unmapIndex < 0 {
		t.Fatalf("no Wayland unmap event recorded: %+v", waylandEvents)
	}
	for i := unmapIndex + 1; i < len(waylandEvents); i++ {
		if waylandEvents[i].Kind == "render" && waylandEvents[i].BufferHash == mainHash {
			return
		}
		if waylandEvents[i].Kind == "render" && waylandEvents[i].BufferHash != "" {
			t.Fatalf("unexpected render before stay-active main grid after double-click: index=%d event=%+v", i, waylandEvents[i])
		}
	}
	t.Fatalf("stay-active main grid did not reopen after double-click completion; events=%+v", waylandEvents)
}

func assertTimerResetDurations(t *testing.T, events []TraceEvent, wants ...string) {
	t.Helper()
	seen := map[string]bool{}
	for _, event := range events {
		if event.Event != traceTimerReset {
			continue
		}
		if duration, ok := event.Fields["duration"].(string); ok {
			seen[duration] = true
		}
	}
	for _, want := range wants {
		if !seen[want] {
			t.Fatalf("timer reset duration %q missing; saw %v", want, sortedKeys(seen))
		}
	}
}

func hasPointerTarget(motions []PointerPosition, target PointerPosition, matchX, matchY bool) bool {
	for _, motion := range motions {
		if motion.OutputName != target.OutputName {
			continue
		}
		if matchX && motion.X != target.X {
			continue
		}
		if matchY && motion.Y != target.Y {
			continue
		}
		return true
	}
	return false
}

func decodeTraceEventsLenient(data string) []TraceEvent {
	var events []TraceEvent
	scanner := bufio.NewScanner(strings.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event TraceEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			events = append(events, TraceEvent{Event: "trace.decode_error", Fields: map[string]any{"error": err.Error(), "line": line}})
			continue
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		events = append(events, TraceEvent{Event: "trace.scan_error", Fields: map[string]any{"error": err.Error()}})
	}
	return events
}

func filterTraceEvents(events []TraceEvent, prefix string) []TraceEvent {
	var out []TraceEvent
	for _, event := range events {
		if strings.HasPrefix(event.Event, prefix) {
			out = append(out, event)
		}
	}
	return out
}

func summarizeOverlayLifecycle(events []fakeWaylandEvent) []map[string]any {
	var out []map[string]any
	for _, event := range events {
		out = append(out, map[string]any{
			"kind":        event.Kind,
			"surface_id":  event.SurfaceID,
			"output_name": event.OutputName,
			"width":       event.Width,
			"height":      event.Height,
			"scale":       event.Scale,
			"hash":        event.BufferHash,
			"monitor":     event.Monitor,
		})
	}
	return out
}

func summarizeRendererHashes(events []fakeWaylandEvent) []map[string]any {
	var hashes []map[string]any
	for _, event := range events {
		if event.Kind != "render" {
			continue
		}
		hashes = append(hashes, map[string]any{
			"surface_id":  event.SurfaceID,
			"output_name": event.OutputName,
			"width":       event.Width,
			"height":      event.Height,
			"hash":        event.BufferHash,
		})
	}
	return hashes
}

func summarizeOutputNames(waylandEvents []fakeWaylandEvent, pointerEvents []recordedPointerEvent) []string {
	seen := map[string]bool{}
	for _, event := range waylandEvents {
		if event.OutputName != "" {
			seen[event.OutputName] = true
		}
		if event.Monitor.Name != "" {
			seen[event.Monitor.Name] = true
		}
	}
	for _, event := range pointerEvents {
		if event.Motion.Position.OutputName != "" {
			seen[event.Motion.Position.OutputName] = true
		}
		if event.Button.Position.OutputName != "" {
			seen[event.Button.Position.OutputName] = true
		}
		if event.Frame.OutputName != "" {
			seen[event.Frame.OutputName] = true
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
