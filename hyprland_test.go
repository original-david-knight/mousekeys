package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHyprlandIPCClientFocusedMonitorFromEnv(t *testing.T) {
	focused := Monitor{
		Name:    "HDMI-A-1",
		X:       -1920,
		Y:       240,
		Width:   1920,
		Height:  1080,
		Scale:   2.0,
		Focused: true,
	}
	unfocused := Monitor{
		Name:   "DP-1",
		X:      0,
		Y:      0,
		Width:  2560,
		Height: 1440,
		Scale:  1.0,
	}
	responder := newFakeHyprlandIPCResponderAtPath(t, configureHyprlandEnvForTest(t), []Monitor{unfocused, focused})
	responder.Start()

	monitor, err := NewHyprlandIPCClientFromEnv().FocusedMonitor(context.Background())
	if err != nil {
		t.Fatalf("focused monitor from Hyprland IPC: %v", err)
	}
	if monitor != focused {
		t.Fatalf("focused monitor = %+v, want %+v", monitor, focused)
	}

	requests := responder.Requests()
	if len(requests) != 1 || requests[0] != hyprlandMonitorsCommand {
		t.Fatalf("Hyprland IPC requests = %+v, want [%q]", requests, hyprlandMonitorsCommand)
	}
}

func TestDaemonShowUsesHyprlandFocusedMonitorLookup(t *testing.T) {
	focused := fakeFocusedMonitorFixture()
	responder := newFakeHyprlandIPCResponderAtPath(t, configureHyprlandEnvForTest(t), fakeMonitorFixtures())
	responder.Start()
	wayland := newFakeWaylandBackend(fakeMonitorFixtures()...)
	controller := NewDaemonController(DaemonDeps{
		MonitorLookup: NewHyprlandIPCClientFromEnv(),
		Overlay:       wayland,
	})

	if err := controller.Show(context.Background()); err != nil {
		t.Fatalf("show with Hyprland monitor lookup: %v", err)
	}

	events := wayland.Events()
	if len(events) == 0 || events[0].Kind != "surface_create" {
		t.Fatalf("Wayland events = %+v, want initial surface_create", events)
	}
	if events[0].OutputName != focused.Name || events[0].Width != focused.Width || events[0].Height != focused.Height || events[0].Scale != focused.Scale {
		t.Fatalf("surface_create target = %+v, want focused monitor %+v", events[0], focused)
	}
}

func TestHyprlandCommandSocketPathFromEnvRequiresInstanceSignature(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("HYPRLAND_INSTANCE_SIGNATURE", "")

	_, err := HyprlandCommandSocketPathFromEnv()
	if err == nil {
		t.Fatalf("Hyprland socket path succeeded without HYPRLAND_INSTANCE_SIGNATURE")
	}
	for _, want := range []string{"locate Hyprland IPC socket", "HYPRLAND_INSTANCE_SIGNATURE"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want substring %q", err.Error(), want)
		}
	}
}

func TestHyprlandIPCClientReportsUnreachableSocket(t *testing.T) {
	socketPath := configureHyprlandEnvForTest(t)

	_, err := NewHyprlandIPCClientFromEnv().FocusedMonitor(context.Background())
	if err == nil {
		t.Fatalf("focused monitor succeeded with missing Hyprland socket")
	}
	for _, want := range []string{"connect to Hyprland IPC socket", socketPath} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want substring %q", err.Error(), want)
		}
	}
}

func TestHyprlandIPCClientReportsMalformedResponse(t *testing.T) {
	socketPath := configureHyprlandEnvForTest(t)
	startRawHyprlandIPCResponder(t, socketPath, []byte("not json"))

	_, err := NewHyprlandIPCClientFromEnv().FocusedMonitor(context.Background())
	if err == nil {
		t.Fatalf("focused monitor succeeded with malformed Hyprland response")
	}
	for _, want := range []string{"parse Hyprland monitors response", "decode monitors JSON"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want substring %q", err.Error(), want)
		}
	}
}

func TestHyprlandIPCClientReportsMissingFocusedMonitor(t *testing.T) {
	socketPath := configureHyprlandEnvForTest(t)
	responder := newFakeHyprlandIPCResponderAtPath(t, socketPath, []Monitor{
		{Name: "DP-1", Width: 1920, Height: 1080, Scale: 1.0},
	})
	responder.Start()

	_, err := NewHyprlandIPCClientFromEnv().FocusedMonitor(context.Background())
	if err == nil {
		t.Fatalf("focused monitor succeeded when no monitor was marked focused")
	}
	if !strings.Contains(err.Error(), "did not mark a focused monitor") {
		t.Fatalf("error = %q, want missing focused monitor context", err.Error())
	}
}

func configureHyprlandEnvForTest(t *testing.T) string {
	t.Helper()
	runtimeDir := t.TempDir()
	signature := "test-signature"
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	t.Setenv("HYPRLAND_INSTANCE_SIGNATURE", signature)
	return filepath.Join(runtimeDir, "hypr", signature, HyprlandCommandSocketName)
}

func startRawHyprlandIPCResponder(t *testing.T, socketPath string, response []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		t.Fatalf("create fake Hyprland IPC socket directory: %v", err)
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen on fake Hyprland IPC socket: %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
		_ = os.Remove(socketPath)
	})

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
				buf := make([]byte, 4096)
				_, _ = conn.Read(buf)
				_, _ = conn.Write(response)
			}(conn)
		}
	}()
}
