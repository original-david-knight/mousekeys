package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMonitorFixturesGeometryAndServiceStatus(t *testing.T) {
	fixtures := focusedMonitorFixtures()
	if len(fixtures) < 3 {
		t.Fatalf("focused monitor fixtures = %d, want at least 3", len(fixtures))
	}

	var hasScaled, hasNonZeroOrigin, hasNegativeOrigin bool
	for _, monitor := range fixtures {
		if monitor.Scale != 1.0 {
			hasScaled = true
		}
		if monitor.OriginX != 0 || monitor.OriginY != 0 {
			hasNonZeroOrigin = true
		}
		if monitor.OriginX < 0 || monitor.OriginY < 0 {
			hasNegativeOrigin = true
		}
	}
	if !hasScaled || !hasNonZeroOrigin || !hasNegativeOrigin {
		t.Fatalf("fixtures missing required coverage: scaled=%v non_zero_origin=%v negative_origin=%v", hasScaled, hasNonZeroOrigin, hasNegativeOrigin)
	}

	lookup := newFakeHyprlandIPC(fixtures[1])
	monitor, err := lookup.FocusedMonitor(context.Background())
	if err != nil {
		t.Fatalf("FocusedMonitor returned error: %v", err)
	}
	grid, err := NewGridGeometry(monitor, 26)
	if err != nil {
		t.Fatalf("NewGridGeometry returned error: %v", err)
	}
	localX, localY, err := grid.CellCenterLocal(12, 10)
	if err != nil {
		t.Fatalf("CellCenterLocal returned error: %v", err)
	}
	virtualX, virtualY, err := grid.CellCenterVirtual(12, 10)
	if err != nil {
		t.Fatalf("CellCenterVirtual returned error: %v", err)
	}
	if virtualX != localX+float64(monitor.OriginX) || virtualY != localY+float64(monitor.OriginY) {
		t.Fatalf("virtual center (%v,%v) does not include origin (%d,%d) from local (%v,%v)", virtualX, virtualY, monitor.OriginX, monitor.OriginY, localX, localY)
	}

	service := fakeServiceStatusProvider{status: InstalledServiceStatus{
		UnitName:   "mousekeys.service",
		Active:     true,
		PID:        4242,
		Executable: "/usr/bin/mousekeys",
		Build:      currentBuildInfo(),
		Details:    map[string]string{"source": "fake-systemd"},
	}}
	status, err := service.InstalledServiceStatus(context.Background())
	if err != nil {
		t.Fatalf("InstalledServiceStatus returned error: %v", err)
	}
	if !status.Active || status.PID != 4242 || status.UnitName != "mousekeys.service" {
		t.Fatalf("unexpected service status: %+v", status)
	}
}

