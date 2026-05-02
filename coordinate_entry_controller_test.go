package main

import (
	"context"
	"testing"
	"time"
)

func TestDaemonKeyboardCoordinateEntryUpdatesGridAndEmitsSelection(t *testing.T) {
	ctx := context.Background()
	config := DefaultConfig()
	atlas, err := NewFontAtlasFromConfig(config)
	if err != nil {
		t.Fatalf("font atlas: %v", err)
	}
	focused := Monitor{Name: "eDP-1", Width: 520, Height: 520, Scale: 1, Focused: true}
	wayland := newFakeWaylandBackend(focused)
	renderer := &fakeRendererSink{}
	pointer := newVirtualPointerRecorder(newFakeClock(time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)))
	keyboard := newFakeKeyboardEventSource(8)
	selected := make(chan MainCoordinateSelectedEvent, 1)
	controller := NewDaemonController(DaemonDeps{
		MonitorLookup: &fakeFocusedMonitorLookup{monitor: focused},
		Overlay:       wayland,
		Renderer:      renderer,
		Keyboard:      keyboard,
		Pointer:       pointer,
		Config:        &config,
		FontAtlas:     atlas,
		MainCoordinateSelected: func(_ context.Context, event MainCoordinateSelectedEvent) error {
			selected <- event
			return nil
		},
	})
	if err := controller.Show(ctx); err != nil {
		t.Fatalf("show overlay: %v", err)
	}

	keyboard.Send(KeyboardEvent{Key: "M", Pressed: true})
	waitForRendererPresentationCount(t, renderer, 2)
	assertLastRendererHashMatchesMainGrid(t, renderer, focused, config, atlas, "M_", ptrToInt(12))

	beforeInvalid := len(renderer.Presentations())
	keyboard.Send(KeyboardEvent{Key: "Delete", Pressed: true})
	assertRendererPresentationCountStays(t, renderer, beforeInvalid)

	keyboard.Send(KeyboardEvent{Key: "BackSpace", Pressed: true})
	waitForRendererPresentationCount(t, renderer, 3)
	assertLastRendererHashMatchesMainGrid(t, renderer, focused, config, atlas, DefaultMainGridHUD, nil)

	keyboard.Send(KeyboardEvent{Key: "M", Pressed: true})
	waitForRendererPresentationCount(t, renderer, 4)
	keyboard.Send(KeyboardEvent{Key: "K", Pressed: true})
	got := receiveMainCoordinateSelection(t, selected)
	if got.Column != 12 || got.Row != 10 || got.ColumnLetter != 'M' || got.RowLetter != 'K' {
		t.Fatalf("selection event = %+v, want M/K column/row", got)
	}
	wantBounds, err := GridCellBounds(focused, config.Grid.Size, 12, 10)
	if err != nil {
		t.Fatalf("expected grid bounds: %v", err)
	}
	if got.Bounds != wantBounds || got.Center != wantBounds.Center() {
		t.Fatalf("selection geometry = bounds %+v center %+v, want bounds %+v center %+v", got.Bounds, got.Center, wantBounds, wantBounds.Center())
	}
	assertLastPointerMotion(t, pointer, focused, wantBounds.Center())
	waitForRendererPresentationCount(t, renderer, 5)
	assertLastRendererHashMatchesSelectedCell(t, renderer, focused, config, wantBounds)
}

