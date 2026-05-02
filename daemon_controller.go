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
}

type DaemonController struct {
	mu      sync.Mutex
	deps    DaemonDeps
	state   DaemonState
	monitor Monitor
	surface OverlaySurface
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

	renderGrid, err := d.mainGridRendererLocked(DefaultMainGridHUD)
	if err != nil {
		return err
	}
	if rerenderer, ok := surface.(OverlaySurfaceRerenderer); ok {
		rerenderer.SetRerenderer(renderGrid)
	}
	buffer, err := renderGrid(config)
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

func (d *DaemonController) mainGridRendererLocked(hud string) (SurfaceRerenderFunc, error) {
	config := DefaultConfig()
	if d.deps.Config != nil {
		config = *d.deps.Config
	}
	atlas := d.deps.FontAtlas
	if atlas == nil {
		var err error
		atlas, err = NewFontAtlasFromConfig(config)
		if err != nil {
			return nil, err
		}
		d.deps.FontAtlas = atlas
	}

	return func(surfaceConfig SurfaceConfig) (ARGBBuffer, error) {
		buffer, err := NewARGBBuffer(surfaceConfig.Width, surfaceConfig.Height)
		if err != nil {
			return ARGBBuffer{}, err
		}
		if err := RenderMainGridOverlay(buffer, MainGridRenderOptions{
			GridSize:   config.Grid.Size,
			Appearance: config.Appearance,
			FontAtlas:  atlas,
			HUD:        hud,
		}); err != nil {
			return ARGBBuffer{}, err
		}
		return buffer, nil
	}, nil
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
		return nil
	}
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
	d.surface = nil
	d.monitor = Monitor{}
	d.state = DaemonStateInactive
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
