package main

import (
	"bytes"
	"context"
	"slices"
	"sync"
	"testing"
	"time"
)

type fixedOverlayRenderer struct{}

func (fixedOverlayRenderer) RenderMainGrid(monitor Monitor, gridSize int) (ARGBSnapshot, error) {
	pixels := make([]ARGBPixel, monitor.LogicalWidth*monitor.LogicalHeight)
	for i := range pixels {
		pixels[i] = StraightARGB(96, 74, 181, 255)
	}
	return NewARGBSnapshot(monitor.LogicalWidth, monitor.LogicalHeight, pixels)
}

type recordingCoordinateOverlayRenderer struct {
	mu            sync.Mutex
	states        []CoordinateRenderState
	monitors      []Monitor
	selectedCells []Rect
	wait          chan struct{}
}

func (r *recordingCoordinateOverlayRenderer) RenderMainGrid(monitor Monitor, gridSize int) (ARGBSnapshot, error) {
	return r.RenderCoordinateGrid(monitor, gridSize, CoordinateRenderState{})
}

func (r *recordingCoordinateOverlayRenderer) RenderCoordinateGrid(monitor Monitor, gridSize int, state CoordinateRenderState) (ARGBSnapshot, error) {
	r.mu.Lock()
	r.ensureWaitLocked()
	r.states = append(r.states, state)
	r.monitors = append(r.monitors, monitor)
	r.wakeLocked()
	r.mu.Unlock()
	pixels := make([]ARGBPixel, monitor.LogicalWidth*monitor.LogicalHeight)
	for i := range pixels {
		pixels[i] = StraightARGB(64, uint8(len(state.Input)*80), 120, 220)
	}
	return NewARGBSnapshot(monitor.LogicalWidth, monitor.LogicalHeight, pixels)
}

func (r *recordingCoordinateOverlayRenderer) RenderSelectedCellOutline(monitor Monitor, cell Rect) (ARGBSnapshot, error) {
	r.mu.Lock()
	r.ensureWaitLocked()
	r.selectedCells = append(r.selectedCells, cell)
	r.monitors = append(r.monitors, monitor)
	r.wakeLocked()
	r.mu.Unlock()
	pixels := make([]ARGBPixel, monitor.LogicalWidth*monitor.LogicalHeight)
	for i := range pixels {
		pixels[i] = StraightARGB(64, 200, 80, 220)
	}
	return NewARGBSnapshot(monitor.LogicalWidth, monitor.LogicalHeight, pixels)
}

func (r *recordingCoordinateOverlayRenderer) States() []CoordinateRenderState {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]CoordinateRenderState, len(r.states))
	copy(out, r.states)
	return out
}

func (r *recordingCoordinateOverlayRenderer) SelectedCells() []Rect {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Rect, len(r.selectedCells))
	copy(out, r.selectedCells)
	return out
}

func (r *recordingCoordinateOverlayRenderer) WaitForSelectedCellCount(ctx context.Context, count int) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		r.mu.Lock()
		r.ensureWaitLocked()
		if len(r.selectedCells) >= count {
			r.mu.Unlock()
			return nil
		}
		wait := r.wait
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wait:
		}
	}
}

func (r *recordingCoordinateOverlayRenderer) ensureWaitLocked() {
	if r.wait == nil {
		r.wait = make(chan struct{})
	}
}

func (r *recordingCoordinateOverlayRenderer) wakeLocked() {
	close(r.wait)
	r.wait = make(chan struct{})
}

func TestLayerShellOverlayDriverShowHideToggleAndReuseWithFakes(t *testing.T) {
	driver, wayland, _ := newTestLayerShellOverlayDriver(t, Monitor{
		Name:          "DP-1",
		LogicalWidth:  12,
		LogicalHeight: 8,
		Scale:         1,
	})
	controller := newDaemonController(driver, statusOutput{})

	response := controller.Dispatch(context.Background(), ipcRequest{Command: "show"})
	if !response.OK || !response.Active || response.Action != "shown" {
		t.Fatalf("show response = %+v, want shown active", response)
	}
	assertFakeWaylandEventKinds(t, wayland.Events(), "surface_create", "configure", "render", "keyboard_grab")

	response = controller.Dispatch(context.Background(), ipcRequest{Command: "show"})
	if !response.OK || response.Active || response.Action != "hidden" {
		t.Fatalf("show-toggle response = %+v, want hidden inactive", response)
	}
	assertFakeWaylandEventKinds(t, wayland.Events(), "unmap", "destroy")

	response = controller.Dispatch(context.Background(), ipcRequest{Command: "show"})
	if !response.OK || !response.Active || response.Action != "shown" {
		t.Fatalf("second show response = %+v, want shown active", response)
	}
	if got := wayland.LastSurface().id; got != 2 {
		t.Fatalf("second show surface id = %d, want 2", got)
	}

	response = controller.Dispatch(context.Background(), ipcRequest{Command: "hide"})
	if !response.OK || response.Active || response.Action != "hidden" {
		t.Fatalf("hide response = %+v, want hidden inactive", response)
	}
	assertFakeWaylandEventKinds(t, wayland.Events(), "unmap", "destroy")
}

