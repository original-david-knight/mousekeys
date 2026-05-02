package main

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestVirtualPointerRecorderLeftRightAndDoubleClickOrdering(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock(time.Date(2026, 5, 1, 13, 0, 0, 0, time.UTC))
	output := Monitor{Name: "DP-1", Width: 1920, Height: 1080, Scale: 1}

	recorder := newVirtualPointerRecorder(clock)
	if err := recorder.MoveAbsolute(ctx, 123, 456, output); err != nil {
		t.Fatalf("move absolute: %v", err)
	}
	if err := recorder.LeftClick(ctx); err != nil {
		t.Fatalf("left click: %v", err)
	}
	if err := recorder.RightClick(ctx); err != nil {
		t.Fatalf("right click: %v", err)
	}
	if err := recorder.DoubleClick(ctx); err != nil {
		t.Fatalf("double click: %v", err)
	}

	want := []recordedPointerEvent{
		{Kind: "motion", OutputName: "DP-1", X: 123, Y: 456, ProtocolX: 123, ProtocolY: 456, XExtent: 1920, YExtent: 1080, Mapping: PointerMappingWithOutput, Time: clock.Now()},
		{Kind: "frame", OutputName: "DP-1", X: 123, Y: 456, ProtocolX: 123, ProtocolY: 456, XExtent: 1920, YExtent: 1080, Mapping: PointerMappingWithOutput, Time: clock.Now()},
		{Kind: "button", OutputName: "DP-1", X: 123, Y: 456, Button: PointerButtonLeft, State: ButtonDown, Time: clock.Now()},
		{Kind: "button", OutputName: "DP-1", X: 123, Y: 456, Button: PointerButtonLeft, State: ButtonUp, Time: clock.Now()},
		{Kind: "frame", OutputName: "DP-1", X: 123, Y: 456, Time: clock.Now()},
		{Kind: "button", OutputName: "DP-1", X: 123, Y: 456, Button: PointerButtonRight, State: ButtonDown, Time: clock.Now()},
		{Kind: "button", OutputName: "DP-1", X: 123, Y: 456, Button: PointerButtonRight, State: ButtonUp, Time: clock.Now()},
		{Kind: "frame", OutputName: "DP-1", X: 123, Y: 456, Time: clock.Now()},
		{Kind: "button", OutputName: "DP-1", X: 123, Y: 456, Button: PointerButtonLeft, State: ButtonDown, Time: clock.Now()},
		{Kind: "button", OutputName: "DP-1", X: 123, Y: 456, Button: PointerButtonLeft, State: ButtonUp, Time: clock.Now()},
		{Kind: "frame", OutputName: "DP-1", X: 123, Y: 456, Time: clock.Now()},
		{Kind: "button", OutputName: "DP-1", X: 123, Y: 456, Button: PointerButtonLeft, State: ButtonDown, Time: clock.Now()},
		{Kind: "button", OutputName: "DP-1", X: 123, Y: 456, Button: PointerButtonLeft, State: ButtonUp, Time: clock.Now()},
		{Kind: "frame", OutputName: "DP-1", X: 123, Y: 456, Time: clock.Now()},
	}
	if got := recorder.Events(); !reflect.DeepEqual(got, want) {
		t.Fatalf("recorded pointer events mismatch\ngot:  %+v\nwant: %+v", got, want)
	}
}

func TestVirtualPointerRecorderFallbackAppliesOutputOrigin(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock(time.Date(2026, 5, 1, 13, 30, 0, 0, time.UTC))
	layout := Rect{X: 0, Y: -120, Width: 3520, Height: 1200}
	output := Monitor{Name: "eDP-1", X: 1920, Y: -120, Width: 1600, Height: 900, Scale: 1.25}
	recorder := newFallbackVirtualPointerRecorder(clock, layout)

	if err := recorder.MoveAbsolute(ctx, 50, 75, output); err != nil {
		t.Fatalf("fallback move absolute: %v", err)
	}

	events := recorder.Events()
	if len(events) != 2 {
		t.Fatalf("events = %+v, want motion and frame", events)
	}
	if motion := events[0]; motion.ProtocolX != 1970 || motion.ProtocolY != 75 || motion.XExtent != 3520 || motion.YExtent != 1200 || motion.Mapping != PointerMappingFallback {
		t.Fatalf("fallback motion = %+v, want output-origin adjusted protocol coords", motion)
	}
}

