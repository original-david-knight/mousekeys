package main

import (
	"context"
	"fmt"
	"sync"
)

type DaemonState string

const (
	DaemonStateInactive     DaemonState = "inactive"
	DaemonStateOverlayShown DaemonState = "overlay_shown"
)

type DaemonDeps struct {
	MonitorLookup FocusedMonitorLookup
	Overlay       WaylandOverlayBackend
	Keyboard      KeyboardEventSource
	Renderer      RendererBufferSink
	Config        *Config
	FontAtlas     *FontAtlas
	Pointer       PointerSynthesizer
	Clock         Clock
	Trace         TraceRecorder

	MainCoordinateSelected func(context.Context, MainCoordinateSelectedEvent) error
}

type DaemonController struct {
	mu              sync.Mutex
	deps            DaemonDeps
	state           DaemonState
	monitor         Monitor
	surface         OverlaySurface
	coordinateEntry *MainCoordinateEntryFSM
	subgridEntry    *SubgridRefinementFSM
	subgridGeometry SubgridGeometry
	currentPoint    Point
	havePoint       bool
	keyboardCancel  context.CancelFunc
}

func NewDaemonController(deps DaemonDeps) *DaemonController {
	if deps.Clock == nil {
		deps.Clock = systemClock{}
	}
	if deps.Trace == nil {
		deps.Trace = noopTraceRecorder{}
	}
	if deps.Config == nil {
		config := DefaultConfig()
		deps.Config = &config
	}
	return &DaemonController{
		deps:  deps,
		state: DaemonStateInactive,
	}
}

func (d *DaemonController) State() DaemonState {
	if d == nil {
		return DaemonStateInactive
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state
}

func (d *DaemonController) Show(ctx context.Context) (err error) {
	if d == nil {
		return fmt.Errorf("daemon controller is nil")
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.state == DaemonStateOverlayShown {
		if !overlaySurfaceClosed(d.surface) {
			return d.hideLocked(ctx)
		}
		d.clearOverlayStateLocked()
	}
	if d.deps.MonitorLookup == nil {
		return fmt.Errorf("focused monitor lookup is required")
	}
	if d.deps.Overlay == nil {
		return fmt.Errorf("Wayland overlay backend is required")
	}

	d.deps.Trace.Record("state", "show_requested", nil)

	focused, err := d.deps.MonitorLookup.FocusedMonitor(ctx)
	if err != nil {
		return err
	}
	monitor := focused
	outputs, err := d.deps.Overlay.Outputs(ctx)
	if err != nil {
		return err
	}
	if len(outputs) > 0 {
		monitor, err = MatchWaylandOutputByName(outputs, focused)
		if err != nil {
			return err
		}
	}

	surface, err := d.deps.Overlay.CreateSurface(ctx, monitor)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			d.stopKeyboardInputLocked()
			d.resetCoordinateEntryLocked()
			_ = surface.Destroy(ctx)
		}
	}()

	config := SurfaceConfig{
		OutputName: monitor.Name,
		Width:      monitor.Width,
		Height:     monitor.Height,
		Scale:      monitor.Scale,
	}
	if err := surface.Configure(ctx, config); err != nil {
		return err
	}
	if err := surface.GrabKeyboard(ctx); err != nil {
		return err
	}

	appConfig := DefaultConfig()
	if d.deps.Config != nil {
		appConfig = *d.deps.Config
	}
	d.coordinateEntry = NewMainCoordinateEntryFSM(appConfig.Grid.Size, monitor)
	if rerenderer, ok := surface.(OverlaySurfaceRerenderer); ok {
		rerenderer.SetRerenderer(d.activeOverlayRerenderer())
	}
	buffer, err := d.renderActiveOverlayBufferLocked(config)
	if err != nil {
		return err
	}
	if d.deps.Renderer != nil {
		if err := d.deps.Renderer.Present(ctx, surface.ID(), buffer); err != nil {
			return err
		}
	}
	if err := surface.Render(ctx, buffer); err != nil {
		return err
	}
	if err := d.startKeyboardInputLocked(ctx); err != nil {
		return err
	}

	d.monitor = monitor
	d.surface = surface
	d.state = DaemonStateOverlayShown
	d.watchSurfaceClosed(surface)
	d.deps.Trace.Record("state", "overlay_shown", map[string]any{
		"output": monitor.Name,
		"width":  monitor.Width,
		"height": monitor.Height,
		"scale":  monitor.Scale,
		"x":      monitor.X,
		"y":      monitor.Y,
	})
	return nil
}