func TestLayerShellOverlayDriverCoordinateEntryFSMHUDBackspaceSelectionAndReset(t *testing.T) {
	traceBytes := &lockedTraceBuffer{}
	trace := NewTraceRecorder(traceBytes, fixedTraceClock(time.Unix(30, 0)))
	monitor := Monitor{
		Name:          "DP-1",
		OriginX:       100,
		OriginY:       -50,
		LogicalWidth:  257,
		LogicalHeight: 193,
		Scale:         1,
	}
	wayland := newFakeWaylandBackend(trace)
	renderer := &recordingCoordinateOverlayRenderer{}
	driver, err := newLayerShellOverlayDriver(newFakeHyprlandIPC(monitor), wayland, renderer, DefaultConfig(), trace)
	if err != nil {
		t.Fatalf("newLayerShellOverlayDriver returned error: %v", err)
	}
	controller := newDaemonController(driver, statusOutput{})
	wayland.keyboard.Enqueue(
		KeyboardEvent{Kind: KeyboardEventKeymap, Keymap: &KeyboardKeymapFD{Data: []byte("keymap"), Size: 6}},
		KeyboardEvent{Kind: KeyboardEventEnter},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "m", State: KeyPressed},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "m", State: KeyReleased},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "/", State: KeyPressed},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "/", State: KeyReleased},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "BackSpace", State: KeyPressed},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "BackSpace", State: KeyReleased},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "M", State: KeyPressed, Modifiers: ModifierState{Shift: true}},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "M", State: KeyReleased, Modifiers: ModifierState{Shift: true}},
		KeyboardEvent{Kind: KeyboardEventModifiers, Modifiers: ModifierState{}},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "K", State: KeyPressed, Modifiers: ModifierState{Shift: true}},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "K", State: KeyReleased, Modifiers: ModifierState{Shift: true}},
	)

	response := controller.Dispatch(context.Background(), ipcRequest{Command: "show"})
	if !response.OK || !response.Active {
		t.Fatalf("show response = %+v, want active", response)
	}
	waitForCondition(t, func() bool { return len(renderer.States()) >= 4 && len(renderer.SelectedCells()) >= 1 })

	states := renderer.States()
	if len(states) != 4 {
		t.Fatalf("coordinate render states = %+v, want initial + M + empty + M only", states)
	}
	wantInputs := []string{"", "M", "", "M"}
	for i, want := range wantInputs {
		if states[i].Input != want {
			t.Fatalf("state[%d].Input = %q, want %q; states=%+v", i, states[i].Input, want, states)
		}
	}
	if states[1].HUDText() != "M_" || !states[1].HasSelectedColumn || states[1].SelectedColumn != 12 {
		t.Fatalf("first-letter render state = %+v, want selected column M and HUD M_", states[1])
	}
	if states[2].HUDText() != "" || states[2].HasSelectedColumn {
		t.Fatalf("backspace render state = %+v, want reset coordinate input", states[2])
	}

	events := decodeTraceEvents(t, traceBytes.String())
	selected := requireTraceEvent(t, events, traceCoordinateSelected)
	if selected.Fields["coordinate"] != "MK" || selected.Fields["column_letter"] != "M" || selected.Fields["row_letter"] != "K" {
		t.Fatalf("selected-cell trace fields = %+v, want MK/M/K", selected.Fields)
	}
	if got := int(selected.Fields["column"].(float64)); got != 12 {
		t.Fatalf("selected column = %d, want 12", got)
	}
	if got := int(selected.Fields["row"].(float64)); got != 10 {
		t.Fatalf("selected row = %d, want 10", got)
	}
	grid, err := NewGridGeometry(monitor, 26)
	if err != nil {
		t.Fatalf("NewGridGeometry returned error: %v", err)
	}
	wantBounds, err := grid.Cell(12, 10)
	if err != nil {
		t.Fatalf("Cell returned error: %v", err)
	}
	bounds := selected.Fields["bounds"].(map[string]any)
	if int(bounds["x"].(float64)) != wantBounds.X || int(bounds["y"].(float64)) != wantBounds.Y ||
		int(bounds["width"].(float64)) != wantBounds.Width || int(bounds["height"].(float64)) != wantBounds.Height {
		t.Fatalf("selected bounds = %+v, want %+v", bounds, wantBounds)
	}
	if selectedCells := renderer.SelectedCells(); len(selectedCells) != 1 || selectedCells[0] != wantBounds {
		t.Fatalf("selected outline cells = %+v, want [%+v]", selectedCells, wantBounds)
	}

	response = controller.Dispatch(context.Background(), ipcRequest{Command: "show"})
	if !response.OK || response.Active || response.Action != "hidden" {
		t.Fatalf("show-toggle response = %+v, want hidden inactive", response)
	}
	response = controller.Dispatch(context.Background(), ipcRequest{Command: "show"})
	if !response.OK || !response.Active || response.Action != "shown" {
		t.Fatalf("show after toggle response = %+v, want shown active", response)
	}
	waitForCondition(t, func() bool { return len(renderer.States()) >= 5 })
	if last := renderer.States()[4]; last.Input != "" || last.HasSelectedColumn {
		t.Fatalf("show after reset render state = %+v, want empty coordinate state", last)
	}
}

