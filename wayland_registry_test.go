package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	wlclient "github.com/rajveermalviya/go-wayland/wayland/client"
)

func TestBuildWaylandBindingPlanBindsRequiredGlobals(t *testing.T) {
	plan, err := buildWaylandBindingPlan([]WaylandGlobal{
		{Name: 4, Interface: waylandInterfaceSeat, Version: 7},
		{Name: 1, Interface: waylandInterfaceCompositor, Version: 6},
		{Name: 2, Interface: waylandInterfaceShm, Version: 1},
		{Name: 9, Interface: waylandInterfaceLayerShell, Version: 4},
		{Name: 8, Interface: waylandInterfaceVirtualPointerManager, Version: 2},
		{Name: 12, Interface: waylandInterfaceOutput, Version: 4},
		{Name: 11, Interface: waylandInterfaceOutput, Version: 3},
		{Name: 7, Interface: waylandInterfaceXDGOutputManager, Version: 3},
	})
	if err != nil {
		t.Fatalf("build binding plan: %v", err)
	}

	if plan.Compositor.Global.Name != 1 || plan.Compositor.Version != 6 {
		t.Fatalf("compositor binding = %+v, want global 1 v6", plan.Compositor)
	}
	if plan.VirtualPointerManager.Global.Name != 8 || plan.VirtualPointerManager.Version != 2 {
		t.Fatalf("virtual pointer binding = %+v, want global 8 v2", plan.VirtualPointerManager)
	}
	if !plan.XDGOutputManager.Valid() || plan.XDGOutputManager.Version != 3 {
		t.Fatalf("xdg output manager binding = %+v, want valid v3", plan.XDGOutputManager)
	}
	if len(plan.Outputs) != 2 {
		t.Fatalf("outputs = %+v, want two outputs", plan.Outputs)
	}
	if plan.Outputs[0].Global.Name != 11 || plan.Outputs[0].Version != 3 {
		t.Fatalf("first output binding = %+v, want global 11 v3", plan.Outputs[0])
	}
	if plan.Outputs[1].Global.Name != 12 || plan.Outputs[1].Version != 4 {
		t.Fatalf("second output binding = %+v, want global 12 v4", plan.Outputs[1])
	}
}

func TestBuildWaylandBindingPlanReportsMissingRequiredGlobal(t *testing.T) {
	_, err := buildWaylandBindingPlan([]WaylandGlobal{
		{Name: 1, Interface: waylandInterfaceCompositor, Version: 6},
		{Name: 2, Interface: waylandInterfaceShm, Version: 1},
		{Name: 3, Interface: waylandInterfaceSeat, Version: 7},
		{Name: 4, Interface: waylandInterfaceOutput, Version: 4},
		{Name: 5, Interface: waylandInterfaceLayerShell, Version: 4},
	})
	if err == nil {
		t.Fatalf("binding plan succeeded without virtual pointer manager")
	}
	for _, want := range []string{"missing required globals", waylandInterfaceVirtualPointerManager} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want substring %q", err.Error(), want)
		}
	}
}

func TestBuildWaylandBindingPlanAcceptsVirtualPointerV1ForFallback(t *testing.T) {
	plan, err := buildWaylandBindingPlan([]WaylandGlobal{
		{Name: 1, Interface: waylandInterfaceCompositor, Version: 6},
		{Name: 2, Interface: waylandInterfaceShm, Version: 1},
		{Name: 3, Interface: waylandInterfaceSeat, Version: 7},
		{Name: 4, Interface: waylandInterfaceOutput, Version: 4},
		{Name: 5, Interface: waylandInterfaceLayerShell, Version: 4},
		{Name: 6, Interface: waylandInterfaceVirtualPointerManager, Version: 1},
	})
	if err != nil {
		t.Fatalf("binding plan rejected virtual pointer manager v1 fallback: %v", err)
	}
	if plan.VirtualPointerManager.Global.Name != 6 || plan.VirtualPointerManager.Version != 1 {
		t.Fatalf("virtual pointer binding = %+v, want global 6 v1", plan.VirtualPointerManager)
	}
}

func TestWaylandOutputStateUsesXDGLogicalGeometryAndName(t *testing.T) {
	state := newWaylandOutputState(waylandGlobalBinding{
		Global:  WaylandGlobal{Name: 12, Interface: waylandInterfaceOutput, Version: 4},
		Version: 4,
	})
	state.applyWLGeometry(0, 0, 0)
	state.applyWLMode(waylandOutputModeCurrent, 2000, 1125)
	state.applyWLScale(2)
	state.applyWLName("wl-fallback")
	state.applyXDGLogicalPosition(1920, -120)
	state.applyXDGLogicalSize(1600, 900)
	state.applyXDGName("eDP-1")

	monitor, err := state.monitor()
	if err != nil {
		t.Fatalf("monitor from output state: %v", err)
	}
	want := Monitor{Name: "eDP-1", X: 1920, Y: -120, Width: 1600, Height: 900, Scale: 1.25}
	if monitor != want {
		t.Fatalf("monitor = %+v, want %+v", monitor, want)
	}
}

