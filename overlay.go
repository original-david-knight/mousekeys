package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

type overlayGridRenderer interface {
	RenderMainGrid(monitor Monitor, gridSize int) (ARGBSnapshot, error)
}

type overlayCoordinateGridRenderer interface {
	RenderCoordinateGrid(monitor Monitor, gridSize int, state CoordinateRenderState) (ARGBSnapshot, error)
}

type overlaySelectedCellRenderer interface {
	RenderSelectedCellOutline(monitor Monitor, cell Rect) (ARGBSnapshot, error)
}

type layerShellOverlayDriver struct {
	mu       sync.Mutex
	monitor  FocusedMonitorLookup
	wayland  WaylandOverlayBackend
	renderer overlayGridRenderer
	pointer  PointerActionSynthesizer
	clock    ClockTimerSource
	config   Config
	trace    *TraceRecorder
	session  *overlaySession
	nextID   int
}

type overlaySession struct {
	id          int
	ctx         context.Context
	cancel      context.CancelFunc
	monitor     Monitor
	surface     OverlaySurface
	keyboard    KeyboardEventSource
	lifecycle   OverlayLifecycleEventSource
	coordinate  coordinateEntryState
	hasSelected bool
	selected    selectedCell
	navigator   hiddenSubcellNavigator
	repeat      *heldDirectionRepeat
	pendingLeft *pendingLeftClick
}

type heldDirectionRepeat struct {
	key       string
	direction hiddenDirection
	timer     TimerHandle
	cancel    chan struct{}
}

type pendingLeftClick struct {
	timer  TimerHandle
	cancel chan struct{}
}

type layerShellOverlayDriverOptions struct {
	Pointer PointerActionSynthesizer
	Clock   ClockTimerSource
}

func newLayerShellOverlayDriver(monitor FocusedMonitorLookup, wayland WaylandOverlayBackend, renderer overlayGridRenderer, config Config, trace *TraceRecorder) (*layerShellOverlayDriver, error) {
	return newLayerShellOverlayDriverWithOptions(monitor, wayland, renderer, config, trace, layerShellOverlayDriverOptions{})
}