func TestLayerShellOverlayDriverSelectionMovesPointerAndRendersOnlyCellOutline(t *testing.T) {
	monitor := Monitor{Name: "DP-1", LogicalWidth: 260, LogicalHeight: 260, Scale: 1}
	driver, wayland, renderer, pointer, _ := newTestNavigationOverlayDriver(t, monitor)
	wayland.keyboard.Enqueue(
		KeyboardEvent{Kind: KeyboardEventKey, Key: "M", State: KeyPressed},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "M", State: KeyReleased},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "K", State: KeyPressed},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "K", State: KeyReleased},
	)
	controller := newDaemonController(driver, statusOutput{})

	response := controller.Dispatch(context.Background(), ipcRequest{Command: "show"})
	if !response.OK || !response.Active {
		t.Fatalf("show response = %+v, want active", response)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := pointer.WaitForEventCount(waitCtx, 2); err != nil {
		t.Fatalf("selected-cell pointer move was not emitted: %v", err)
	}
	if err := renderer.WaitForSelectedCellCount(waitCtx, 1); err != nil {
		t.Fatalf("selected-cell outline was not rendered: %v", err)
	}

	grid, err := NewGridGeometry(monitor, 26)
	if err != nil {
		t.Fatalf("NewGridGeometry returned error: %v", err)
	}
	cell, err := grid.Cell(12, 10)
	if err != nil {
		t.Fatalf("Cell returned error: %v", err)
	}
	centerX, centerY := cell.Center()
	motions := recordedMotionPositions(pointer.Events())
	if len(motions) != 1 || motions[0] != (PointerPosition{X: centerX, Y: centerY, OutputName: monitor.Name}) {
		t.Fatalf("selected-cell pointer motions = %+v, want center %.1f,%.1f on %s", motions, centerX, centerY, monitor.Name)
	}
	if selected := renderer.SelectedCells(); len(selected) != 1 || selected[0] != cell {
		t.Fatalf("selected outline cells = %+v, want [%+v]", selected, cell)
	}
	if states := renderer.States(); len(states) != 2 || states[0].Input != "" || states[1].Input != "M" {
		t.Fatalf("coordinate grid states after selection = %+v, want only main grid and first-letter column state", states)
	}
}

func TestLayerShellOverlayDriverHiddenSubcellMovementKeysMoveOutsideCell(t *testing.T) {
	monitor := Monitor{Name: "DP-1", LogicalWidth: 260, LogicalHeight: 260, Scale: 1}
	driver, wayland, _, pointer, _ := newTestNavigationOverlayDriver(t, monitor)
	wayland.keyboard.Enqueue(
		KeyboardEvent{Kind: KeyboardEventKey, Key: "M", State: KeyPressed},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "M", State: KeyReleased},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "K", State: KeyPressed},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "K", State: KeyReleased},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "L", State: KeyPressed},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "L", State: KeyReleased},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "Right", State: KeyPressed},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "Right", State: KeyReleased},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "h", State: KeyPressed},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "h", State: KeyReleased},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "Down", State: KeyPressed},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "Down", State: KeyReleased},
	)
	controller := newDaemonController(driver, statusOutput{})

	response := controller.Dispatch(context.Background(), ipcRequest{Command: "show"})
	if !response.OK || !response.Active {
		t.Fatalf("show response = %+v, want active", response)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := pointer.WaitForEventCount(waitCtx, 10); err != nil {
		t.Fatalf("hidden movement pointer targets were not emitted: %v", err)
	}

	motions := recordedMotionPositions(pointer.Events())
	want := []PointerPosition{
		{X: 125, Y: 105, OutputName: monitor.Name},
		{X: 130, Y: 105, OutputName: monitor.Name},
		{X: 135, Y: 105, OutputName: monitor.Name},
		{X: 130, Y: 105, OutputName: monitor.Name},
		{X: 130, Y: 110, OutputName: monitor.Name},
	}
	if !slices.Equal(motions, want) {
		t.Fatalf("hidden movement motions = %+v, want %+v", motions, want)
	}
	grid, err := NewGridGeometry(monitor, 26)
	if err != nil {
		t.Fatalf("NewGridGeometry returned error: %v", err)
	}
	cell, err := grid.Cell(12, 10)
	if err != nil {
		t.Fatalf("Cell returned error: %v", err)
	}
	if motions[2].X <= float64(cell.X+cell.Width) {
		t.Fatalf("Right arrow did not move outside selected cell: motion=%+v cell=%+v", motions[2], cell)
	}
}