func TestKeyboardFakeLifecycleKeymapRepeatAndReuse(t *testing.T) {
	var traceBytes bytes.Buffer
	trace := NewTraceRecorder(&traceBytes, fixedTraceClock(time.Unix(10, 0)))
	keyboard := newFakeKeyboardEventSource(trace)

	if got, err := (KeyboardKeymapFD{Data: []byte("xxlayoutyy"), Offset: 2, Size: 6}).Bytes(); err != nil || string(got) != "layout" {
		t.Fatalf("keymap offset read = %q, %v; want layout, nil", string(got), err)
	}
	if _, err := (KeyboardKeymapFD{Data: []byte("abc"), Offset: 4, Size: 1}).Bytes(); !errors.Is(err, io.EOF) {
		t.Fatalf("keymap offset past EOF error = %v, want io.EOF", err)
	}
	if _, err := (KeyboardKeymapFD{Data: []byte("abc"), Offset: 2, Size: 5}).Bytes(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("keymap short read error = %v, want io.ErrUnexpectedEOF", err)
	}

	keyboard.BeginShow()
	keyboard.Enqueue(
		KeyboardEvent{Kind: KeyboardEventKeymap, Keymap: &KeyboardKeymapFD{Data: []byte("xkeymapx"), Offset: 1, Size: 6}},
		KeyboardEvent{Kind: KeyboardEventEnter},
		KeyboardEvent{Kind: KeyboardEventModifiers, Modifiers: ModifierState{Shift: true}},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "space", State: KeyPressed, Modifiers: ModifierState{Shift: true}},
		KeyboardEvent{Kind: KeyboardEventRepeat, RepeatRate: 30, RepeatDelay: 500 * time.Millisecond},
		KeyboardEvent{Kind: KeyboardEventLeave},
	)
	keyboard.BeginShow()
	keyboard.Enqueue(
		KeyboardEvent{Kind: KeyboardEventEnter},
		KeyboardEvent{Kind: KeyboardEventDestroy},
	)

	var state KeyboardSessionState
	for {
		event, err := keyboard.NextKeyboardEvent(context.Background())
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("NextKeyboardEvent returned error: %v", err)
		}
		if err := state.Apply(event); err != nil {
			t.Fatalf("Apply(%s) returned error: %v", event.Kind, err)
		}
		if event.Kind == KeyboardEventKey && event.Key == "space" && !event.Modifiers.Shift {
			t.Fatalf("Shift-space chord lost modifiers: %+v", event)
		}
		if event.Kind == KeyboardEventLeave && !state.Modifiers.Empty() {
			t.Fatalf("leave did not reset modifiers: %+v", state.Modifiers)
		}
	}
	if keyboard.ShowCount() != 2 {
		t.Fatalf("show reuse count = %d, want 2", keyboard.ShowCount())
	}
	if !state.Destroyed || state.Entered {
		t.Fatalf("destroy did not reset keyboard state: %+v", state)
	}

	events := decodeTraceEvents(t, traceBytes.String())
	assertTraceContains(t, events, traceKeyboardKeymap, traceKeyboardEnter, traceKeyboardModifiers, traceKeyboardKey, traceKeyboardRepeat, traceKeyboardLeave, traceKeyboardDestroy)
}

func TestRendererSnapshotHashAndWaylandPremultiplication(t *testing.T) {
	straight := StraightARGB(128, 255, 64, 32)
	if IsPremultipliedARGB(straight) {
		t.Fatalf("straight ARGB pixel %#08x unexpectedly looks premultiplied", uint32(straight))
	}

	snapshot, err := NewARGBSnapshot(2, 1, []ARGBPixel{straight, StraightARGB(255, 10, 20, 30)})
	if err != nil {
		t.Fatalf("NewARGBSnapshot returned error: %v", err)
	}
	same, err := NewARGBSnapshot(2, 1, []ARGBPixel{straight, StraightARGB(255, 10, 20, 30)})
	if err != nil {
		t.Fatalf("NewARGBSnapshot returned error: %v", err)
	}
	changed, err := NewARGBSnapshot(2, 1, []ARGBPixel{straight, StraightARGB(255, 10, 20, 31)})
	if err != nil {
		t.Fatalf("NewARGBSnapshot returned error: %v", err)
	}
	if snapshot.StraightHash() != same.StraightHash() {
		t.Fatal("same straight ARGB pixels produced different hashes")
	}
	if snapshot.StraightHash() == changed.StraightHash() {
		t.Fatal("changed straight ARGB pixels produced same hash")
	}

	upload := snapshot.PremultipliedForWayland()
	if upload[0] == straight {
		t.Fatalf("premultiplied upload pixel equals straight source: %#08x", uint32(upload[0]))
	}
	if !IsPremultipliedARGB(upload[0]) {
		t.Fatalf("upload pixel %#08x is not premultiplied", uint32(upload[0]))
	}
	if upload[0].A() != straight.A() {
		t.Fatalf("premultiply changed alpha from %d to %d", straight.A(), upload[0].A())
	}
}

func TestTraceRecorderFromEnvironment(t *testing.T) {
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace.jsonl")
	var stdout, stderr bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	env := mapEnv(map[string]string{
		"XDG_RUNTIME_DIR":             dir,
		"XDG_CONFIG_HOME":             t.TempDir(),
		"WAYLAND_DISPLAY":             "wayland-test",
		"HYPRLAND_INSTANCE_SIGNATURE": "trace-test",
		traceEnvVar:                   tracePath,
	})

	go func() {
		done <- run(ctx, []string{"daemon"}, &stdout, &stderr, env)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("daemon returned %d; stderr=%q", code, stderr.String())
		}
	case <-time.After(time.Second):
		t.Fatal("daemon did not stop")
	}

	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("ReadFile(%s) returned error: %v", tracePath, err)
	}
	events := decodeTraceEvents(t, string(data))
	assertTraceContains(t, events, traceDaemonStart, traceDaemonStop)
}

