package main

import (
	"context"
	"fmt"
)

func NewStubDaemonController(trace TraceRecorder) *DaemonController {
	monitor := Monitor{
		Name:    "stub-output",
		Width:   1920,
		Height:  1080,
		Scale:   1.0,
		Focused: true,
	}
	return NewDaemonController(DaemonDeps{
		MonitorLookup: staticFocusedMonitorLookup{monitor: monitor},
		Overlay:       stubOverlayBackend{},
		Trace:         trace,
	})
}

type staticFocusedMonitorLookup struct {
	monitor Monitor
}

func (s staticFocusedMonitorLookup) FocusedMonitor(context.Context) (Monitor, error) {
	if s.monitor.Name == "" || s.monitor.Width <= 0 || s.monitor.Height <= 0 {
		return Monitor{}, fmt.Errorf("stub focused monitor is not configured")
	}
	return s.monitor, nil
}

type stubOverlayBackend struct{}

func (stubOverlayBackend) Outputs(context.Context) ([]Monitor, error) {
	return []Monitor{{
		Name:    "stub-output",
		Width:   1920,
		Height:  1080,
		Scale:   1.0,
		Focused: true,
	}}, nil
}

func (stubOverlayBackend) CreateSurface(context.Context, Monitor) (OverlaySurface, error) {
	return stubOverlaySurface{id: "stub-overlay-surface"}, nil
}

type stubOverlaySurface struct {
	id string
}

func (s stubOverlaySurface) ID() string {
	return s.id
}

func (stubOverlaySurface) Configure(context.Context, SurfaceConfig) error {
	return nil
}

func (stubOverlaySurface) GrabKeyboard(context.Context) error {
	return nil
}

func (stubOverlaySurface) Render(context.Context, ARGBBuffer) error {
	return nil
}

func (stubOverlaySurface) Destroy(context.Context) error {
	return nil
}