func TestLayerShellOverlayDriverHeldDirectionRepeatDelayAccelerationStopAndCancel(t *testing.T) {
	monitor := Monitor{Name: "DP-1", LogicalWidth: 260, LogicalHeight: 260, Scale: 1}
	driver, wayland, _, pointer, clock := newTestNavigationOverlayDriver(t, monitor)
	wayland.keyboard.Enqueue(
		KeyboardEvent{Kind: KeyboardEventKey, Key: "M", State: KeyPressed},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "M", State: KeyReleased},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "K", State: KeyPressed},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "K", State: KeyReleased},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "L", State: KeyPressed},
	)
	controller := newDaemonController(driver, statusOutput{})

	response := controller.Dispatch(context.Background(), ipcRequest{Command: "show"})
	if !response.OK || !response.Active {
		t.Fatalf("show response = %+v, want active", response)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := pointer.WaitForEventCount(waitCtx, 4); err != nil {
		t.Fatalf("initial selected/immediate pointer targets were not emitted: %v", err)
	}
	if len(recordedMotionPositions(pointer.Events())) != 2 {
		t.Fatalf("unexpected motions before repeat delay: %+v", recordedMotionPositions(pointer.Events()))
	}

	clock.Advance(349 * time.Millisecond)
	if motions := recordedMotionPositions(pointer.Events()); len(motions) != 2 {
		t.Fatalf("repeat fired before initial delay: %+v", motions)
	}
	clock.Advance(time.Millisecond)
	if err := pointer.WaitForEventCount(waitCtx, 6); err != nil {
		t.Fatalf("first repeat tick was not emitted after delay: %v", err)
	}
	clock.Advance(50 * time.Millisecond)
	if err := pointer.WaitForEventCount(waitCtx, 8); err != nil {
		t.Fatalf("base repeat tick was not emitted: %v", err)
	}
	clock.Advance(450 * time.Millisecond)
	if err := pointer.WaitForEventCount(waitCtx, 10); err != nil {
		t.Fatalf("accelerated repeat tick was not emitted: %v", err)
	}
	motions := recordedMotionPositions(pointer.Events())
	if dx1, dx2, dx3 := motions[2].X-motions[1].X, motions[3].X-motions[2].X, motions[4].X-motions[3].X; dx1 != 5 || dx2 != 5 || dx3 != 10 {
		t.Fatalf("repeat deltas = %.1f, %.1f, %.1f; want 5, 5, 10", dx1, dx2, dx3)
	}

	wayland.keyboard.Enqueue(KeyboardEvent{Kind: KeyboardEventKey, Key: "L", State: KeyReleased})
	if err := wayland.keyboard.WaitForPendingEvents(waitCtx, 0); err != nil {
		t.Fatalf("L release was not consumed: %v", err)
	}
	afterRelease := len(pointer.Events())
	clock.Advance(time.Second)
	if got := len(pointer.Events()); got != afterRelease {
		t.Fatalf("repeat emitted after key release: before=%d after=%d", afterRelease, got)
	}

	wayland.keyboard.Enqueue(KeyboardEvent{Kind: KeyboardEventKey, Key: "Right", State: KeyPressed})
	if err := pointer.WaitForEventCount(waitCtx, afterRelease+2); err != nil {
		t.Fatalf("new direction immediate target was not emitted: %v", err)
	}
	afterRightPress := len(pointer.Events())
	wayland.keyboard.Enqueue(KeyboardEvent{Kind: KeyboardEventKey, Key: "K", State: KeyPressed})
	if err := pointer.WaitForEventCount(waitCtx, afterRightPress+2); err != nil {
		t.Fatalf("different direction immediate target was not emitted: %v", err)
	}
	clock.Advance(349 * time.Millisecond)
	if got := len(pointer.Events()); got != afterRightPress+2 {
		t.Fatalf("new direction repeat fired before delay or old repeat was not canceled: got=%d want=%d", got, afterRightPress+2)
	}
	clock.Advance(time.Millisecond)
	if err := pointer.WaitForEventCount(waitCtx, afterRightPress+4); err != nil {
		t.Fatalf("new direction repeat did not start after fresh delay: %v", err)
	}
	motions = recordedMotionPositions(pointer.Events())
	last := motions[len(motions)-1]
	prev := motions[len(motions)-2]
	if last.Y >= prev.Y {
		t.Fatalf("fresh K repeat did not move upward: previous=%+v last=%+v", prev, last)
	}
}

func TestLayerShellOverlayDriverIgnoresCompositorRepeatForDirectionKeys(t *testing.T) {
	monitor := Monitor{Name: "DP-1", LogicalWidth: 260, LogicalHeight: 260, Scale: 1}
	driver, wayland, _, pointer, _ := newTestNavigationOverlayDriver(t, monitor)
	wayland.keyboard.Enqueue(
		KeyboardEvent{Kind: KeyboardEventKey, Key: "M", State: KeyPressed},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "M", State: KeyReleased},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "K", State: KeyPressed},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "K", State: KeyReleased},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "L", State: KeyPressed},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "L", State: KeyPressed},
	)
	controller := newDaemonController(driver, statusOutput{})

	response := controller.Dispatch(context.Background(), ipcRequest{Command: "show"})
	if !response.OK || !response.Active {
		t.Fatalf("show response = %+v, want active", response)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := pointer.WaitForEventCount(waitCtx, 4); err != nil {
		t.Fatalf("initial selected/immediate pointer targets were not emitted: %v", err)
	}
	if err := wayland.keyboard.WaitForPendingEvents(waitCtx, 0); err != nil {
		t.Fatalf("keyboard repeat event was not consumed: %v", err)
	}
	if motions := recordedMotionPositions(pointer.Events()); len(motions) != 2 {
		t.Fatalf("compositor repeated direction emitted duplicate immediate movement: %+v", motions)
	}
}

func TestLayerShellOverlayDriverSpaceTimeoutLeftClickAndStayActive(t *testing.T) {
	monitor := Monitor{Name: "DP-1", LogicalWidth: 260, LogicalHeight: 260, Scale: 1}
	driver, wayland, _, pointer, clock, traceBytes := newTestClickOverlayDriver(t, monitor, func(config *Config) {
		config.Behavior.StayActive = true
		config.Behavior.DoubleClickTimeoutMS = 250
	})
	wayland.keyboard.Enqueue(
		KeyboardEvent{Kind: KeyboardEventKey, Key: "M", State: KeyPressed},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "M", State: KeyReleased},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "K", State: KeyPressed},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "K", State: KeyReleased},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "space", State: KeyPressed},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "space", State: KeyReleased},
	)
	controller := newDaemonController(driver, statusOutput{})

	response := controller.Dispatch(context.Background(), ipcRequest{Command: "show"})
	if !response.OK || !response.Active {
		t.Fatalf("show response = %+v, want active", response)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := pointer.WaitForEventCount(waitCtx, 2); err != nil {
		t.Fatalf("selected-cell pointer move was not emitted: %v", err)
	}
	if err := wayland.keyboard.WaitForPendingEvents(waitCtx, 0); err != nil {
		t.Fatalf("space press was not consumed: %v", err)
	}
	if clock.TimerCount() != 1 || clock.ActiveTimerCount() != 1 {
		t.Fatalf("double-click timer state = total %d active %d, want one active timer", clock.TimerCount(), clock.ActiveTimerCount())
	}
	if buttons := recordedButtonEvents(pointer.Events()); len(buttons) != 0 {
		t.Fatalf("left click fired before timeout: %+v", buttons)
	}

	clock.Advance(249 * time.Millisecond)
	if buttons := recordedButtonEvents(pointer.Events()); len(buttons) != 0 {
		t.Fatalf("left click fired before configured timeout: %+v", buttons)
	}
	clock.Advance(time.Millisecond)
	if err := pointer.WaitForEventCount(waitCtx, 5); err != nil {
		t.Fatalf("left click was not emitted after timeout: %v", err)
	}
	if err := wayland.keyboard.WaitForShowCount(waitCtx, 2); err != nil {
		t.Fatalf("stay-active grid was not recreated: %v", err)
	}
	if clock.ActiveTimerCount() != 0 {
		t.Fatalf("double-click timer still active after click: %d", clock.ActiveTimerCount())
	}
	motions := recordedMotionPositions(pointer.Events())
	buttons := recordedButtonEvents(pointer.Events())
	assertButtonClick(t, buttons, PointerButtonLeft, 1, motions[0])

	traceEvents := decodeTraceEvents(t, traceBytes.String())
	assertOverlayUnmapBeforePointerButtons(t, traceEvents)
	assertStayActiveResetAfterClickComplete(t, traceEvents)
}