func TestStubEndToEndHarnessRecordsTraceAndOrdering(t *testing.T) {
	var traceBytes bytes.Buffer
	start := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	trace := NewTraceRecorder(&traceBytes, nil)
	clock := newFakeClock(start, trace)
	trace.now = clock.Now

	ctx := context.Background()
	fixtures := focusedMonitorFixtures()
	hyprland := newFakeHyprlandIPC(fixtures[0])
	wayland := newFakeWaylandBackend(trace)
	pointer := newPointerRecorder(trace)
	renderer := newFakeRendererBufferSink(trace)

	state := struct {
		active     bool
		phase      string
		coordinate string
		monitor    Monitor
		stayActive bool
	}{
		stayActive: true,
	}

	monitor, err := hyprland.FocusedMonitor(ctx)
	if err != nil {
		t.Fatalf("FocusedMonitor returned error: %v", err)
	}
	state.active = true
	state.phase = "main_grid"
	state.monitor = monitor

	grid, err := NewGridGeometry(monitor, 26)
	if err != nil {
		t.Fatalf("NewGridGeometry returned error: %v", err)
	}
	mainGrid := mustSnapshot(t, StraightARGB(96, 30, 160, 220), StraightARGB(96, 20, 90, 150))
	selectedCell := mustSnapshot(t, StraightARGB(120, 80, 220, 160), StraightARGB(80, 10, 60, 40))
	mainHash := mainGrid.StraightHash()

	surface, err := wayland.CreateSurface(ctx, monitor)
	if err != nil {
		t.Fatalf("CreateSurface returned error: %v", err)
	}
	if err := surface.Configure(ctx, monitor.LogicalWidth, monitor.LogicalHeight, monitor.Scale); err != nil {
		t.Fatalf("Configure returned error: %v", err)
	}
	if err := surface.Render(ctx, mainGrid); err != nil {
		t.Fatalf("Render main grid returned error: %v", err)
	}
	if err := renderer.UploadARGB(ctx, "layer-surface", mainGrid); err != nil {
		t.Fatalf("UploadARGB returned error: %v", err)
	}
	keyboard, err := surface.GrabKeyboard(ctx)
	if err != nil {
		t.Fatalf("GrabKeyboard returned error: %v", err)
	}

	fakeKeyboard := wayland.keyboard
	fakeKeyboard.Enqueue(
		KeyboardEvent{Kind: KeyboardEventKeymap, Keymap: &KeyboardKeymapFD{Data: []byte("xstub-keymapx"), Offset: 1, Size: 11}},
		KeyboardEvent{Kind: KeyboardEventEnter},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "M", State: KeyPressed},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "M", State: KeyReleased},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "K", State: KeyPressed},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "K", State: KeyReleased},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "space", State: KeyPressed},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "space", State: KeyReleased},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "space", State: KeyPressed},
		KeyboardEvent{Kind: KeyboardEventKey, Key: "space", State: KeyReleased},
		KeyboardEvent{Kind: KeyboardEventLeave},
	)

	var keyboardState KeyboardSessionState
	doubleClickTimer := clock.NewTimer(250 * time.Millisecond)
	if !doubleClickTimer.Stop() {
		t.Fatal("new double-click timer should have stopped as active")
	}
	repeatTimer := clock.NewTimer(350 * time.Millisecond)
	repeatTimer.Reset(50 * time.Millisecond)
	clock.Advance(50 * time.Millisecond)
	select {
	case <-repeatTimer.C():
	default:
		t.Fatal("held-direction repeat timer did not fire after fake clock advance")
	}

	doubleClickReady := false
	spacePresses := 0
	for {
		event, err := keyboard.NextKeyboardEvent(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("NextKeyboardEvent returned error: %v", err)
		}
		if err := keyboardState.Apply(event); err != nil {
			t.Fatalf("Apply(%s) returned error: %v", event.Kind, err)
		}
		if event.Kind == KeyboardEventKey && event.State == KeyPressed && len(event.Key) == 1 {
			state.coordinate += strings.ToUpper(event.Key)
			if len(state.coordinate) == 2 {
				state.phase = "selected_cell"
				if err := surface.Render(ctx, selectedCell); err != nil {
					t.Fatalf("Render selected cell returned error: %v", err)
				}
				x, y, err := grid.CellCenterVirtual(12, 10)
				if err != nil {
					t.Fatalf("CellCenterVirtual returned error: %v", err)
				}
				position := PointerPosition{X: x, Y: y, OutputName: monitor.Name}
				if err := pointer.MovePointer(ctx, PointerMotion{Position: position, At: clock.Now()}); err != nil {
					t.Fatalf("MovePointer returned error: %v", err)
				}
			}
		}
		if event.Kind == KeyboardEventKey && event.Key == "space" && event.State == KeyPressed {
			spacePresses++
			switch spacePresses {
			case 1:
				if doubleClickTimer.Reset(250 * time.Millisecond) {
					t.Fatalf("double-click timer should be inactive before first reset")
				}
			case 2:
				if !doubleClickTimer.Stop() {
					t.Fatalf("double-click timer should be active before second Space stops it")
				}
				doubleClickReady = true
			}
		}
	}
	if !doubleClickReady {
		t.Fatal("keyboard fake did not deliver second Space for double-click")
	}

	// The second Space completes the double-click before the timeout. The main
	// grid must not be rendered again until both clicks are grouped and emitted.
	if err := surface.Unmap(ctx); err != nil {
		t.Fatalf("Unmap returned error: %v", err)
	}
	position := pointer.Events()[0].Motion.Position
	trace.Record(traceClickGroupStart, map[string]any{"click_group": 1, "click_count": 2, "position": position})
	for click := 1; click <= 2; click++ {
		if err := pointer.Button(ctx, PointerButtonEvent{Button: PointerButtonLeft, State: PointerButtonDown, Position: position, ClickGroup: 1, ClickCount: 2, Sequence: click, At: clock.Now()}); err != nil {
			t.Fatalf("Button down returned error: %v", err)
		}
		if err := pointer.Button(ctx, PointerButtonEvent{Button: PointerButtonLeft, State: PointerButtonUp, Position: position, ClickGroup: 1, ClickCount: 2, Sequence: click, At: clock.Now()}); err != nil {
			t.Fatalf("Button up returned error: %v", err)
		}
		if err := pointer.Frame(ctx, PointerFrame{OutputName: monitor.Name, At: clock.Now()}); err != nil {
			t.Fatalf("Frame returned error: %v", err)
		}
	}
	trace.Record(traceClickGroupComplete, map[string]any{"click_group": 1, "click_count": 2})
	if state.stayActive {
		trace.Record(traceStayActiveReset, map[string]any{"phase": "main_grid", "monitor": monitor.Name})
		state.phase = "main_grid"
		if err := surface.Render(ctx, mainGrid); err != nil {
			t.Fatalf("Render stay-active main grid returned error: %v", err)
		}
	}
	if err := surface.Destroy(ctx); err != nil {
		t.Fatalf("Destroy returned error: %v", err)
	}
	if err := wayland.OutputChanged(ctx, fixtures[2]); err != nil {
		t.Fatalf("OutputChanged returned error: %v", err)
	}
	if err := wayland.Close(ctx); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	if !state.active || state.phase != "main_grid" || state.coordinate != "MK" {
		t.Fatalf("unexpected stub daemon state: %+v", state)
	}
	assertPointerDoubleClick(t, pointer.Events(), position)
	assertWaylandLifecycle(t, wayland.Events())
	assertNoMainGridRenderDuringDoubleClick(t, wayland.Events(), mainHash)

	uploads := renderer.Uploads()
	if len(uploads) != 1 {
		t.Fatalf("renderer uploads = %d, want 1", len(uploads))
	}
	for _, pixel := range uploads[0].PremultipliedPixels {
		if !IsPremultipliedARGB(pixel) {
			t.Fatalf("renderer upload contained non-premultiplied pixel %#08x", uint32(pixel))
		}
	}

	events := decodeTraceEvents(t, traceBytes.String())
	assertTraceContains(t, events,
		traceOverlaySurfaceCreate,
		traceOverlayConfigure,
		traceOverlayRender,
		traceOverlayKeyboardGrab,
		traceOverlayUnmap,
		traceOverlayDestroy,
		traceOverlayOutputChange,
		traceOverlayClose,
		traceKeyboardKeymap,
		traceKeyboardEnter,
		traceKeyboardLeave,
		traceKeyboardKey,
		tracePointerMotion,
		tracePointerButton,
		tracePointerFrame,
		traceTimerCreate,
		traceTimerReset,
		traceTimerStop,
		traceTimerFire,
		traceClickGroupStart,
		traceClickGroupComplete,
		traceStayActiveReset,
	)
}

