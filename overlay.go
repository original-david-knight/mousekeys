package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
)

type overlayGridRenderer interface {
	RenderMainGrid(monitor Monitor, gridSize int) (ARGBSnapshot, error)
}

type layerShellOverlayDriver struct {
	mu       sync.Mutex
	monitor  FocusedMonitorLookup
	wayland  WaylandOverlayBackend
	renderer overlayGridRenderer
	config   Config
	trace    *TraceRecorder
	session  *overlaySession
	nextID   int
}

type overlaySession struct {
	id        int
	ctx       context.Context
	cancel    context.CancelFunc
	monitor   Monitor
	surface   OverlaySurface
	keyboard  KeyboardEventSource
	lifecycle OverlayLifecycleEventSource
}

func newLayerShellOverlayDriver(monitor FocusedMonitorLookup, wayland WaylandOverlayBackend, renderer overlayGridRenderer, config Config, trace *TraceRecorder) (*layerShellOverlayDriver, error) {
	if monitor == nil {
		return nil, fmt.Errorf("focused monitor lookup is required")
	}
	if wayland == nil {
		return nil, fmt.Errorf("Wayland overlay backend is required")
	}
	if renderer == nil {
		return nil, fmt.Errorf("overlay renderer is required")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &layerShellOverlayDriver{
		monitor:  monitor,
		wayland:  wayland,
		renderer: renderer,
		config:   config,
		trace:    trace,
	}, nil
}

func (d *layerShellOverlayDriver) ShowOverlay(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	d.mu.Lock()
	if d.session != nil {
		d.mu.Unlock()
		return nil
	}
	d.nextID++
	sessionID := d.nextID
	d.mu.Unlock()

	monitor, err := d.monitor.FocusedMonitor(ctx)
	if err != nil {
		return err
	}
	surface, err := d.wayland.CreateSurface(ctx, monitor)
	if err != nil {
		return err
	}
	sessionCtx, cancel := context.WithCancel(context.Background())
	session := &overlaySession{
		id:      sessionID,
		ctx:     sessionCtx,
		cancel:  cancel,
		monitor: monitor,
		surface: surface,
	}
	if provider, ok := surface.(OverlayLifecycleEventProvider); ok {
		session.lifecycle = provider.LifecycleEvents()
	}

	if err := d.configureAndRender(ctx, session, monitor); err != nil {
		cancel()
		return errors.Join(err, destroyOverlaySurface(ctx, surface, false))
	}
	keyboard, err := surface.GrabKeyboard(ctx)
	if err != nil {
		cancel()
		return errors.Join(err, destroyOverlaySurface(ctx, surface, false))
	}
	session.keyboard = keyboard

	d.mu.Lock()
	if d.session != nil {
		d.mu.Unlock()
		cancel()
		return errors.Join(fmt.Errorf("overlay show raced with active session"), destroyOverlaySession(ctx, session, true))
	}
	d.session = session
	d.mu.Unlock()

	go d.runKeyboardLoop(session)
	if session.lifecycle != nil {
		go d.runLifecycleLoop(session)
	}
	return nil
}

func (d *layerShellOverlayDriver) CancelOverlay(ctx context.Context, reason overlayCancelReason) error {
	return d.finishActiveSession(ctx, reason, true)
}

func (d *layerShellOverlayDriver) OverlayActive() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.session != nil
}

func (d *layerShellOverlayDriver) CloseOverlay(ctx context.Context) error {
	err := d.finishActiveSession(ctx, overlayCancelDaemonShutdown, true)
	return errors.Join(err, d.wayland.Close(ctx))
}

func (d *layerShellOverlayDriver) runKeyboardLoop(session *overlaySession) {
	translator, err := NewKeyboardInputTranslator(d.config)
	if err != nil {
		d.recordOverlayError("keyboard", err)
		_ = d.finishSession(context.Background(), session, overlayCancelKeyboardDestroy, true)
		return
	}
	for {
		event, err := session.keyboard.NextKeyboardEvent(session.ctx)
		if err != nil {
			if !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrClosedPipe) {
				d.recordOverlayError("keyboard", err)
				_ = d.finishSession(context.Background(), session, overlayCancelKeyboardDestroy, true)
			}
			return
		}
		token, ok, err := translator.Apply(event)
		if err != nil {
			d.recordOverlayError("keyboard", err)
			_ = d.finishSession(context.Background(), session, overlayCancelKeyboardDestroy, true)
			return
		}
		if event.Kind == KeyboardEventDestroy {
			_ = d.finishSession(context.Background(), session, overlayCancelKeyboardDestroy, true)
			return
		}
		if ok {
			d.trace.Record(traceKeyboardToken, map[string]any{
				"kind":    token.Kind,
				"letter":  token.Letter,
				"command": token.Command,
				"chord":   token.Chord.String(),
			})
		}
		if ok && token.Kind == KeyboardTokenCommand && token.Command == KeyboardCommandExit {
			_ = d.finishSession(context.Background(), session, overlayCancelEscape, true)
			return
		}
	}
}