func TestDaemonHideAndShowToggleResetCoordinateEntry(t *testing.T) {
	ctx := context.Background()
	config := DefaultConfig()
	atlas, err := NewFontAtlasFromConfig(config)
	if err != nil {
		t.Fatalf("font atlas: %v", err)
	}
	focused := Monitor{Name: "eDP-1", Width: 520, Height: 520, Scale: 1, Focused: true}
	wayland := newFakeWaylandBackend(focused)
	renderer := &fakeRendererSink{}
	controller := NewDaemonController(DaemonDeps{
		MonitorLookup: &fakeFocusedMonitorLookup{monitor: focused},
		Overlay:       wayland,
		Renderer:      renderer,
		Config:        &config,
		FontAtlas:     atlas,
	})
	if err := controller.Show(ctx); err != nil {
		t.Fatalf("show overlay: %v", err)
	}
	if err := controller.HandleKeyboardToken(ctx, KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'M'}); err != nil {
		t.Fatalf("handle first letter: %v", err)
	}
	assertLastRendererHashMatchesMainGrid(t, renderer, focused, config, atlas, "M_", ptrToInt(12))

	if err := controller.Show(ctx); err != nil {
		t.Fatalf("show toggle off: %v", err)
	}
	if controller.State() != DaemonStateInactive {
		t.Fatalf("state after show toggle = %q, want inactive", controller.State())
	}
	if err := controller.Show(ctx); err != nil {
		t.Fatalf("show after toggle off: %v", err)
	}
	assertLastRendererHashMatchesMainGrid(t, renderer, focused, config, atlas, DefaultMainGridHUD, nil)

	if err := controller.HandleKeyboardToken(ctx, KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'M'}); err != nil {
		t.Fatalf("handle first letter after re-show: %v", err)
	}
	assertLastRendererHashMatchesMainGrid(t, renderer, focused, config, atlas, "M_", ptrToInt(12))
	if err := controller.Hide(ctx); err != nil {
		t.Fatalf("hide overlay: %v", err)
	}
	if err := controller.Show(ctx); err != nil {
		t.Fatalf("show after hide: %v", err)
	}
	assertLastRendererHashMatchesMainGrid(t, renderer, focused, config, atlas, DefaultMainGridHUD, nil)
}

func TestDaemonHiddenSubgridVimKeysMovePointerAndIgnoreOtherLetters(t *testing.T) {
	ctx := context.Background()
	config := DefaultConfig()
	atlas, err := NewFontAtlasFromConfig(config)
	if err != nil {
		t.Fatalf("font atlas: %v", err)
	}
	focused := Monitor{Name: "eDP-1", Width: 520, Height: 520, Scale: 1, Focused: true}
	wayland := newFakeWaylandBackend(focused)
	renderer := &fakeRendererSink{}
	pointer := newVirtualPointerRecorder(newFakeClock(time.Date(2026, 5, 1, 12, 15, 0, 0, time.UTC)))
	controller := NewDaemonController(DaemonDeps{
		MonitorLookup: &fakeFocusedMonitorLookup{monitor: focused},
		Overlay:       wayland,
		Renderer:      renderer,
		Pointer:       pointer,
		Config:        &config,
		FontAtlas:     atlas,
	})
	if err := controller.Show(ctx); err != nil {
		t.Fatalf("show overlay: %v", err)
	}

	mainCell := selectMainGridCellForTest(t, ctx, controller, focused, config.Grid.Size, 'M', 'K')
	assertLastPointerMotion(t, pointer, focused, mainCell.Center())

	beforePresentations := len(renderer.Presentations())
	beforePointerEvents := len(pointer.Events())
	if err := controller.HandleKeyboardToken(ctx, KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'E'}); err != nil {
		t.Fatalf("handle non-navigation letter: %v", err)
	}
	if got := len(renderer.Presentations()); got != beforePresentations {
		t.Fatalf("renderer presentations after ignored letter = %d, want %d", got, beforePresentations)
	}
	if got := len(pointer.Events()); got != beforePointerEvents {
		t.Fatalf("pointer events after ignored letter = %d, want %d", got, beforePointerEvents)
	}

	if err := controller.HandleKeyboardToken(ctx, KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'H'}); err != nil {
		t.Fatalf("handle H subgrid move: %v", err)
	}
	left := hiddenSubgridPointAfterMovesForTest(t, focused.LocalRect(), mainCell, config, mainCell.Center(), 'H')
	assertLastPointerMotion(t, pointer, focused, left)

	if err := controller.HandleKeyboardToken(ctx, KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'J'}); err != nil {
		t.Fatalf("handle J subgrid move: %v", err)
	}
	refined := hiddenSubgridPointAfterMovesForTest(t, focused.LocalRect(), mainCell, config, mainCell.Center(), 'H', 'J')
	assertLastPointerMotion(t, pointer, focused, refined)
	if got := len(renderer.Presentations()); got != beforePresentations {
		t.Fatalf("renderer presentations after hidden subgrid moves = %d, want %d", got, beforePresentations)
	}
}

