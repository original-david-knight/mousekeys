package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHarnessFakesAndTraceStubFlow(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock(time.Date(2026, 5, 1, 10, 30, 0, 0, time.UTC))
	focused := fakeFocusedMonitorFixture()
	if focused.X == 0 && focused.Y == 0 {
		t.Fatalf("focused fixture must cover a non-zero virtual-layout origin: %+v", focused)
	}
	if focused.Scale == 1.0 {
		t.Fatalf("focused fixture must cover scale != 1.0: %+v", focused)
	}

	var trace bytes.Buffer
	wayland := newFakeWaylandBackend(fakeMonitorFixtures()...)
	renderer := &fakeRendererSink{}
	pointer := &virtualPointerRecorder{}
	keyboard := newFakeKeyboardEventSource(3)
	controller := NewDaemonController(DaemonDeps{
		MonitorLookup: &fakeFocusedMonitorLookup{monitor: focused},
		Overlay:       wayland,
		Renderer:      renderer,
		Pointer:       pointer,
		Clock:         clock,
		Trace:         NewJSONLTraceRecorder(&trace, clock),
	})

	if err := controller.Show(ctx); err != nil {
		t.Fatalf("show overlay with fakes: %v", err)
	}
	if controller.State() != DaemonStateOverlayShown {
		t.Fatalf("state after show = %q, want %q", controller.State(), DaemonStateOverlayShown)
	}

	events, err := keyboard.Events(ctx)
	if err != nil {
		t.Fatalf("keyboard events: %v", err)
	}
	keyboard.Send(KeyboardEvent{Key: "B", Pressed: true, Time: clock.Now()})
	keyboard.Send(KeyboardEvent{Key: "C", Pressed: true, Time: clock.Now()})
	keyboard.Send(KeyboardEvent{Key: "Return", Pressed: true, Time: clock.Now()})

	colEvent := <-events
	rowEvent := <-events
	commitEvent := <-events
	if commitEvent.Key != "Return" {
		t.Fatalf("commit key = %q, want Return", commitEvent.Key)
	}

	point, err := GridCellCenter(focused, 26, keyIndex(t, colEvent.Key), keyIndex(t, rowEvent.Key))
	if err != nil {
		t.Fatalf("grid cell center: %v", err)
	}
	if !focused.ContainsLocal(point) {
		t.Fatalf("point outside focused monitor: %+v monitor=%+v", point, focused)
	}
	virtual := focused.LocalToVirtual(point)
	if virtual == point {
		t.Fatalf("virtual point should include non-zero monitor origin: local=%+v virtual=%+v", point, virtual)
	}

	timeout := clock.After(250 * time.Millisecond)
	clock.Advance(249 * time.Millisecond)
	assertTimerNotFired(t, timeout)

	const groupID = "stub-double-click"
	if err := controller.ClickAt(ctx, point, PointerButtonLeft, 2, groupID); err != nil {
		t.Fatalf("double click through pointer recorder: %v", err)
	}
	if got := pointer.ClickCount(groupID, PointerButtonLeft); got != 2 {
		t.Fatalf("left click count for group %q = %d, want 2", groupID, got)
	}
	for _, event := range pointer.Events() {
		if event.X != point.X || event.Y != point.Y || event.OutputName != focused.Name {
			t.Fatalf("pointer event has wrong target: %+v point=%+v output=%s", event, point, focused.Name)
		}
		if event.GroupID != groupID {
			t.Fatalf("pointer event has wrong group: %+v", event)
		}
		if !event.Time.Equal(clock.Now()) {
			t.Fatalf("pointer event timestamp = %s, want %s", event.Time, clock.Now())
		}
	}
	if got := wayland.Count("surface_create"); got != 1 {
		t.Fatalf("surface_create count after double click = %d, want 1; events=%+v", got, wayland.Events())
	}
	if err := controller.Hide(ctx); err != nil {
		t.Fatalf("hide overlay with fakes: %v", err)
	}
	if controller.State() != DaemonStateInactive {
		t.Fatalf("state after hide = %q, want %q", controller.State(), DaemonStateInactive)
	}
	if got := wayland.Count("destroy"); got != 1 {
		t.Fatalf("destroy count after hide = %d, want 1; events=%+v", got, wayland.Events())
	}

	clock.Advance(time.Millisecond)
	assertTimerFiredAt(t, timeout, clock.Now())

	presentations := renderer.Presentations()
	if len(presentations) != 1 {
		t.Fatalf("renderer presentations = %d, want 1", len(presentations))
	}
	if presentations[0].Width != focused.Width || presentations[0].Height != focused.Height {
		t.Fatalf("renderer buffer geometry = %dx%d, want %dx%d", presentations[0].Width, presentations[0].Height, focused.Width, focused.Height)
	}

	traceEvents := parseTraceLines(t, trace.Bytes())
	assertTraceAction(t, traceEvents, "state", "show_requested")
	assertTraceAction(t, traceEvents, "state", "overlay_shown")
	assertTraceAction(t, traceEvents, "io", "pointer_click")
	assertTraceAction(t, traceEvents, "state", "overlay_hidden")
}