func TestWaylandOutputStateFallsBackToWLOutputV4NameAndScale(t *testing.T) {
	state := newWaylandOutputState(waylandGlobalBinding{
		Global:  WaylandGlobal{Name: 9, Interface: waylandInterfaceOutput, Version: 4},
		Version: 4,
	})
	state.applyWLGeometry(-1920, 240, 0)
	state.applyWLMode(waylandOutputModeCurrent, 3840, 2160)
	state.applyWLScale(2)
	state.applyWLName("HDMI-A-1")

	monitor, err := state.monitor()
	if err != nil {
		t.Fatalf("monitor from wl_output state: %v", err)
	}
	want := Monitor{Name: "HDMI-A-1", X: -1920, Y: 240, Width: 1920, Height: 1080, Scale: 2.0}
	if monitor != want {
		t.Fatalf("monitor = %+v, want %+v", monitor, want)
	}
}

func TestWaylandOutputStateRequiresOutputName(t *testing.T) {
	state := newWaylandOutputState(waylandGlobalBinding{
		Global:  WaylandGlobal{Name: 9, Interface: waylandInterfaceOutput, Version: 3},
		Version: 3,
	})
	state.applyWLGeometry(0, 0, 0)
	state.applyWLMode(waylandOutputModeCurrent, 1920, 1080)
	state.applyWLScale(1)

	_, err := state.monitor()
	if err == nil {
		t.Fatalf("monitor succeeded without wl_output v4 or xdg output name")
	}
	if !strings.Contains(err.Error(), "did not advertise a name") {
		t.Fatalf("error = %q, want missing name context", err.Error())
	}
}

func TestMatchWaylandOutputByNameUsesHyprlandFocusedMonitorName(t *testing.T) {
	focused := Monitor{Name: "eDP-1", X: 1920, Y: -120, Width: 1706, Height: 960, Scale: 1.25, Focused: true}
	outputs := []Monitor{
		{Name: "DP-1", X: 0, Y: 0, Width: 1920, Height: 1080, Scale: 1.0},
		{Name: "eDP-1", X: 1920, Y: -120, Width: 1706, Height: 960, Scale: 1.25},
	}

	matched, err := MatchWaylandOutputByName(outputs, focused)
	if err != nil {
		t.Fatalf("match output by name: %v", err)
	}
	if matched.Name != focused.Name || !matched.Focused {
		t.Fatalf("matched output = %+v, want focused name %q", matched, focused.Name)
	}
}

func TestMatchWaylandOutputByNameReportsEmptyFocusedName(t *testing.T) {
	_, err := MatchWaylandOutputByName([]Monitor{{Name: "DP-1"}}, Monitor{})
	if err == nil {
		t.Fatalf("match output succeeded with empty focused monitor name")
	}
	if !strings.Contains(err.Error(), "focused monitor name is required") {
		t.Fatalf("error = %q, want focused-name context", err.Error())
	}
}

func TestMatchWaylandOutputByNameReportsAvailableNames(t *testing.T) {
	_, err := MatchWaylandOutputByName([]Monitor{{Name: "HDMI-A-1"}, {Name: "DP-1"}}, Monitor{Name: "eDP-1"})
	if err == nil {
		t.Fatalf("match output succeeded with missing focused monitor")
	}
	for _, want := range []string{"eDP-1", "DP-1, HDMI-A-1"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want substring %q", err.Error(), want)
		}
	}
}

func TestWaylandClientValidateSeatCapabilities(t *testing.T) {
	tests := []struct {
		name         string
		seatName     string
		capabilities uint32
		wantErr      []string
	}{
		{
			name:         "ok",
			seatName:     "seat0",
			capabilities: uint32(wlclient.SeatCapabilityKeyboard | wlclient.SeatCapabilityPointer),
		},
		{
			name:         "missing keyboard",
			seatName:     "seat0",
			capabilities: uint32(wlclient.SeatCapabilityPointer),
			wantErr:      []string{"seat0", "keyboard"},
		},
		{
			name:         "missing pointer",
			seatName:     "seat0",
			capabilities: uint32(wlclient.SeatCapabilityKeyboard),
			wantErr:      []string{"seat0", "pointer"},
		},
		{
			name:         "missing both unnamed",
			capabilities: 0,
			wantErr:      []string{"<unnamed>", "keyboard", "pointer"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &WaylandClient{seatName: tt.seatName, seatCapabilities: tt.capabilities}
			err := client.validateSeatCapabilities()
			if len(tt.wantErr) == 0 {
				if err != nil {
					t.Fatalf("validate seat capabilities: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validate seat capabilities succeeded, want error containing %v", tt.wantErr)
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %q, want substring %q", err.Error(), want)
				}
			}
		})
	}
}

