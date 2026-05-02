package main

import (
	"context"
	"testing"
	"time"
)

func TestDaemonLeftClickWaitsForTimeoutThenStaysActive(t *testing.T) {
	ctx := context.Background()
	controller, clock, pointer, renderer, _, focused, config, atlas := newClickActionTestController(t, true)
	mainCell := selectMainGridCellForTest(t, ctx, controller, focused, config.Grid.Size, 'M', 'K')
	beforePresentations := len(renderer.Presentations())
	beforeButtons := countPointerClicks(pointer, PointerButtonLeft)

	if err := controller.HandleKeyboardToken(ctx, commandToken("Return", KeyboardCommandLeftClick)); err != nil {
		t.Fatalf("handle first Enter: %v", err)
	}
	if got := countPointerClicks(pointer, PointerButtonLeft); got != beforeButtons {
		t.Fatalf("left clicks immediately after first Enter = %d, want %d", got, beforeButtons)
	}
	if got := len(renderer.Presentations()); got != beforePresentations {
		t.Fatalf("renderer presentations immediately after first Enter = %d, want %d", got, beforePresentations)
	}

	clock.Advance(249 * time.Millisecond)
	if got := countPointerClicks(pointer, PointerButtonLeft); got != beforeButtons {
		t.Fatalf("left clicks before timeout = %d, want %d", got, beforeButtons)
	}
	clock.Advance(time.Millisecond)
	waitForPointerClickCount(t, pointer, PointerButtonLeft, beforeButtons+1)
	assertLastPointerMotion(t, pointer, focused, mainCell.Center())
	waitForRendererPresentationCount(t, renderer, beforePresentations+1)
	assertLastRendererHashMatchesMainGrid(t, renderer, focused, config, atlas, DefaultMainGridHUD, nil)
	if controller.State() != DaemonStateOverlayShown {
		t.Fatalf("state after timed single click = %q, want overlay shown", controller.State())
	}
}

func TestDaemonDoubleClickDoesNotReopenMainGridBetweenClicks(t *testing.T) {
	ctx := context.Background()
	controller, _, pointer, renderer, wayland, focused, config, atlas := newClickActionTestController(t, true)
	mainCell := selectMainGridCellForTest(t, ctx, controller, focused, config.Grid.Size, 'M', 'K')
	beforePresentations := len(renderer.Presentations())
	beforeRenders := wayland.Count("render")

	if err := controller.HandleKeyboardToken(ctx, commandToken("Return", KeyboardCommandLeftClick)); err != nil {
		t.Fatalf("handle first Enter: %v", err)
	}
	if got := countPointerClicks(pointer, PointerButtonLeft); got != 0 {
		t.Fatalf("left clicks after first Enter = %d, want 0", got)
	}
	if got := len(renderer.Presentations()); got != beforePresentations {
		t.Fatalf("renderer presentations between double-click keys = %d, want %d", got, beforePresentations)
	}
	if got := wayland.Count("render"); got != beforeRenders {
		t.Fatalf("surface renders between double-click keys = %d, want %d", got, beforeRenders)
	}

	if err := controller.HandleKeyboardToken(ctx, commandToken("Return", KeyboardCommandLeftClick)); err != nil {
		t.Fatalf("handle second Enter: %v", err)
	}
	waitForPointerClickCount(t, pointer, PointerButtonLeft, 2)
	assertLastPointerMotion(t, pointer, focused, mainCell.Center())
	waitForRendererPresentationCount(t, renderer, beforePresentations+1)
	if got := wayland.Count("render"); got != beforeRenders+1 {
		t.Fatalf("surface renders after double click = %d, want %d", got, beforeRenders+1)
	}
	assertLastRendererHashMatchesMainGrid(t, renderer, focused, config, atlas, DefaultMainGridHUD, nil)
}

func TestDaemonRightClickAndStayActiveFalseExitAfterClick(t *testing.T) {
	ctx := context.Background()
	controller, _, pointer, _, wayland, focused, config, _ := newClickActionTestController(t, false)
	mainCell := selectMainGridCellForTest(t, ctx, controller, focused, config.Grid.Size, 'M', 'K')

	if err := controller.HandleKeyboardToken(ctx, commandToken("space", KeyboardCommandRightClick)); err != nil {
		t.Fatalf("handle Space right click: %v", err)
	}
	waitForPointerClickCount(t, pointer, PointerButtonRight, 1)
	assertLastPointerMotion(t, pointer, focused, mainCell.Center())
	if controller.State() != DaemonStateInactive {
		t.Fatalf("state after right click with stay_active=false = %q, want inactive", controller.State())
	}
	if got := wayland.Count("destroy"); got != 1 {
		t.Fatalf("surface destroy count = %d, want 1", got)
	}
}

func TestDaemonTabCommitThenRightClickUsesRefinedPoint(t *testing.T) {
	ctx := context.Background()
	controller, _, pointer, _, _, focused, config, _ := newClickActionTestController(t, true)
	mainCell := selectMainGridCellForTest(t, ctx, controller, focused, config.Grid.Size, 'M', 'K')
	if err := controller.HandleKeyboardToken(ctx, KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'C', KeySym: "C"}); err != nil {
		t.Fatalf("handle subgrid X: %v", err)
	}
	if err := controller.HandleKeyboardToken(ctx, commandToken("Tab", KeyboardCommandCommitPartial)); err != nil {
		t.Fatalf("handle Tab commit partial: %v", err)
	}
	refined := xOnlySubgridPointForTest(t, mainCell, 4, 2)
	assertLastPointerMotion(t, pointer, focused, refined)

	if err := controller.HandleKeyboardToken(ctx, commandToken("space", KeyboardCommandRightClick)); err != nil {
		t.Fatalf("handle Space after Tab: %v", err)
	}
	waitForPointerClickCount(t, pointer, PointerButtonRight, 1)
	assertLastPointerMotion(t, pointer, focused, refined)
}