func (d *DaemonController) activeOverlayRerenderer() SurfaceRerenderFunc {
	return func(surfaceConfig SurfaceConfig) (ARGBBuffer, error) {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.renderActiveOverlayBufferLocked(surfaceConfig)
	}
}

func (d *DaemonController) renderActiveOverlayBufferLocked(surfaceConfig SurfaceConfig) (ARGBBuffer, error) {
	if d.subgridEntry != nil {
		return d.renderSubgridBufferLocked(surfaceConfig)
	}
	return d.renderMainGridBufferLocked(surfaceConfig)
}

func (d *DaemonController) renderMainGridBufferLocked(surfaceConfig SurfaceConfig) (ARGBBuffer, error) {
	config := DefaultConfig()
	if d.deps.Config != nil {
		config = *d.deps.Config
	}
	atlas := d.deps.FontAtlas
	if atlas == nil {
		var err error
		atlas, err = NewFontAtlasFromConfig(config)
		if err != nil {
			return ARGBBuffer{}, err
		}
		d.deps.FontAtlas = atlas
	}

	hud := DefaultMainGridHUD
	var selectedColumn *int
	if d.coordinateEntry != nil {
		hud = d.coordinateEntry.HUD()
		if col, ok := d.coordinateEntry.SelectedColumn(); ok {
			selectedColumn = &col
		}
	}

	buffer, err := NewARGBBuffer(surfaceConfig.Width, surfaceConfig.Height)
	if err != nil {
		return ARGBBuffer{}, err
	}
	if err := RenderMainGridOverlay(buffer, MainGridRenderOptions{
		GridSize:       config.Grid.Size,
		Appearance:     config.Appearance,
		FontAtlas:      atlas,
		HUD:            hud,
		SelectedColumn: selectedColumn,
	}); err != nil {
		return ARGBBuffer{}, err
	}
	return buffer, nil
}

func (d *DaemonController) renderSubgridBufferLocked(surfaceConfig SurfaceConfig) (ARGBBuffer, error) {
	config := DefaultConfig()
	if d.deps.Config != nil {
		config = *d.deps.Config
	}
	atlas := d.deps.FontAtlas
	if atlas == nil {
		var err error
		atlas, err = NewFontAtlasFromConfig(config)
		if err != nil {
			return ARGBBuffer{}, err
		}
		d.deps.FontAtlas = atlas
	}

	buffer, err := NewARGBBuffer(surfaceConfig.Width, surfaceConfig.Height)
	if err != nil {
		return ARGBBuffer{}, err
	}
	if err := RenderSubgridOverlay(buffer, SubgridRenderOptions{
		Geometry:   d.subgridGeometry,
		Appearance: config.Appearance,
		FontAtlas:  atlas,
	}); err != nil {
		return ARGBBuffer{}, err
	}
	return buffer, nil
}

func (d *DaemonController) startKeyboardInputLocked(ctx context.Context) error {
	if d.deps.Keyboard == nil {
		return nil
	}

	config := DefaultConfig()
	if d.deps.Config != nil {
		config = *d.deps.Config
	}
	mapper, err := NewKeyboardInputMapper(config)
	if err != nil {
		return err
	}

	keyboardCtx, cancel := context.WithCancel(ctx)
	tokens, err := mapper.Tokens(keyboardCtx, d.deps.Keyboard)
	if err != nil {
		cancel()
		return err
	}
	d.keyboardCancel = cancel
	go d.consumeKeyboardTokens(keyboardCtx, tokens)
	return nil
}

func (d *DaemonController) consumeKeyboardTokens(ctx context.Context, tokens <-chan KeyboardToken) {
	for {
		select {
		case <-ctx.Done():
			return
		case token, ok := <-tokens:
			if !ok {
				return
			}
			if err := d.HandleKeyboardToken(ctx, token); err != nil {
				d.deps.Trace.Record("error", "keyboard_token_failed", map[string]any{"error": err.Error()})
			}
		}
	}
}