func TestWaylandClientFocusedOutput(t *testing.T) {
	outputHandle := &wlclient.Output{}
	client := newWaylandClientForFocusedOutputTest("eDP-1", outputHandle)

	monitor, handle, err := client.FocusedOutput(context.Background(), &fakeFocusedMonitorLookup{monitor: Monitor{Name: "eDP-1"}})
	if err != nil {
		t.Fatalf("focused output: %v", err)
	}
	if monitor.Name != "eDP-1" || !monitor.Focused {
		t.Fatalf("monitor = %+v, want focused eDP-1", monitor)
	}
	if handle != outputHandle {
		t.Fatalf("handle = %p, want %p", handle, outputHandle)
	}
}

func TestWaylandClientFocusedOutputReportsLookupError(t *testing.T) {
	wantErr := errors.New("lookup failed")
	client := newWaylandClientForFocusedOutputTest("eDP-1", &wlclient.Output{})

	_, _, err := client.FocusedOutput(context.Background(), &fakeFocusedMonitorLookup{err: wantErr})
	if !errors.Is(err, wantErr) {
		t.Fatalf("focused output error = %v, want %v", err, wantErr)
	}
}

func TestWaylandClientFocusedOutputReportsMissingName(t *testing.T) {
	client := newWaylandClientForFocusedOutputTest("DP-1", &wlclient.Output{})

	_, _, err := client.FocusedOutput(context.Background(), &fakeFocusedMonitorLookup{monitor: Monitor{Name: "eDP-1"}})
	if err == nil {
		t.Fatalf("focused output succeeded with missing output name")
	}
	for _, want := range []string{"eDP-1", "DP-1"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want substring %q", err.Error(), want)
		}
	}
}

func TestWaylandClientFocusedOutputReportsMissingHandle(t *testing.T) {
	client := newWaylandClientForFocusedOutputTest("eDP-1", nil)

	_, _, err := client.FocusedOutput(context.Background(), &fakeFocusedMonitorLookup{monitor: Monitor{Name: "eDP-1"}})
	if err == nil {
		t.Fatalf("focused output succeeded without wl_output handle")
	}
	if !strings.Contains(err.Error(), "no bound wl_output handle") {
		t.Fatalf("error = %q, want missing-handle context", err.Error())
	}
}

func newWaylandClientForFocusedOutputTest(name string, handle *wlclient.Output) *WaylandClient {
	state := newWaylandOutputState(waylandGlobalBinding{
		Global:  WaylandGlobal{Name: 42, Interface: waylandInterfaceOutput, Version: 4},
		Version: 4,
	})
	state.applyXDGName(name)
	state.applyXDGLogicalPosition(10, 20)
	state.applyXDGLogicalSize(1600, 900)
	state.applyWLMode(waylandOutputModeCurrent, 2000, 1125)
	state.applyWLScale(2)
	client := &WaylandClient{
		outputs:       map[uint32]*waylandOutputState{42: state},
		outputHandles: map[uint32]*wlclient.Output{},
	}
	if handle != nil {
		client.outputHandles[42] = handle
	}
	return client
}

func TestWaylandSocketPathFromEnv(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	t.Setenv("WAYLAND_DISPLAY", "wayland-test")

	socketPath, err := WaylandSocketPathFromEnv()
	if err != nil {
		t.Fatalf("Wayland socket path from env: %v", err)
	}
	if want := filepath.Join(runtimeDir, "wayland-test"); socketPath != want {
		t.Fatalf("socket path = %q, want %q", socketPath, want)
	}
}

func TestWaylandSocketPathFromEnvReportsMissingDisplay(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("WAYLAND_DISPLAY", "")

	_, err := WaylandSocketPathFromEnv()
	if err == nil {
		t.Fatalf("Wayland socket path succeeded without WAYLAND_DISPLAY")
	}
	if !strings.Contains(err.Error(), "WAYLAND_DISPLAY") {
		t.Fatalf("error = %q, want WAYLAND_DISPLAY context", err.Error())
	}
}

func TestOpenWaylandClientReportsMissingSocket(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "missing-wayland")

	_, err := OpenWaylandClient(context.Background(), socketPath)
	if err == nil {
		t.Fatalf("OpenWaylandClient succeeded with missing socket")
	}
	for _, want := range []string{"connect to Wayland compositor socket", socketPath} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want substring %q", err.Error(), want)
		}
	}
}