func TestDaemonClickAutoCommitsPendingSubgridPartial(t *testing.T) {
	ctx := context.Background()
	controller, clock, pointer, _, _, focused, config, _ := newClickActionTestController(t, true)
	mainCell := selectMainGridCellForTest(t, ctx, controller, focused, config.Grid.Size, 'M', 'K')
	if err := controller.HandleKeyboardToken(ctx, KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'C', KeySym: "C"}); err != nil {
		t.Fatalf("handle subgrid X: %v", err)
	}
	refined := xOnlySubgridPointForTest(t, mainCell, 4, 2)

	if err := controller.HandleKeyboardToken(ctx, commandToken("Return", KeyboardCommandLeftClick)); err != nil {
		t.Fatalf("handle Enter after partial subgrid X: %v", err)
	}
	assertLastPointerMotion(t, pointer, focused, refined)
	if got := countPointerClicks(pointer, PointerButtonLeft); got != 0 {
		t.Fatalf("left clicks before timeout = %d, want 0", got)
	}
	clock.Advance(250 * time.Millisecond)
	waitForPointerClickCount(t, pointer, PointerButtonLeft, 1)
	assertLastPointerMotion(t, pointer, focused, refined)
}

func TestDaemonEscCancelsPendingClickAndOverridesStayActive(t *testing.T) {
	ctx := context.Background()
	controller, clock, pointer, _, wayland, focused, config, _ := newClickActionTestController(t, true)
	mainCell := selectMainGridCellForTest(t, ctx, controller, focused, config.Grid.Size, 'M', 'K')
	assertLastPointerMotion(t, pointer, focused, mainCell.Center())

	if err := controller.HandleKeyboardToken(ctx, commandToken("Return", KeyboardCommandLeftClick)); err != nil {
		t.Fatalf("handle pending Enter: %v", err)
	}
	if err := controller.HandleKeyboardToken(ctx, commandToken("Escape", KeyboardCommandExit)); err != nil {
		t.Fatalf("handle Escape: %v", err)
	}
	if controller.State() != DaemonStateInactive {
		t.Fatalf("state after Escape = %q, want inactive", controller.State())
	}
	if got := countPointerClicks(pointer, PointerButtonLeft); got != 0 {
		t.Fatalf("left clicks after Escape = %d, want 0", got)
	}
	if got := wayland.Count("destroy"); got != 1 {
		t.Fatalf("surface destroy count after Escape = %d, want 1", got)
	}
	clock.Advance(250 * time.Millisecond)
	if got := countPointerClicks(pointer, PointerButtonLeft); got != 0 {
		t.Fatalf("left clicks after canceled timeout = %d, want 0", got)
	}
	assertLastPointerMotion(t, pointer, focused, mainCell.Center())
}

func newClickActionTestController(t *testing.T, stayActive bool) (*DaemonController, *fakeClock, *virtualPointerRecorder, *fakeRendererSink, *fakeWaylandBackend, Monitor, Config, *FontAtlas) {
	t.Helper()
	config := DefaultConfig()
	config.Behavior.StayActive = stayActive
	config.Behavior.DoubleClickTimeoutMS = 250
	atlas, err := NewFontAtlasFromConfig(config)
	if err != nil {
		t.Fatalf("font atlas: %v", err)
	}
	focused := Monitor{Name: "eDP-1", Width: 520, Height: 520, Scale: 1, Focused: true}
	clock := newFakeClock(time.Date(2026, 5, 2, 9, 0, 0, 0, time.UTC))
	pointer := newVirtualPointerRecorder(clock)
	renderer := &fakeRendererSink{}
	wayland := newFakeWaylandBackend(focused)
	controller := NewDaemonController(DaemonDeps{
		MonitorLookup: &fakeFocusedMonitorLookup{monitor: focused},
		Overlay:       wayland,
		Renderer:      renderer,
		Pointer:       pointer,
		Config:        &config,
		FontAtlas:     atlas,
		Clock:         clock,
	})
	if err := controller.Show(context.Background()); err != nil {
		t.Fatalf("show overlay: %v", err)
	}
	return controller, clock, pointer, renderer, wayland, focused, config, atlas
}

func commandToken(key string, command KeyboardCommand) KeyboardToken {
	return KeyboardToken{
		Kind:     KeyboardTokenCommand,
		KeySym:   KeySym(key),
		Commands: []KeyboardCommand{command},
	}
}

func countPointerClicks(pointer *virtualPointerRecorder, button PointerButton) int {
	count := 0
	for _, event := range pointer.Events() {
		if event.Kind == "button" && event.Button == button && event.State == ButtonDown {
			count++
		}
	}
	return count
}

func waitForPointerClickCount(t *testing.T, pointer *virtualPointerRecorder, button PointerButton, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := countPointerClicks(pointer, button); got >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s pointer clicks = %d, want at least %d; events=%+v", button, countPointerClicks(pointer, button), want, pointer.Events())
}

func xOnlySubgridPointForTest(t *testing.T, mainCell Rect, xCount int, col int) Point {
	t.Helper()
	x0, x1, err := axisSegment(mainCell.Width, xCount, col)
	if err != nil {
		t.Fatalf("expected X segment: %v", err)
	}
	return Point{X: mainCell.X + centeredInSpan(x0, x1, 1), Y: mainCell.Center().Y}
}