func (d *DaemonController) HandleKeyboardToken(ctx context.Context, token KeyboardToken) error {
	if d == nil {
		return fmt.Errorf("daemon controller is nil")
	}

	var selected *MainCoordinateSelectedEvent
	var selectionHandler func(context.Context, MainCoordinateSelectedEvent) error
	var trace TraceRecorder = noopTraceRecorder{}

	d.mu.Lock()
	if d.state != DaemonStateOverlayShown || d.coordinateEntry == nil {
		d.mu.Unlock()
		return nil
	}
	if tokenHasKeyboardCommand(token, KeyboardCommandExit) {
		err := d.hideLocked(ctx)
		d.mu.Unlock()
		return err
	}

	if d.subgridEntry != nil {
		result := d.subgridEntry.HandleToken(token)
		if !result.Changed && result.Committed == nil {
			d.mu.Unlock()
			return nil
		}
		if result.Changed {
			if err := d.renderActiveOverlayLocked(ctx); err != nil {
				d.mu.Unlock()
				return err
			}
		}
		if result.Committed != nil {
			if err := d.commitSubgridRefinementLocked(ctx, *result.Committed); err != nil {
				d.mu.Unlock()
				return err
			}
		}
		d.mu.Unlock()
		return nil
	}

	result := d.coordinateEntry.HandleToken(token)
	if !result.Changed && result.Selected == nil {
		d.mu.Unlock()
		return nil
	}

	if result.Selected != nil {
		selectedCopy := *result.Selected
		selected = &selectedCopy
		if err := d.enterSubgridLocked(ctx, selectedCopy); err != nil {
			d.mu.Unlock()
			return err
		}
		selectionHandler = d.deps.MainCoordinateSelected
		if d.deps.Trace != nil {
			trace = d.deps.Trace
		}
	} else if result.Changed {
		if err := d.renderActiveMainGridLocked(ctx); err != nil {
			d.mu.Unlock()
			return err
		}
	}
	d.mu.Unlock()

	if selected == nil {
		return nil
	}
	trace.Record("fsm", "main_coordinate_selected", map[string]any{
		"column":        selected.Column,
		"row":           selected.Row,
		"column_letter": string([]byte{selected.ColumnLetter}),
		"row_letter":    string([]byte{selected.RowLetter}),
		"x":             selected.Bounds.X,
		"y":             selected.Bounds.Y,
		"width":         selected.Bounds.Width,
		"height":        selected.Bounds.Height,
		"center_x":      selected.Center.X,
		"center_y":      selected.Center.Y,
	})
	if selectionHandler != nil {
		return selectionHandler(ctx, *selected)
	}
	return nil
}

func (d *DaemonController) enterSubgridLocked(ctx context.Context, selected MainCoordinateSelectedEvent) error {
	config := DefaultConfig()
	if d.deps.Config != nil {
		config = *d.deps.Config
	}
	geometry, err := NewSubgridGeometry(d.monitor, selected.Bounds, selected.Center, config.Grid.SubgridPixelSize)
	if err != nil {
		return err
	}
	if err := d.movePointerLocked(ctx, selected.Center); err != nil {
		return err
	}

	d.subgridGeometry = geometry
	d.subgridEntry = NewSubgridRefinementFSM(selected.Bounds, geometry.XCount, geometry.YCount)
	d.deps.Trace.Record("fsm", "subgrid_shown", map[string]any{
		"x":       geometry.Display.X,
		"y":       geometry.Display.Y,
		"width":   geometry.Display.Width,
		"height":  geometry.Display.Height,
		"x_count": geometry.XCount,
		"y_count": geometry.YCount,
	})
	return d.renderActiveOverlayLocked(ctx)
}

func (d *DaemonController) commitSubgridRefinementLocked(ctx context.Context, commit SubgridRefinementCommit) error {
	if err := d.movePointerLocked(ctx, commit.Point); err != nil {
		return err
	}
	d.deps.Trace.Record("fsm", "subgrid_refined", map[string]any{
		"mode":          string(commit.Mode),
		"column":        commit.Column,
		"row":           commit.Row,
		"column_letter": string([]byte{commit.ColumnLetter}),
		"row_letter":    stringOrEmpty(commit.RowLetter),
		"x":             commit.Point.X,
		"y":             commit.Point.Y,
	})
	return nil
}

func (d *DaemonController) movePointerLocked(ctx context.Context, p Point) error {
	if !d.monitor.ContainsLocal(p) {
		return fmt.Errorf("pointer target outside focused monitor: %d,%d not in %dx%d", p.X, p.Y, d.monitor.Width, d.monitor.Height)
	}
	if d.deps.Pointer != nil {
		if err := d.deps.Pointer.MoveAbsolute(ctx, p.X, p.Y, d.monitor); err != nil {
			return err
		}
	}
	d.currentPoint = p
	d.havePoint = true
	d.deps.Trace.Record("io", "pointer_move", map[string]any{
		"output": d.monitor.Name,
		"x":      p.X,
		"y":      p.Y,
	})
	return nil
}

func stringOrEmpty(letter byte) string {
	if letter == 0 {
		return ""
	}
	return string([]byte{letter})
}