func TestLayerShellOverlayDriverSpaceSpaceDoubleClickBeforeTimeout(t *testing.T) {
	monitor := Monitor{Name: "DP-1", LogicalWidth: 260, LogicalHeight: 260, Scale: 1}
	driver, wayland, _, pointer, clock, traceBytes := newTestClickOverlayDriver(t, monitor, func(config *Config) {
		config.Behavior.StayActive = true
		config.Behavior.DoubleClickTimeoutMS = 250
	})
	wayland.keyboard.Enqueue(
		KeyboardEvent{Kind: KeyboardEventKey, Key: "M", State: KeyPressed},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "M", State: KeyReleased},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "K", State: KeyPressed},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "K", State: KeyReleased},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "space", State: KeyPressed},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "space", State: KeyReleased},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "space", State: KeyPressed},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "space", State: KeyReleased},
	)
	controller := newDaemonController(driver, statusOutput{})

	response := controller.Dispatch(context.Background(), ipcRequest{Command: "show"})
	if !response.OK || !response.Active {
		t.Fatalf("show response = %+v, want active", response)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := pointer.WaitForEventCount(waitCtx, 8); err != nil {
		t.Fatalf("double click was not emitted: %v", err)
	}
	if err := wayland.keyboard.WaitForShowCount(waitCtx, 2); err != nil {
		t.Fatalf("stay-active grid was not recreated after double click: %v", err)
	}
	if clock.TimerCount() != 1 || clock.ActiveTimerCount() != 0 {
		t.Fatalf("double-click timer state after completion = total %d active %d, want one stopped timer", clock.TimerCount(), clock.ActiveTimerCount())
	}
	motions := recordedMotionPositions(pointer.Events())
	buttons := recordedButtonEvents(pointer.Events())
	assertButtonClick(t, buttons, PointerButtonLeft, 2, motions[0])

	clock.Advance(time.Second)
	if got := len(recordedButtonEvents(pointer.Events())); got != 4 {
		t.Fatalf("stale double-click timer emitted extra buttons: got %d want 4", got)
	}

	traceEvents := decodeTraceEvents(t, traceBytes.String())
	assertOverlayUnmapBeforePointerButtons(t, traceEvents)
	assertNoOverlayRenderBetweenPointerButtons(t, traceEvents)
	assertStayActiveResetAfterClickComplete(t, traceEvents)
}

func TestLayerShellOverlayDriverShiftSpaceRightClickNoLeftTimerAndStayInactive(t *testing.T) {
	monitor := Monitor{Name: "DP-1", LogicalWidth: 260, LogicalHeight: 260, Scale: 1}
	driver, wayland, _, pointer, clock, traceBytes := newTestClickOverlayDriver(t, monitor, func(config *Config) {
		config.Behavior.StayActive = false
	})
	wayland.keyboard.Enqueue(
		KeyboardEvent{Kind: KeyboardEventKey, Key: "M", State: KeyPressed},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "M", State: KeyReleased},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "K", State: KeyPressed},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "K", State: KeyReleased},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "space", State: KeyPressed, Modifiers: ModifierState{Shift: true}},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "space", State: KeyReleased, Modifiers: ModifierState{Shift: true}},
	)
	controller := newDaemonController(driver, statusOutput{})

	response := controller.Dispatch(context.Background(), ipcRequest{Command: "show"})
	if !response.OK || !response.Active {
		t.Fatalf("show response = %+v, want active", response)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := pointer.WaitForEventCount(waitCtx, 5); err != nil {
		t.Fatalf("right click was not emitted: %v", err)
	}
	if clock.TimerCount() != 0 || clock.ActiveTimerCount() != 0 {
		t.Fatalf("Shift-space created left-click timer: total %d active %d", clock.TimerCount(), clock.ActiveTimerCount())
	}
	if driver.OverlayActive() {
		t.Fatal("driver stayed active with behavior.stay_active=false")
	}
	if got := wayland.keyboard.ShowCount(); got != 1 {
		t.Fatalf("show count after stay_active=false click = %d, want 1", got)
	}
	motions := recordedMotionPositions(pointer.Events())
	buttons := recordedButtonEvents(pointer.Events())
	assertButtonClick(t, buttons, PointerButtonRight, 1, motions[0])

	traceEvents := decodeTraceEvents(t, traceBytes.String())
	assertOverlayUnmapBeforePointerButtons(t, traceEvents)
	assertNoTraceEvent(t, traceEvents, traceStayActiveReset)
}