func (d *layerShellOverlayDriver) runLifecycleLoop(session *overlaySession) {
	for {
		event, err := session.lifecycle.NextOverlayEvent(session.ctx)
		if err != nil {
			if !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrClosedPipe) {
				d.recordOverlayError("lifecycle", err)
				_ = d.finishSession(context.Background(), session, overlayCancelCompositor, false)
			}
			return
		}
		switch event.Kind {
		case OverlayLifecycleConfigure:
			monitor := session.monitor
			if event.Width > 0 {
				monitor.LogicalWidth = event.Width
			}
			if event.Height > 0 {
				monitor.LogicalHeight = event.Height
			}
			if event.Scale > 0 {
				monitor.Scale = event.Scale
			}
			if err := d.reconfigureActiveSession(session, monitor); err != nil {
				d.recordOverlayError("configure", err)
				_ = d.finishSession(context.Background(), session, overlayCancelCompositor, false)
				return
			}
		case OverlayLifecycleRelease:
		case OverlayLifecycleOutputChange:
			monitor := event.Monitor
			if err := monitor.Validate(); err != nil {
				d.recordOverlayError("output_change", err)
				_ = d.finishSession(context.Background(), session, overlayCancelCompositor, false)
				return
			}
			if err := d.wayland.OutputChanged(session.ctx, monitor); err != nil {
				d.recordOverlayError("output_change", err)
				_ = d.finishSession(context.Background(), session, overlayCancelCompositor, false)
				return
			}
			if err := d.reconfigureActiveSession(session, monitor); err != nil {
				d.recordOverlayError("output_change", err)
				_ = d.finishSession(context.Background(), session, overlayCancelCompositor, false)
				return
			}
		case OverlayLifecycleCompositorClose, OverlayLifecycleDestroy:
			_ = d.finishSession(context.Background(), session, overlayCancelCompositor, false)
			return
		case OverlayLifecycleError:
			if event.Err != nil {
				d.recordOverlayError("lifecycle", event.Err)
			}
			_ = d.finishSession(context.Background(), session, overlayCancelCompositor, false)
			return
		}
	}
}

func (d *layerShellOverlayDriver) reconfigureActiveSession(session *overlaySession, monitor Monitor) error {
	d.mu.Lock()
	if d.session != session {
		d.mu.Unlock()
		return nil
	}
	session.monitor = monitor
	d.mu.Unlock()
	return d.configureAndRender(session.ctx, session, monitor)
}

func (d *layerShellOverlayDriver) configureAndRender(ctx context.Context, session *overlaySession, monitor Monitor) error {
	if err := session.surface.Configure(ctx, monitor.LogicalWidth, monitor.LogicalHeight, monitor.Scale); err != nil {
		return err
	}
	buffer, err := d.renderer.RenderMainGrid(monitor, d.config.Grid.Size)
	if err != nil {
		return err
	}
	return session.surface.Render(ctx, buffer)
}

func (d *layerShellOverlayDriver) finishActiveSession(ctx context.Context, reason overlayCancelReason, unmap bool) error {
	d.mu.Lock()
	session := d.session
	d.mu.Unlock()
	return d.finishSession(ctx, session, reason, unmap)
}

func (d *layerShellOverlayDriver) finishSession(ctx context.Context, session *overlaySession, reason overlayCancelReason, unmap bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if session == nil {
		return nil
	}

	d.mu.Lock()
	if d.session != session {
		d.mu.Unlock()
		return nil
	}
	d.session = nil
	d.mu.Unlock()

	session.cancel()
	d.trace.Record(traceOverlayDestroy, map[string]any{
		"session_id": session.id,
		"reason":     string(reason),
	})
	return destroyOverlaySession(ctx, session, unmap)
}

func destroyOverlaySession(ctx context.Context, session *overlaySession, unmap bool) error {
	if session == nil {
		return nil
	}
	var errs []error
	if session.keyboard != nil {
		errs = append(errs, session.keyboard.Close())
	}
	if session.lifecycle != nil {
		errs = append(errs, session.lifecycle.Close())
	}
	errs = append(errs, destroyOverlaySurface(ctx, session.surface, unmap))
	return errors.Join(errs...)
}

func destroyOverlaySurface(ctx context.Context, surface OverlaySurface, unmap bool) error {
	if surface == nil {
		return nil
	}
	var errs []error
	if unmap {
		errs = append(errs, surface.Unmap(ctx))
	}
	errs = append(errs, surface.Destroy(ctx))
	return errors.Join(errs...)
}

func (d *layerShellOverlayDriver) recordOverlayError(source string, err error) {
	if err == nil {
		return
	}
	d.trace.Record(traceOverlayError, map[string]any{
		"source": source,
		"error":  err.Error(),
	})
}

type overlayEventQueue[T any] struct {
	mu     sync.Mutex
	events []T
	wait   chan struct{}
	closed bool
	err    error
}

func newOverlayEventQueue[T any]() *overlayEventQueue[T] {
	return &overlayEventQueue[T]{wait: make(chan struct{})}
}

func (q *overlayEventQueue[T]) push(event T) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed || q.err != nil {
		return
	}
	q.events = append(q.events, event)
	q.wakeLocked()
}

func (q *overlayEventQueue[T]) fatal(err error) {
	if err == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.err == nil {
		q.err = err
	}
	q.wakeLocked()
}

func (q *overlayEventQueue[T]) close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.wakeLocked()
	return nil
}

func (q *overlayEventQueue[T]) pop(ctx context.Context, pump func(context.Context) error) (T, error) {
	var zero T
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		q.mu.Lock()
		if len(q.events) > 0 {
			event := q.events[0]
			copy(q.events, q.events[1:])
			var empty T
			q.events[len(q.events)-1] = empty
			q.events = q.events[:len(q.events)-1]
			q.mu.Unlock()
			return event, nil
		}
		if q.err != nil {
			err := q.err
			q.mu.Unlock()
			return zero, err
		}
		if q.closed {
			q.mu.Unlock()
			return zero, io.ErrClosedPipe
		}
		wait := q.wait
		q.mu.Unlock()

		if pump != nil {
			if err := pump(ctx); err != nil {
				return zero, err
			}
			continue
		}

		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-wait:
		}
	}
}

func (q *overlayEventQueue[T]) wakeLocked() {
	close(q.wait)
	q.wait = make(chan struct{})
}