func mustSnapshot(t *testing.T, pixels ...ARGBPixel) ARGBSnapshot {
	t.Helper()
	snapshot, err := NewARGBSnapshot(len(pixels), 1, pixels)
	if err != nil {
		t.Fatalf("NewARGBSnapshot returned error: %v", err)
	}
	return snapshot
}

func fixedTraceClock(at time.Time) func() time.Time {
	return func() time.Time {
		return at
	}
}

func decodeTraceEvents(t *testing.T, data string) []TraceEvent {
	t.Helper()
	var events []TraceEvent
	scanner := bufio.NewScanner(strings.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event TraceEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("trace line is not valid JSON: %v\n%s", err, line)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("trace scanner returned error: %v", err)
	}
	return events
}

func assertTraceContains(t *testing.T, events []TraceEvent, wants ...string) {
	t.Helper()
	seen := make(map[string]bool)
	for _, event := range events {
		seen[event.Event] = true
	}
	for _, want := range wants {
		if !seen[want] {
			t.Fatalf("trace missing %q; saw %v", want, sortedKeys(seen))
		}
	}
}

func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func assertPointerDoubleClick(t *testing.T, events []recordedPointerEvent, position PointerPosition) {
	t.Helper()
	if len(events) != 7 {
		t.Fatalf("pointer event count = %d, want motion + 2*(down/up/frame)", len(events))
	}
	if events[0].Kind != "motion" {
		t.Fatalf("first pointer event = %s, want motion", events[0].Kind)
	}
	var buttonEvents []PointerButtonEvent
	for _, event := range events {
		if event.Kind == "button" {
			buttonEvents = append(buttonEvents, event.Button)
		}
	}
	if len(buttonEvents) != 4 {
		t.Fatalf("button event count = %d, want 4", len(buttonEvents))
	}
	for i, event := range buttonEvents {
		wantState := PointerButtonDown
		if i%2 == 1 {
			wantState = PointerButtonUp
		}
		if event.State != wantState || event.Button != PointerButtonLeft || event.ClickGroup != 1 || event.ClickCount != 2 {
			t.Fatalf("button event[%d] = %+v", i, event)
		}
		if event.Position != position || event.Position.OutputName == "" {
			t.Fatalf("button event[%d] position = %+v, want %+v with output", i, event.Position, position)
		}
	}
	if buttonEvents[0].Sequence != 1 || buttonEvents[2].Sequence != 2 {
		t.Fatalf("button sequences = %d,%d want 1,2", buttonEvents[0].Sequence, buttonEvents[2].Sequence)
	}
}