func TestLayerShellOverlayDriverEscapeCancelsPendingClickAndOverridesStayActive(t *testing.T) {
	monitor := Monitor{Name: "DP-1", LogicalWidth: 260, LogicalHeight: 260, Scale: 1}
	driver, wayland, _, pointer, clock, traceBytes := newTestClickOverlayDriver(t, monitor, func(config *Config) {
		config.Behavior.StayActive = true
		config.Behavior.DoubleClickTimeoutMS = 250
	})
	wayland.keyboard.Enqueue(
		KeyboardEvent{Kind: KeyboardEventKey, Key: "M", State: KeyPressed},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "M", State: KeyReleased},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "K", State: KeyPressed},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "K", State: KeyReleased},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "space", State: KeyPressed},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "space", State: KeyReleased},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "Escape", State: KeyPressed},
	)
	controller := newDaemonController(driver, statusOutput{})

	response := controller.Dispatch(context.Background(), ipcRequest{Command: "show"})
	if !response.OK || !response.Active {
		t.Fatalf("show response = %+v, want active", response)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := wayland.keyboard.WaitForPendingEvents(waitCtx, 0); err != nil {
		t.Fatalf("keyboard events were not consumed: %v", err)
	}
	if err := wayland.WaitForEventCount(waitCtx, 10); err != nil {
		t.Fatalf("Escape did not tear down the overlay: %v", err)
	}
	if driver.OverlayActive() {
		t.Fatal("driver stayed active after Escape")
	}
	if clock.TimerCount() != 1 || clock.ActiveTimerCount() != 0 {
		t.Fatalf("pending click timer state after Escape = total %d active %d, want one stopped timer", clock.TimerCount(), clock.ActiveTimerCount())
	}

	clock.Advance(time.Second)
	if got := len(recordedButtonEvents(pointer.Events())); got != 0 {
		t.Fatalf("stale pending click emitted %d button events after Escape", got)
	}
	if got := wayland.keyboard.ShowCount(); got != 1 {
		t.Fatalf("Escape recreated stay-active grid: show count = %d want 1", got)
	}
	traceEvents := decodeTraceEvents(t, traceBytes.String())
	assertNoTraceEvent(t, traceEvents, tracePointerButton)
	assertNoTraceEvent(t, traceEvents, traceStayActiveReset)
}

func TestLayerShellOverlayDriverEscapeDestroysSurfaceAndResetsController(t *testing.T) {
	driver, wayland, _ := newTestLayerShellOverlayDriver(t, Monitor{
		Name:          "DP-1",
		LogicalWidth:  10,
		LogicalHeight: 6,
		Scale:         1,
	})
	wayland.keyboard.Enqueue(KeyboardEvent{Kind: KeyboardEventKey, Key: "Escape", State: KeyPressed})
	controller := newDaemonController(driver, statusOutput{})

	response := controller.Dispatch(context.Background(), ipcRequest{Command: "show"})
	if !response.OK || !response.Active {
		t.Fatalf("show response = %+v, want active", response)
	}
	waitForCondition(t, func() bool { return !driver.OverlayActive() })

	status := controller.Dispatch(context.Background(), ipcRequest{Command: "status"})
	if !status.OK || status.Active {
		t.Fatalf("status after Escape = %+v, want inactive", status)
	}
	assertFakeWaylandEventKinds(t, wayland.Events(), "unmap", "destroy")
}

func TestLayerShellOverlayDriverRecoversAfterCompositorClose(t *testing.T) {
	driver, wayland, _ := newTestLayerShellOverlayDriver(t, Monitor{
		Name:          "DP-1",
		LogicalWidth:  10,
		LogicalHeight: 6,
		Scale:         1,
	})
	controller := newDaemonController(driver, statusOutput{})

	response := controller.Dispatch(context.Background(), ipcRequest{Command: "show"})
	if !response.OK || !response.Active {
		t.Fatalf("show response = %+v, want active", response)
	}
	firstSurface := wayland.LastSurface()
	firstSurface.EnqueueLifecycle(OverlayLifecycleEvent{Kind: OverlayLifecycleCompositorClose})
	waitForCondition(t, func() bool { return !driver.OverlayActive() })

	status := controller.Dispatch(context.Background(), ipcRequest{Command: "status"})
	if !status.OK || status.Active {
		t.Fatalf("status after compositor close = %+v, want inactive", status)
	}

	response = controller.Dispatch(context.Background(), ipcRequest{Command: "show"})
	if !response.OK || !response.Active || response.Action != "shown" {
		t.Fatalf("show after compositor close = %+v, want shown active", response)
	}
	if got := wayland.LastSurface().id; got != firstSurface.id+1 {
		t.Fatalf("surface id after compositor close = %d, want %d", got, firstSurface.id+1)
	}
}

