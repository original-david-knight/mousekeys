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
	keyboard := newFakeKeyboardEventSource(8)
	selected := make(chan MainCoordinateSelectedEvent, 1)
	controller := NewDaemonController(DaemonDeps{
		MonitorLookup: &fakeFocusedMonitorLookup{monitor: focused},
		Overlay:       wayland,
		Renderer:      renderer,
		Keyboard:      keyboard,
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
	waitForRendererPresentationCount(t, renderer, 5)
	assertLastRendererHashMatchesMainGrid(t, renderer, focused, config, atlas, "MK", ptrToInt(12))
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