func TestDaemonHiddenSubgridVimKeysContinueBeyondSelectedCellAndClampAtMonitorEdge(t *testing.T) {
	ctx := context.Background()
	config := DefaultConfig()
	atlas, err := NewFontAtlasFromConfig(config)
	if err != nil {
		t.Fatalf("font atlas: %v", err)
	}
	focused := Monitor{Name: "eDP-1", Width: 520, Height: 520, Scale: 1, Focused: true}
	wayland := newFakeWaylandBackend(focused)
	renderer := &fakeRendererSink{}
	pointer := newVirtualPointerRecorder(newFakeClock(time.Date(2026, 5, 1, 12, 30, 0, 0, time.UTC)))
	controller := NewDaemonController(DaemonDeps{
		MonitorLookup: &fakeFocusedMonitorLookup{monitor: focused},
		Overlay:       wayland,
		Renderer:      renderer,
		Pointer:       pointer,
		Config:        &config,
		FontAtlas:     atlas,
	})
	if err := controller.Show(ctx); err != nil {
		t.Fatalf("show overlay: %v", err)
	}

	mainCell := selectMainGridCellForTest(t, ctx, controller, focused, config.Grid.Size, 'M', 'K')
	for i := 0; i < 3; i++ {
		if err := controller.HandleKeyboardToken(ctx, KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'H'}); err != nil {
			t.Fatalf("handle H subgrid move %d: %v", i+1, err)
		}
	}
	want := hiddenSubgridPointAfterMovesForTest(t, focused.LocalRect(), mainCell, config, mainCell.Center(), 'H', 'H', 'H')
	if want.X >= mainCell.X {
		t.Fatalf("test expected point beyond selected cell: got %+v selected cell %+v", want, mainCell)
	}
	assertLastPointerMotion(t, pointer, focused, want)

	for i := 0; i < 100; i++ {
		if err := controller.HandleKeyboardToken(ctx, KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'H'}); err != nil {
			t.Fatalf("handle H toward monitor edge %d: %v", i+1, err)
		}
	}
	assertLastPointerMotion(t, pointer, focused, Point{X: 0, Y: want.Y})
	beforePointerEvents := len(pointer.Events())
	if err := controller.HandleKeyboardToken(ctx, KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'H'}); err != nil {
		t.Fatalf("handle H at monitor edge: %v", err)
	}
	if got := len(pointer.Events()); got != beforePointerEvents {
		t.Fatalf("pointer events after H at monitor edge = %d, want %d", got, beforePointerEvents)
	}
}

func assertLastRendererHashMatchesMainGrid(t *testing.T, renderer *fakeRendererSink, monitor Monitor, config Config, atlas *FontAtlas, hud string, selectedColumn *int) {
	t.Helper()
	expected, err := NewARGBBuffer(monitor.Width, monitor.Height)
	if err != nil {
		t.Fatalf("new expected buffer: %v", err)
	}
	if err := RenderMainGridOverlay(expected, MainGridRenderOptions{
		GridSize:       config.Grid.Size,
		Appearance:     config.Appearance,
		FontAtlas:      atlas,
		HUD:            hud,
		SelectedColumn: selectedColumn,
	}); err != nil {
		t.Fatalf("render expected grid: %v", err)
	}
	presentations := renderer.Presentations()
	if len(presentations) == 0 {
		t.Fatalf("renderer has no presentations")
	}
	if got, want := presentations[len(presentations)-1].Hash, mustARGBHash(t, expected); got != want {
		t.Fatalf("last renderer hash = %s, want %s for HUD %q selected %v", got, want, hud, selectedColumn)
	}
}