func (d *DaemonController) renderActiveMainGridLocked(ctx context.Context) error {
	if d.surface == nil {
		return nil
	}
	config := SurfaceConfig{
		OutputName: d.monitor.Name,
		Width:      d.monitor.Width,
		Height:     d.monitor.Height,
		Scale:      d.monitor.Scale,
	}
	buffer, err := d.renderMainGridBufferLocked(config)
	if err != nil {
		return err
	}
	if d.deps.Renderer != nil {
		if err := d.deps.Renderer.Present(ctx, d.surface.ID(), buffer); err != nil {
			return err
		}
	}
	return d.surface.Render(ctx, buffer)
}

func (d *DaemonController) renderActiveOverlayLocked(ctx context.Context) error {
	if d.surface == nil {
		return nil
	}
	config := SurfaceConfig{
		OutputName: d.monitor.Name,
		Width:      d.monitor.Width,
		Height:     d.monitor.Height,
		Scale:      d.monitor.Scale,
	}
	buffer, err := d.renderActiveOverlayBufferLocked(config)
	if err != nil {
		return err
	}
	if d.deps.Renderer != nil {
		if err := d.deps.Renderer.Present(ctx, d.surface.ID(), buffer); err != nil {
			return err
		}
	}
	return d.surface.Render(ctx, buffer)
}

func (d *DaemonController) Hide(ctx context.Context) error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.hideLocked(ctx)
}

func (d *DaemonController) hideLocked(ctx context.Context) error {
	if d.state == DaemonStateInactive {
		d.resetCoordinateEntryLocked()
		return nil
	}
	d.stopKeyboardInputLocked()
	if d.surface != nil {
		if err := d.surface.Destroy(ctx); err != nil {
			return err
		}
	}
	d.clearOverlayStateLocked()
	d.deps.Trace.Record("state", "overlay_hidden", nil)
	return nil
}

func (d *DaemonController) clearOverlayStateLocked() {
	d.stopKeyboardInputLocked()
	d.resetCoordinateEntryLocked()
	d.surface = nil
	d.monitor = Monitor{}
	d.state = DaemonStateInactive
}

func (d *DaemonController) resetCoordinateEntryLocked() {
	if d.coordinateEntry != nil {
		d.coordinateEntry.Reset()
	}
	d.coordinateEntry = nil
	if d.subgridEntry != nil {
		d.subgridEntry.Reset()
	}
	d.subgridEntry = nil
	d.subgridGeometry = SubgridGeometry{}
	d.currentPoint = Point{}
	d.havePoint = false
}

func (d *DaemonController) stopKeyboardInputLocked() {
	if d.keyboardCancel != nil {
		d.keyboardCancel()
		d.keyboardCancel = nil
	}
}

func (d *DaemonController) ClickAt(ctx context.Context, p Point, button PointerButton, clickCount int, groupID string) error {
	if d == nil {
		return fmt.Errorf("daemon controller is nil")
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.state != DaemonStateOverlayShown {
		return fmt.Errorf("cannot click while daemon state is %q", d.state)
	}
	if !d.monitor.ContainsLocal(p) {
		return fmt.Errorf("click point outside focused monitor: %d,%d", p.X, p.Y)
	}
	if clickCount != 1 && clickCount != 2 {
		return fmt.Errorf("unsupported click count %d", clickCount)
	}

	if clickCount == 2 {
		if err := EmitPointerDoubleClick(ctx, d.deps.Pointer, d.monitor, p, button); err != nil {
			return err
		}
	} else {
		if err := EmitPointerClick(ctx, d.deps.Pointer, d.monitor, p, button); err != nil {
			return err
		}
	}

	d.deps.Trace.Record("io", "pointer_click", map[string]any{
		"output":      d.monitor.Name,
		"x":           p.X,
		"y":           p.Y,
		"button":      string(button),
		"click_count": clickCount,
		"group_id":    groupID,
	})
	return nil
}

func (d *DaemonController) watchSurfaceClosed(surface OverlaySurface) {
	if surface == nil {
		return
	}
	closed := surface.Closed()
	if closed == nil {
		return
	}
	go func() {
		<-closed
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.surface != surface || d.state != DaemonStateOverlayShown {
			return
		}
		d.clearOverlayStateLocked()
		d.deps.Trace.Record("state", "overlay_closed", nil)
	}()
}

func overlaySurfaceClosed(surface OverlaySurface) bool {
	if surface == nil {
		return true
	}
	closed := surface.Closed()
	if closed == nil {
		return false
	}
	select {
	case <-closed:
		return true
	default:
		return false
	}
}