func TestFakeHyprlandIPCResponderFixtures(t *testing.T) {
	responder := newFakeHyprlandIPCResponder(t, fakeMonitorFixtures())
	socketPath := responder.Start()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial fake Hyprland IPC: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("j/monitors")); err != nil {
		t.Fatalf("write fake Hyprland request: %v", err)
	}

	var monitors []struct {
		Name    string  `json:"name"`
		X       int     `json:"x"`
		Y       int     `json:"y"`
		Scale   float64 `json:"scale"`
		Focused bool    `json:"focused"`
	}
	if err := json.NewDecoder(conn).Decode(&monitors); err != nil {
		t.Fatalf("decode fake Hyprland response: %v", err)
	}

	var foundFocused bool
	for _, monitor := range monitors {
		if !monitor.Focused {
			continue
		}
		foundFocused = true
		if monitor.X == 0 && monitor.Y == 0 {
			t.Fatalf("focused fake monitor lacks non-zero origin: %+v", monitor)
		}
		if monitor.Scale == 1.0 {
			t.Fatalf("focused fake monitor lacks scale != 1.0: %+v", monitor)
		}
	}
	if !foundFocused {
		t.Fatalf("fake Hyprland response did not include a focused monitor: %+v", monitors)
	}
}

func TestARGBSnapshotHashStable(t *testing.T) {
	buffer, err := NewARGBBuffer(2, 2)
	if err != nil {
		t.Fatalf("new ARGB buffer: %v", err)
	}
	for _, pixel := range []struct {
		x    int
		y    int
		argb uint32
	}{
		{x: 0, y: 0, argb: 0xff000000},
		{x: 1, y: 0, argb: 0xffffffff},
		{x: 0, y: 1, argb: 0x80402010},
		{x: 1, y: 1, argb: 0x00000000},
	} {
		if err := buffer.Set(pixel.x, pixel.y, pixel.argb); err != nil {
			t.Fatalf("set ARGB pixel: %v", err)
		}
	}

	snapshot, err := ARGBSnapshot(buffer)
	if err != nil {
		t.Fatalf("ARGB snapshot: %v", err)
	}
	if got, want := hex.EncodeToString(snapshot), "000000020000000200000002ff000000ffffffff8040201000000000"; got != want {
		t.Fatalf("snapshot hex = %s, want %s", got, want)
	}
	hash, err := ARGBHash(buffer)
	if err != nil {
		t.Fatalf("ARGB hash: %v", err)
	}
	if want := "9daf58a2dcabfb7b64e15f41198f90b215d59df6a396578e4e198c9fa4245450"; hash != want {
		t.Fatalf("ARGB hash = %s, want %s", hash, want)
	}
}

func TestTraceRecorderFromEnvWritesJSONL(t *testing.T) {
	tracePath := filepath.Join(t.TempDir(), "mousekeys.jsonl")
	t.Setenv("MOUSEKEYS_TRACE_JSONL", tracePath)
	clock := newFakeClock(time.Date(2026, 5, 1, 11, 0, 0, 0, time.UTC))

	recorder, closer, err := newTraceRecorderFromEnv(clock)
	if err != nil {
		t.Fatalf("trace recorder from env: %v", err)
	}
	recorder.Record("state", "unit_test", map[string]any{"value": 7})
	if err := closer.Close(); err != nil {
		t.Fatalf("close trace recorder: %v", err)
	}

	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("read trace file: %v", err)
	}
	events := parseTraceLines(t, data)
	assertTraceAction(t, events, "state", "unit_test")
}

func keyIndex(t *testing.T, key string) int {
	t.Helper()
	if len(key) != 1 || key[0] < 'A' || key[0] > 'Z' {
		t.Fatalf("key %q is not an A-Z grid key", key)
	}
	return int(key[0] - 'A')
}

func assertTimerNotFired(t *testing.T, timer Timer) {
	t.Helper()
	select {
	case got := <-timer.C():
		t.Fatalf("timer fired early at %s", got)
	default:
	}
}

func assertTimerFiredAt(t *testing.T, timer Timer, want time.Time) {
	t.Helper()
	select {
	case got := <-timer.C():
		if !got.Equal(want) {
			t.Fatalf("timer fired at %s, want %s", got, want)
		}
	default:
		t.Fatalf("timer did not fire")
	}
}

func parseTraceLines(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	events := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatalf("decode trace line %q: %v", string(line), err)
		}
		events = append(events, event)
	}
	return events
}

func assertTraceAction(t *testing.T, events []map[string]any, kind string, action string) {
	t.Helper()
	for _, event := range events {
		if event["kind"] == kind && event["action"] == action {
			return
		}
	}
	t.Fatalf("trace action %s/%s not found in %+v", kind, action, events)
}