func TestLayerShellOverlayDriverLifecycleConfigureReleaseOutputChangeAndDestroy(t *testing.T) {
	driver, wayland, traceBytes := newTestLayerShellOverlayDriver(t, Monitor{
		Name:          "DP-1",
		LogicalWidth:  10,
		LogicalHeight: 6,
		Scale:         1,
	})
	controller := newDaemonController(driver, statusOutput{})

	response := controller.Dispatch(context.Background(), ipcRequest{Command: "show"})
	if !response.OK || !response.Active {
		t.Fatalf("show response = %+v, want active", response)
	}
	surface := wayland.LastSurface()
	surface.EnqueueLifecycle(
		OverlayLifecycleEvent{Kind: OverlayLifecycleConfigure, Width: 14, Height: 9, Scale: 2},
		OverlayLifecycleEvent{Kind: OverlayLifecycleRelease, Width: 14, Height: 9},
		OverlayLifecycleEvent{Kind: OverlayLifecycleOutputChange, Monitor: Monitor{Name: "HDMI-A-1", LogicalWidth: 16, LogicalHeight: 10, Scale: 1.5}},
		OverlayLifecycleEvent{Kind: OverlayLifecycleDestroy},
	)
	waitForCondition(t, func() bool { return !driver.OverlayActive() })

	events := wayland.Events()
	assertFakeWaylandEventKinds(t, events, "configure", "render", "output_change", "destroy")
	if !hasFakeRenderSize(events, 14, 9) {
		t.Fatalf("configure lifecycle did not recreate a 14x9 buffer: %+v", events)
	}
	if !hasFakeRenderSize(events, 16, 10) {
		t.Fatalf("output-change lifecycle did not recreate a 16x10 buffer: %+v", events)
	}

	traceEvents := decodeTraceEvents(t, traceBytes.String())
	assertTraceContains(t, traceEvents, traceOverlaySurfaceCreate, traceOverlayConfigure, traceOverlayRender, traceOverlayKeyboardGrab, traceOverlayOutputChange, traceOverlayDestroy)
}

func TestOverlayEventQueueDeliversLifecycleAndKeyboardCriticalEventsInOrder(t *testing.T) {
	keyboard := newOverlayEventQueue[KeyboardEvent]()
	keyboardEvents := []KeyboardEvent{
		{Kind: KeyboardEventKeymap, Keymap: &KeyboardKeymapFD{Data: []byte("keymap"), Size: 6}},
		{Kind: KeyboardEventEnter},
		{Kind: KeyboardEventKey, Key: "Escape", State: KeyPressed},
		{Kind: KeyboardEventDestroy},
	}
	for _, event := range keyboardEvents {
		keyboard.push(event)
	}
	for i, want := range keyboardEvents {
		got, err := keyboard.pop(context.Background(), nil)
		if err != nil {
			t.Fatalf("keyboard pop[%d] returned error: %v", i, err)
		}
		if got.Kind != want.Kind || got.Key != want.Key {
			t.Fatalf("keyboard pop[%d] = %+v, want %+v", i, got, want)
		}
	}

	lifecycle := newOverlayEventQueue[OverlayLifecycleEvent]()
	lifecycle.fatal(assertiveFatalError("wayland protocol error: invalid_keyboard_interactivity"))
	_, err := lifecycle.pop(context.Background(), nil)
	if err == nil || err.Error() != "wayland protocol error: invalid_keyboard_interactivity" {
		t.Fatalf("fatal lifecycle pop error = %v, want clear fatal error", err)
	}
}

type assertiveFatalError string

func (e assertiveFatalError) Error() string {
	return string(e)
}

func newTestLayerShellOverlayDriver(t *testing.T, monitor Monitor) (*layerShellOverlayDriver, *fakeWaylandBackend, *lockedTraceBuffer) {
	t.Helper()
	traceBytes := &lockedTraceBuffer{}
	trace := NewTraceRecorder(traceBytes, fixedTraceClock(time.Unix(20, 0)))
	config := DefaultConfig()
	config.Grid.Size = 2
	if err := config.Validate(); err != nil {
		t.Fatalf("test config Validate returned error: %v", err)
	}
	wayland := newFakeWaylandBackend(trace)
	driver, err := newLayerShellOverlayDriver(newFakeHyprlandIPC(monitor), wayland, fixedOverlayRenderer{}, config, trace)
	if err != nil {
		t.Fatalf("newLayerShellOverlayDriver returned error: %v", err)
	}
	return driver, wayland, traceBytes
}

func newTestNavigationOverlayDriver(t *testing.T, monitor Monitor) (*layerShellOverlayDriver, *fakeWaylandBackend, *recordingCoordinateOverlayRenderer, *pointerRecorder, *fakeClock) {
	t.Helper()
	trace := NewTraceRecorder(nil, nil)
	config := DefaultConfig()
	config.Grid.Size = 26
	config.Grid.SubgridPixelSize = 5
	if err := config.Validate(); err != nil {
		t.Fatalf("test config Validate returned error: %v", err)
	}
	clock := newFakeClock(time.Unix(100, 0), trace)
	pointer := newPointerRecorder(trace)
	pointer.now = clock.Now
	wayland := newFakeWaylandBackend(trace)
	wayland.keyboard.SetBlocking(true)
	renderer := &recordingCoordinateOverlayRenderer{}
	driver, err := newLayerShellOverlayDriverWithOptions(newFakeHyprlandIPC(monitor), wayland, renderer, config, trace, layerShellOverlayDriverOptions{
		Pointer: pointer,
		Clock:   clock,
	})
	if err != nil {
		t.Fatalf("newLayerShellOverlayDriverWithOptions returned error: %v", err)
	}
	return driver, wayland, renderer, pointer, clock
}

func newTestClickOverlayDriver(t *testing.T, monitor Monitor, configure func(*Config)) (*layerShellOverlayDriver, *fakeWaylandBackend, *recordingCoordinateOverlayRenderer, *pointerRecorder, *fakeClock, *lockedTraceBuffer) {
	t.Helper()
	traceBytes := &lockedTraceBuffer{}
	clock := newFakeClock(time.Unix(200, 0), nil)
	trace := NewTraceRecorder(traceBytes, clock.Now)
	clock.trace = trace
	config := DefaultConfig()
	config.Grid.Size = 26
	config.Grid.SubgridPixelSize = 5
	if configure != nil {
		configure(&config)
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("test config Validate returned error: %v", err)
	}
	pointer := newPointerRecorder(trace)
	pointer.now = clock.Now
	wayland := newFakeWaylandBackend(trace)
	wayland.keyboard.SetBlocking(true)
	renderer := &recordingCoordinateOverlayRenderer{}
	driver, err := newLayerShellOverlayDriverWithOptions(newFakeHyprlandIPC(monitor), wayland, renderer, config, trace, layerShellOverlayDriverOptions{
		Pointer: pointer,
		Clock:   clock,
	})
	if err != nil {
		t.Fatalf("newLayerShellOverlayDriverWithOptions returned error: %v", err)
	}
	return driver, wayland, renderer, pointer, clock, traceBytes
}