func TestWaylandVirtualPointerUsesCreateWithOutputWhenAvailable(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock(time.Date(2026, 5, 1, 14, 0, 0, 0, time.UTC))
	responder := newFakeWaylandProtocolResponder(t)
	socketPath := responder.Start()

	client, err := OpenWaylandClient(ctx, socketPath)
	if err != nil {
		t.Fatalf("open Wayland client: %v", err)
	}
	defer client.Close()

	output, _, err := client.FocusedOutput(ctx, &fakeFocusedMonitorLookup{monitor: Monitor{Name: "eDP-1"}})
	if err != nil {
		t.Fatalf("focused output: %v", err)
	}
	pointer := NewWaylandVirtualPointerSynthesizer(client, clock)
	if err := pointer.MoveAbsolute(ctx, 300, 200, output); err != nil {
		t.Fatalf("move absolute: %v", err)
	}
	if err := pointer.LeftClick(ctx); err != nil {
		t.Fatalf("left click: %v", err)
	}

	waitForFakeWaylandRequestPrefixCount(t, responder, "zwlr_virtual_pointer_v1.frame", 2)
	relevant := virtualPointerRuntimeRequests(responder.Requests())
	want := []string{
		"zwlr_virtual_pointer_manager_v1.create_virtual_pointer_with_output:output=eDP-1",
		"zwlr_virtual_pointer_v1.motion_absolute:output=eDP-1,x=300,y=200,x_extent=1600,y_extent=900",
		"zwlr_virtual_pointer_v1.frame:output=eDP-1",
		"zwlr_virtual_pointer_v1.button:output=eDP-1,button=272,state=1",
		"zwlr_virtual_pointer_v1.button:output=eDP-1,button=272,state=0",
		"zwlr_virtual_pointer_v1.frame:output=eDP-1",
	}
	if !reflect.DeepEqual(relevant, want) {
		t.Fatalf("virtual pointer requests mismatch\ngot:  %+v\nwant: %+v\nall:  %+v", relevant, want, responder.Requests())
	}
	if !fakeWaylandRequestsContain(responder.Requests(), "wl_registry.bind:"+waylandInterfaceVirtualPointerManager) {
		t.Fatalf("client did not bind %s: %+v", waylandInterfaceVirtualPointerManager, responder.Requests())
	}
}

func TestWaylandVirtualPointerV1FallbackAppliesFocusedOutputOrigin(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock(time.Date(2026, 5, 1, 14, 15, 0, 0, time.UTC))
	responder := newFakeWaylandProtocolResponder(t)
	responder.virtualPointerManagerVersion = 1
	socketPath := responder.Start()

	client, err := OpenWaylandClient(ctx, socketPath)
	if err != nil {
		t.Fatalf("open Wayland client with virtual pointer v1: %v", err)
	}
	defer client.Close()

	output, _, err := client.FocusedOutput(ctx, &fakeFocusedMonitorLookup{monitor: Monitor{Name: "eDP-1"}})
	if err != nil {
		t.Fatalf("focused output: %v", err)
	}
	pointer := NewWaylandVirtualPointerSynthesizer(client, clock)
	if err := pointer.MoveAbsolute(ctx, 50, 75, output); err != nil {
		t.Fatalf("fallback move absolute: %v", err)
	}
	if err := pointer.DoubleClick(ctx); err != nil {
		t.Fatalf("double click: %v", err)
	}

	waitForFakeWaylandRequestPrefixCount(t, responder, "zwlr_virtual_pointer_v1.frame", 3)
	relevant := virtualPointerRuntimeRequests(responder.Requests())
	want := []string{
		"zwlr_virtual_pointer_manager_v1.create_virtual_pointer",
		"zwlr_virtual_pointer_v1.motion_absolute:output=,x=1970,y=75,x_extent=3520,y_extent=1200",
		"zwlr_virtual_pointer_v1.frame:output=",
		"zwlr_virtual_pointer_v1.button:output=,button=272,state=1",
		"zwlr_virtual_pointer_v1.button:output=,button=272,state=0",
		"zwlr_virtual_pointer_v1.frame:output=",
		"zwlr_virtual_pointer_v1.button:output=,button=272,state=1",
		"zwlr_virtual_pointer_v1.button:output=,button=272,state=0",
		"zwlr_virtual_pointer_v1.frame:output=",
	}
	if !reflect.DeepEqual(relevant, want) {
		t.Fatalf("fallback virtual pointer requests mismatch\ngot:  %+v\nwant: %+v\nall:  %+v", relevant, want, responder.Requests())
	}
}

func TestPointerDependenciesDoNotUseYdotoolOrUinput(t *testing.T) {
	for _, path := range []string{"go.mod", "go.sum"} {
		data := readFileForTest(t, path)
		for _, forbidden := range []string{"ydotool", "uinput"} {
			if strings.Contains(string(data), forbidden) {
				t.Fatalf("%s contains forbidden pointer synthesis dependency %q", path, forbidden)
			}
		}
	}
}

func virtualPointerRuntimeRequests(requests []string) []string {
	var out []string
	for _, request := range requests {
		if strings.HasPrefix(request, "zwlr_virtual_pointer_manager_v1.create_virtual_pointer") ||
			strings.HasPrefix(request, "zwlr_virtual_pointer_v1.") {
			out = append(out, request)
		}
	}
	return out
}

func waitForFakeWaylandRequestPrefixCount(t *testing.T, responder *fakeWaylandProtocolResponder, prefix string, count int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got := 0
		for _, request := range responder.Requests() {
			if strings.HasPrefix(request, prefix) {
				got++
			}
		}
		if got >= count {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d requests with prefix %q; got %+v", count, prefix, responder.Requests())
}

func readFileForTest(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}
