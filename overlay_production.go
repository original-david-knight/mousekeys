package main

import (
	"context"
	"fmt"
	"sync"
)

func newProductionOverlayDriver(getenv getenvFunc, config Config, trace *TraceRecorder) (*layerShellOverlayDriver, error) {
	renderer, err := NewSoftwareRenderer(config.Appearance)
	if err != nil {
		return nil, fmt.Errorf("create overlay renderer: %w", err)
	}
	backend := &lazyWaylandOverlayBackend{
		getenv: getenv,
		trace:  trace,
	}
	return newLayerShellOverlayDriver(NewHyprlandIPCClient(getenv), backend, renderer, config, trace)
}

type lazyWaylandOverlayBackend struct {
	mu      sync.Mutex
	getenv  getenvFunc
	trace   *TraceRecorder
	base    *WaylandClientBase
	backend *realWaylandOverlayBackend
}

func (b *lazyWaylandOverlayBackend) CreateSurface(ctx context.Context, monitor Monitor) (OverlaySurface, error) {
	backend, err := b.ensure(ctx)
	if err != nil {
		return nil, err
	}
	return backend.CreateSurface(ctx, monitor)
}

func (b *lazyWaylandOverlayBackend) OutputChanged(ctx context.Context, monitor Monitor) error {
	backend, err := b.ensure(ctx)
	if err != nil {
		return err
	}
	return backend.OutputChanged(ctx, monitor)
}

func (b *lazyWaylandOverlayBackend) Close(ctx context.Context) error {
	b.mu.Lock()
	backend := b.backend
	b.backend = nil
	b.base = nil
	b.mu.Unlock()
	if backend == nil {
		return nil
	}
	return backend.Close(ctx)
}

func (b *lazyWaylandOverlayBackend) ensure(ctx context.Context) (*realWaylandOverlayBackend, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.backend != nil {
		return b.backend, nil
	}
	base, err := OpenWaylandClientBase(ctx, b.getenv)
	if err != nil {
		return nil, err
	}
	backend, err := newRealWaylandOverlayBackend(base, b.trace)
	if err != nil {
		_ = base.Close(context.Background())
		return nil, err
	}
	b.base = base
	b.backend = backend
	return backend, nil
}