func recordedMotionPositions(events []recordedPointerEvent) []PointerPosition {
	var positions []PointerPosition
	for _, event := range events {
		if event.Kind == "motion" {
			positions = append(positions, event.Motion.Position)
		}
	}
	return positions
}

func recordedButtonEvents(events []recordedPointerEvent) []PointerButtonEvent {
	var buttons []PointerButtonEvent
	for _, event := range events {
		if event.Kind == "button" {
			buttons = append(buttons, event.Button)
		}
	}
	return buttons
}

func assertButtonClick(t *testing.T, buttons []PointerButtonEvent, button PointerButton, clickCount int, position PointerPosition) {
	t.Helper()
	if len(buttons) != clickCount*2 {
		t.Fatalf("button event count = %d, want %d for %d click(s): %+v", len(buttons), clickCount*2, clickCount, buttons)
	}
	clickGroup := buttons[0].ClickGroup
	for i, event := range buttons {
		wantState := PointerButtonDown
		if i%2 == 1 {
			wantState = PointerButtonUp
		}
		wantSequence := i/2 + 1
		if event.Button != button || event.State != wantState || event.ClickGroup != clickGroup || event.ClickCount != clickCount || event.Sequence != wantSequence {
			t.Fatalf("button event[%d] = %+v, want %s %s group %d count %d sequence %d", i, event, button, wantState, clickGroup, clickCount, wantSequence)
		}
		if event.Position != position {
			t.Fatalf("button event[%d] position = %+v, want committed position %+v", i, event.Position, position)
		}
	}
}

type lockedTraceBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedTraceBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedTraceBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func assertFakeWaylandEventKinds(t *testing.T, events []fakeWaylandEvent, wants ...string) {
	t.Helper()
	var kinds []string
	for _, event := range events {
		kinds = append(kinds, event.Kind)
	}
	for _, want := range wants {
		if !slices.Contains(kinds, want) {
			t.Fatalf("Wayland events missing %q: %v", want, kinds)
		}
	}
}

func hasFakeRenderSize(events []fakeWaylandEvent, width, height int) bool {
	for _, event := range events {
		if event.Kind == "render" && event.Width == width && event.Height == height {
			return true
		}
	}
	return false
}

func waitForCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not satisfied before timeout")
}

func requireTraceEvent(t *testing.T, events []TraceEvent, want string) TraceEvent {
	t.Helper()
	for _, event := range events {
		if event.Event == want {
			return event
		}
	}
	seen := make(map[string]bool)
	for _, event := range events {
		seen[event.Event] = true
	}
	t.Fatalf("trace missing %q; saw %v", want, sortedKeys(seen))
	return TraceEvent{}
}

func assertOverlayUnmapBeforePointerButtons(t *testing.T, events []TraceEvent) {
	t.Helper()
	unmapped := false
	for _, event := range events {
		if event.Event == traceOverlayUnmap {
			unmapped = true
		}
		if event.Event == tracePointerButton && !unmapped {
			t.Fatalf("pointer button trace occurred before overlay unmap: seq=%d fields=%+v", event.Seq, event.Fields)
		}
	}
}

func assertStayActiveResetAfterClickComplete(t *testing.T, events []TraceEvent) {
	t.Helper()
	completeSeq := uint64(0)
	resetSeq := uint64(0)
	for _, event := range events {
		if event.Event == traceClickGroupComplete && completeSeq == 0 {
			completeSeq = event.Seq
		}
		if event.Event == traceStayActiveReset && resetSeq == 0 {
			resetSeq = event.Seq
		}
	}
	if completeSeq == 0 || resetSeq == 0 {
		t.Fatalf("missing click completion or stay-active reset trace: complete=%d reset=%d", completeSeq, resetSeq)
	}
	if resetSeq <= completeSeq {
		t.Fatalf("stay-active reset seq=%d occurred before click completion seq=%d", resetSeq, completeSeq)
	}
}

func assertNoOverlayRenderBetweenPointerButtons(t *testing.T, events []TraceEvent) {
	t.Helper()
	firstButtonSeq := uint64(0)
	lastButtonSeq := uint64(0)
	for _, event := range events {
		if event.Event != tracePointerButton {
			continue
		}
		if firstButtonSeq == 0 {
			firstButtonSeq = event.Seq
		}
		lastButtonSeq = event.Seq
	}
	if firstButtonSeq == 0 || lastButtonSeq == 0 {
		t.Fatal("no pointer button traces recorded")
	}
	for _, event := range events {
		if event.Seq <= firstButtonSeq || event.Seq >= lastButtonSeq {
			continue
		}
		if event.Event == traceOverlayRender || event.Event == traceOverlaySurfaceCreate || event.Event == traceOverlayKeyboardGrab {
			t.Fatalf("overlay reopened during click sequence at seq=%d event=%s", event.Seq, event.Event)
		}
	}
}

func assertNoTraceEvent(t *testing.T, events []TraceEvent, unwanted string) {
	t.Helper()
	for _, event := range events {
		if event.Event == unwanted {
			t.Fatalf("unexpected trace event %q at seq=%d fields=%+v", unwanted, event.Seq, event.Fields)
		}
	}
}