func assertWaylandLifecycle(t *testing.T, events []fakeWaylandEvent) {
	t.Helper()
	kinds := make(map[string]bool)
	for _, event := range events {
		kinds[event.Kind] = true
	}
	for _, want := range []string{"surface_create", "configure", "render", "keyboard_grab", "unmap", "destroy", "output_change", "compositor_close"} {
		if !kinds[want] {
			t.Fatalf("Wayland events missing %q: %+v", want, events)
		}
	}
}

func assertNoMainGridRenderDuringDoubleClick(t *testing.T, events []fakeWaylandEvent, mainHash string) {
	t.Helper()
	unmapIndex := -1
	lastMainRenderIndex := -1
	for i, event := range events {
		if event.Kind == "unmap" {
			unmapIndex = i
		}
		if event.Kind == "render" && event.BufferHash == mainHash {
			lastMainRenderIndex = i
		}
	}
	if unmapIndex < 0 {
		t.Fatal("no unmap event recorded")
	}
	if lastMainRenderIndex <= unmapIndex {
		t.Fatalf("main grid did not reopen after double-click completion; unmap=%d last_main=%d", unmapIndex, lastMainRenderIndex)
	}
	for i := unmapIndex + 1; i < lastMainRenderIndex; i++ {
		if events[i].Kind == "render" && events[i].BufferHash == mainHash {
			t.Fatalf("main grid reopened during double-click at event %d", i)
		}
	}
}