func assertLastRendererHashMatchesSelectedCell(t *testing.T, renderer *fakeRendererSink, monitor Monitor, config Config, cell Rect) {
	t.Helper()
	expected, err := NewARGBBuffer(monitor.Width, monitor.Height)
	if err != nil {
		t.Fatalf("new expected buffer: %v", err)
	}
	if err := RenderSelectedCellOverlay(expected, SelectedCellRenderOptions{
		Cell:       cell,
		Appearance: config.Appearance,
	}); err != nil {
		t.Fatalf("render expected selected cell: %v", err)
	}
	presentations := renderer.Presentations()
	if len(presentations) == 0 {
		t.Fatalf("renderer has no presentations")
	}
	if got, want := presentations[len(presentations)-1].Hash, mustARGBHash(t, expected); got != want {
		t.Fatalf("last renderer hash = %s, want %s for selected cell %+v", got, want, cell)
	}
}

func hiddenSubgridPointAfterMovesForTest(t *testing.T, bounds Rect, mainCell Rect, config Config, start Point, moves ...byte) Point {
	t.Helper()
	xCount, err := SubgridAxisCount(mainCell.Width, config.Grid.SubgridPixelSize)
	if err != nil {
		t.Fatalf("expected hidden subgrid X count: %v", err)
	}
	yCount, err := SubgridAxisCount(mainCell.Height, config.Grid.SubgridPixelSize)
	if err != nil {
		t.Fatalf("expected hidden subgrid Y count: %v", err)
	}
	fsm := NewSubgridNavigationFSM(mainCell, bounds, xCount, yCount, start)
	point := start
	for _, move := range moves {
		result := fsm.HandleToken(KeyboardToken{Kind: KeyboardTokenLetter, Letter: move})
		if !result.Changed {
			t.Fatalf("hidden subgrid move %q was ignored", move)
		}
		point = result.Point
	}
	return point
}

func selectMainGridCellForTest(t *testing.T, ctx context.Context, controller *DaemonController, monitor Monitor, gridSize int, colLetter byte, rowLetter byte) Rect {
	t.Helper()
	if err := controller.HandleKeyboardToken(ctx, KeyboardToken{Kind: KeyboardTokenLetter, Letter: colLetter}); err != nil {
		t.Fatalf("handle main grid column %q: %v", colLetter, err)
	}
	if err := controller.HandleKeyboardToken(ctx, KeyboardToken{Kind: KeyboardTokenLetter, Letter: rowLetter}); err != nil {
		t.Fatalf("handle main grid row %q: %v", rowLetter, err)
	}
	bounds, err := GridCellBounds(monitor, gridSize, int(colLetter-'A'), int(rowLetter-'A'))
	if err != nil {
		t.Fatalf("expected grid cell bounds: %v", err)
	}
	return bounds
}

func assertLastPointerMotion(t *testing.T, pointer *virtualPointerRecorder, output Monitor, want Point) {
	t.Helper()
	events := pointer.Events()
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if event.Kind != "motion" {
			continue
		}
		if event.OutputName != output.Name || event.X != want.X || event.Y != want.Y {
			t.Fatalf("last pointer motion = %+v, want output=%s point=%+v", event, output.Name, want)
		}
		return
	}
	t.Fatalf("pointer recorder has no motion events: %+v", events)
}

func waitForRendererPresentationCount(t *testing.T, renderer *fakeRendererSink, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := len(renderer.Presentations()); got >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("renderer presentations = %d, want at least %d", len(renderer.Presentations()), want)
}

func assertRendererPresentationCountStays(t *testing.T, renderer *fakeRendererSink, want int) {
	t.Helper()
	time.Sleep(50 * time.Millisecond)
	if got := len(renderer.Presentations()); got != want {
		t.Fatalf("renderer presentations after ignored key = %d, want %d", got, want)
	}
}

func receiveMainCoordinateSelection(t *testing.T, selected <-chan MainCoordinateSelectedEvent) MainCoordinateSelectedEvent {
	t.Helper()
	select {
	case event := <-selected:
		return event
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for main coordinate selection")
		return MainCoordinateSelectedEvent{}
	}
}

func ptrToInt(value int) *int {
	return &value
}