func newLayerShellOverlayDriverWithOptions(monitor FocusedMonitorLookup, wayland WaylandOverlayBackend, renderer overlayGridRenderer, config Config, trace *TraceRecorder, opts layerShellOverlayDriverOptions) (*layerShellOverlayDriver, error) {
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
	pointer := opts.Pointer
	if pointer == nil {
		pointer = noopPointerActionSynthesizer{}
	}
	clock := opts.Clock
	if clock == nil {
		clock = realClock{}
	}
	return &layerShellOverlayDriver{
		monitor:  monitor,
		wayland:  wayland,
		renderer: renderer,
		pointer:  pointer,
		clock:    clock,
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

	if err := d.configureAndRender(ctx, session, monitor, session.coordinate.RenderState(d.config.Grid.Size)); err != nil {
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
	if closer, ok := d.pointer.(interface{ Close(context.Context) error }); ok {
		err = errors.Join(err, closer.Close(ctx))
	}
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
		event = translator.LastEvent()
		if event.Kind == KeyboardEventDestroy {
			_ = d.finishSession(context.Background(), session, overlayCancelKeyboardDestroy, true)
			return
		}
		consumed, err := d.handleKeyboardEvent(session, event)
		if err != nil {
			d.recordOverlayError("keyboard", err)
			_ = d.finishSession(context.Background(), session, overlayCancelKeyboardDestroy, true)
			return
		}
		if consumed {
			continue
		}
		if ok {
			d.trace.Record(traceKeyboardToken, map[string]any{
				"kind":    token.Kind,
				"letter":  token.Letter,
				"command": token.Command,
				"chord":   token.Chord.String(),
			})
		}
		if ok {
			if err := d.handleKeyboardToken(session, token); err != nil {
				d.recordOverlayError("coordinate", err)
				_ = d.finishSession(context.Background(), session, overlayCancelKeyboardDestroy, true)
				return
			}
			if token.Kind == KeyboardTokenCommand && token.Command == KeyboardCommandExit {
				return
			}
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
	if session.hasSelected {
		grid, err := NewGridGeometry(monitor, d.config.Grid.Size)
		if err != nil {
			d.mu.Unlock()
			return err
		}
		cell, err := session.coordinate.SelectedCell(grid)
		if err != nil {
			d.mu.Unlock()
			return err
		}
		navigator, err := newHiddenSubcellNavigator(monitor, cell, d.config.Grid.SubgridPixelSize)
		if err != nil {
			d.mu.Unlock()
			return err
		}
		d.stopHeldDirectionRepeatLocked(session)
		session.selected = cell
		session.navigator = navigator
		d.mu.Unlock()
		return d.configureAndRenderSelectedCell(session.ctx, session, monitor, cell.Bounds)
	}
	state := session.coordinate.RenderState(d.config.Grid.Size)
	d.mu.Unlock()
	return d.configureAndRender(session.ctx, session, monitor, state)
}

func (d *layerShellOverlayDriver) configureAndRender(ctx context.Context, session *overlaySession, monitor Monitor, state CoordinateRenderState) error {
	if err := session.surface.Configure(ctx, monitor.LogicalWidth, monitor.LogicalHeight, monitor.Scale); err != nil {
		return err
	}
	buffer, err := d.renderGridBuffer(monitor, state)
	if err != nil {
		return err
	}
	return session.surface.Render(ctx, buffer)
}

func (d *layerShellOverlayDriver) configureAndRenderSelectedCell(ctx context.Context, session *overlaySession, monitor Monitor, cell Rect) error {
	if err := session.surface.Configure(ctx, monitor.LogicalWidth, monitor.LogicalHeight, monitor.Scale); err != nil {
		return err
	}
	buffer, err := d.renderSelectedCellBuffer(monitor, cell)
	if err != nil {
		return err
	}
	return session.surface.Render(ctx, buffer)
}

func (d *layerShellOverlayDriver) renderGridBuffer(monitor Monitor, state CoordinateRenderState) (ARGBSnapshot, error) {
	if renderer, ok := d.renderer.(overlayCoordinateGridRenderer); ok {
		return renderer.RenderCoordinateGrid(monitor, d.config.Grid.Size, state)
	}
	if state.Input != "" || state.HasSelectedColumn {
		return ARGBSnapshot{}, fmt.Errorf("overlay renderer does not support coordinate render state")
	}
	return d.renderer.RenderMainGrid(monitor, d.config.Grid.Size)
}

func (d *layerShellOverlayDriver) renderSelectedCellBuffer(monitor Monitor, cell Rect) (ARGBSnapshot, error) {
	if renderer, ok := d.renderer.(overlaySelectedCellRenderer); ok {
		return renderer.RenderSelectedCellOutline(monitor, cell)
	}
	return ARGBSnapshot{}, fmt.Errorf("overlay renderer does not support selected-cell outline rendering")
}

func (d *layerShellOverlayDriver) handleKeyboardToken(session *overlaySession, token KeyboardInputToken) error {
	switch token.Kind {
	case KeyboardTokenLetter:
		if d.sessionHasPendingLeftClick(session) {
			return nil
		}
		return d.handleCoordinateLetter(session, token.Letter)
	case KeyboardTokenCommand:
		switch token.Command {
		case KeyboardCommandExit:
			return d.finishSession(context.Background(), session, overlayCancelEscape, true)
		case KeyboardCommandBackspace:
			if d.sessionHasPendingLeftClick(session) {
				return nil
			}
			return d.handleCoordinateBackspace(session)
		case KeyboardCommandLeftClick:
			return d.startPendingLeftClick(session)
		case KeyboardCommandDoubleClick:
			return d.commitClick(session, clickCommitDoubleLeft, nil, true)
		case KeyboardCommandRightClick:
			return d.commitClick(session, clickCommitRight, nil, false)
		default:
			return nil
		}
	default:
		return nil
	}
}

type clickCommitKind string

const (
	clickCommitLeft       clickCommitKind = "left"
	clickCommitRight      clickCommitKind = "right"
	clickCommitDoubleLeft clickCommitKind = "double_left"
)

func (d *layerShellOverlayDriver) startPendingLeftClick(session *overlaySession) error {
	d.mu.Lock()
	if d.session != session || !session.hasSelected {
		d.mu.Unlock()
		return nil
	}
	if session.pendingLeft != nil {
		d.mu.Unlock()
		return nil
	}
	d.stopHeldDirectionRepeatLocked(session)
	pending := &pendingLeftClick{
		cancel: make(chan struct{}),
	}
	timer := d.clock.NewTimer(d.config.DoubleClickTimeout())
	pending.timer = timer
	session.pendingLeft = pending
	d.mu.Unlock()

	go d.runPendingLeftClickTimer(session, pending, timer)
	return nil
}

func (d *layerShellOverlayDriver) runPendingLeftClickTimer(session *overlaySession, pending *pendingLeftClick, timer TimerHandle) {
	select {
	case <-pending.cancel:
		return
	case <-timer.C():
		if err := d.commitClick(session, clickCommitLeft, pending, true); err != nil {
			if !errors.Is(err, context.Canceled) {
				d.recordOverlayError("click", err)
			}
		}
	}
}

func (d *layerShellOverlayDriver) commitClick(session *overlaySession, kind clickCommitKind, pending *pendingLeftClick, requirePending bool) error {
	if !d.claimSessionForClick(session, pending, requirePending) {
		return nil
	}

	clickCtx := context.Background()
	if err := destroyOverlaySession(clickCtx, session, true); err != nil {
		return err
	}

	switch kind {
	case clickCommitLeft:
		if err := d.pointer.LeftClick(clickCtx); err != nil {
			return err
		}
	case clickCommitRight:
		if err := d.pointer.RightClick(clickCtx); err != nil {
			return err
		}
	case clickCommitDoubleLeft:
		if err := d.pointer.DoubleClick(clickCtx); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown click commit kind %q", kind)
	}

	if d.config.Behavior.StayActive {
		d.trace.Record(traceStayActiveReset, map[string]any{
			"phase":      "main_grid",
			"session_id": session.id,
			"monitor":    session.monitor.Name,
			"click_kind": string(kind),
		})
		return d.ShowOverlay(clickCtx)
	}
	return nil
}

func (d *layerShellOverlayDriver) claimSessionForClick(session *overlaySession, pending *pendingLeftClick, requirePending bool) bool {
	if session == nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.session != session || !session.hasSelected {
		return false
	}
	if requirePending {
		if session.pendingLeft == nil {
			return false
		}
		if pending != nil && session.pendingLeft != pending {
			return false
		}
	}
	d.stopHeldDirectionRepeatLocked(session)
	d.stopPendingLeftClickLocked(session)
	d.session = nil
	session.cancel()
	d.trace.Record(traceOverlayDestroy, map[string]any{
		"session_id": session.id,
		"reason":     string(overlayCancelClickCommit),
	})
	return true
}

func (d *layerShellOverlayDriver) sessionHasPendingLeftClick(session *overlaySession) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.session == session && session.pendingLeft != nil
}

func (d *layerShellOverlayDriver) handleCoordinateLetter(session *overlaySession, letter string) error {
	d.mu.Lock()
	if d.session != session {
		d.mu.Unlock()
		return nil
	}
	changed, selected := session.coordinate.AddLetter(letter, d.config.Grid.Size)
	if !changed {
		d.mu.Unlock()
		return nil
	}
	monitor := session.monitor
	state := session.coordinate.RenderState(d.config.Grid.Size)
	input := session.coordinate.Input()
	var cell selectedCell
	if selected {
		grid, err := NewGridGeometry(monitor, d.config.Grid.Size)
		if err != nil {
			d.mu.Unlock()
			return err
		}
		cell, err = session.coordinate.SelectedCell(grid)
		if err != nil {
			d.mu.Unlock()
			return err
		}
		navigator, err := newHiddenSubcellNavigator(monitor, cell, d.config.Grid.SubgridPixelSize)
		if err != nil {
			d.mu.Unlock()
			return err
		}
		session.hasSelected = true
		session.selected = cell
		session.navigator = navigator
	}
	d.mu.Unlock()

	d.trace.Record(traceCoordinateInput, map[string]any{
		"input": input,
		"hud":   state.HUDText(),
	})
	if selected {
		d.trace.Record(traceCoordinateSelected, map[string]any{
			"coordinate":       cell.Coordinate,
			"column_letter":    cell.ColumnLetter,
			"row_letter":       cell.RowLetter,
			"column":           cell.Column,
			"row":              cell.Row,
			"bounds":           cell.Bounds,
			"center_local_x":   cell.CenterLocalX,
			"center_local_y":   cell.CenterLocalY,
			"center_virtual_x": cell.CenterVirtualX,
			"center_virtual_y": cell.CenterVirtualY,
			"monitor":          monitor,
			"subcells":         cellSubcellTrace(cell.Bounds, d.config.Grid.SubgridPixelSize),
		})
		if err := d.pointer.MoveAbsolute(session.ctx, cell.CenterLocalX, cell.CenterLocalY, monitor); err != nil {
			return err
		}
		return d.configureAndRenderSelectedCell(session.ctx, session, monitor, cell.Bounds)
	}
	return d.configureAndRender(session.ctx, session, monitor, state)
}

func (d *layerShellOverlayDriver) handleCoordinateBackspace(session *overlaySession) error {
	d.mu.Lock()
	if d.session != session {
		d.mu.Unlock()
		return nil
	}
	if !session.coordinate.Backspace() {
		d.mu.Unlock()
		return nil
	}
	d.stopHeldDirectionRepeatLocked(session)
	session.hasSelected = false
	session.selected = selectedCell{}
	session.navigator = hiddenSubcellNavigator{}
	monitor := session.monitor
	state := session.coordinate.RenderState(d.config.Grid.Size)
	input := session.coordinate.Input()
	d.mu.Unlock()

	d.trace.Record(traceCoordinateInput, map[string]any{
		"input": input,
		"hud":   state.HUDText(),
	})
	return d.configureAndRender(session.ctx, session, monitor, state)
}

func (d *layerShellOverlayDriver) handleKeyboardEvent(session *overlaySession, event KeyboardEvent) (bool, error) {
	switch event.Kind {
	case KeyboardEventKeymap, KeyboardEventLeave:
		d.mu.Lock()
		if d.session == session {
			d.stopHeldDirectionRepeatLocked(session)
			d.stopPendingLeftClickLocked(session)
		}
		d.mu.Unlock()
		return false, nil
	case KeyboardEventKey:
	default:
		return false, nil
	}

	direction, ok := directionFromKeyEvent(event)
	if !ok {
		return false, nil
	}
	d.mu.Lock()
	selected := d.session == session && session.hasSelected
	pendingClick := selected && session.pendingLeft != nil
	d.mu.Unlock()
	if !selected {
		return false, nil
	}
	if pendingClick {
		return true, nil
	}

	switch event.State {
	case KeyPressed:
		if event.Repeated {
			return true, nil
		}
		d.startHeldDirectionRepeat(session, direction, event.Key)
		if err := d.moveHiddenSubcell(session, direction, 1); err != nil {
			d.stopHeldDirectionRepeatForKey(session, event.Key)
			return true, err
		}
		return true, nil
	case KeyReleased:
		d.stopHeldDirectionRepeatForKey(session, event.Key)
		return true, nil
	default:
		return true, nil
	}
}

func (d *layerShellOverlayDriver) moveHiddenSubcell(session *overlaySession, direction hiddenDirection, subcells int) error {
	d.mu.Lock()
	if d.session != session || !session.hasSelected {
		d.mu.Unlock()
		return nil
	}
	x, y, err := session.navigator.Move(direction, subcells)
	monitor := session.monitor
	d.mu.Unlock()
	if err != nil {
		return err
	}
	return d.pointer.MoveAbsolute(session.ctx, x, y, monitor)
}

func (d *layerShellOverlayDriver) startHeldDirectionRepeat(session *overlaySession, direction hiddenDirection, key string) {
	d.mu.Lock()
	if d.session != session || !session.hasSelected {
		d.mu.Unlock()
		return
	}
	d.stopHeldDirectionRepeatLocked(session)
	repeat := &heldDirectionRepeat{
		key:       key,
		direction: direction,
		cancel:    make(chan struct{}),
	}
	session.repeat = repeat
	startedAt := d.clock.Now()
	timer := d.clock.NewTimer(heldDirectionInitialDelay)
	repeat.timer = timer
	d.mu.Unlock()

	go d.runHeldDirectionRepeat(session, repeat, startedAt, timer)
}

func (d *layerShellOverlayDriver) runHeldDirectionRepeat(session *overlaySession, repeat *heldDirectionRepeat, startedAt time.Time, timer TimerHandle) {
	defer timer.Stop()
	for {
		select {
		case <-repeat.cancel:
			return
		case at := <-timer.C():
			select {
			case <-repeat.cancel:
				return
			default:
			}
			stage := heldDirectionRepeatStageForElapsed(at.Sub(startedAt))
			timer.Reset(stage.Interval)
			if err := d.moveHiddenSubcell(session, repeat.direction, stage.Subcells); err != nil {
				if !errors.Is(err, context.Canceled) {
					d.recordOverlayError("held_repeat", err)
				}
				d.stopHeldDirectionRepeat(session, repeat)
				return
			}
		}
	}
}

func (d *layerShellOverlayDriver) stopHeldDirectionRepeatForKey(session *overlaySession, key string) {
	d.mu.Lock()
	if d.session == session && session.repeat != nil && session.repeat.key == key {
		d.stopHeldDirectionRepeatLocked(session)
	}
	d.mu.Unlock()
}

func (d *layerShellOverlayDriver) stopHeldDirectionRepeat(session *overlaySession, repeat *heldDirectionRepeat) {
	d.mu.Lock()
	if d.session == session && session.repeat == repeat {
		d.stopHeldDirectionRepeatLocked(session)
	}
	d.mu.Unlock()
}

func (d *layerShellOverlayDriver) stopHeldDirectionRepeatLocked(session *overlaySession) {
	if session == nil || session.repeat == nil {
		return
	}
	if session.repeat.timer != nil {
		session.repeat.timer.Stop()
	}
	close(session.repeat.cancel)
	session.repeat = nil
}

func (d *layerShellOverlayDriver) stopPendingLeftClickLocked(session *overlaySession) {
	if session == nil || session.pendingLeft == nil {
		return
	}
	pending := session.pendingLeft
	if pending.timer != nil {
		pending.timer.Stop()
	}
	if pending.cancel != nil {
		close(pending.cancel)
	}
	session.pendingLeft = nil
}

func cellSubcellTrace(cell Rect, subgridPixelSize int) map[string]any {
	grid, err := NewHiddenSubcellGeometry(cell, subgridPixelSize)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	return map[string]any{
		"count_x":      grid.CountX,
		"count_y":      grid.CountY,
		"step_x":       grid.StepX,
		"step_y":       grid.StepY,
		"pixel_target": grid.PixelTarget,
	}
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
	d.stopHeldDirectionRepeatLocked(session)
	d.stopPendingLeftClickLocked(session)
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

type noopPointerActionSynthesizer struct{}

func (noopPointerActionSynthesizer) MoveAbsolute(ctx context.Context, x, y float64, output Monitor) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return output.Validate()
}

func (noopPointerActionSynthesizer) LeftClick(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return ctx.Err()
}

func (noopPointerActionSynthesizer) RightClick(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return ctx.Err()
}

func (noopPointerActionSynthesizer) DoubleClick(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return ctx.Err()
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
