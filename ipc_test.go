package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIPCServerLifecycleAndCommands(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)

	focused := fakeFocusedMonitorFixture()
	wayland := newFakeWaylandBackend(focused)
	pointer := &virtualPointerRecorder{}
	controller := NewDaemonController(DaemonDeps{
		MonitorLookup: &fakeFocusedMonitorLookup{monitor: focused},
		Overlay:       wayland,
		Pointer:       pointer,
		Clock:         newFakeClock(time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)),
		Trace:         noopTraceRecorder{},
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- runDaemonLoop(ctx, testLogger(), noopTraceRecorder{}, controller)
	}()
	defer cancel()

	socketPath := filepath.Join(runtimeDir, IPCSocketName)
	waitForSocket(t, socketPath)
	assertSocketExists(t, socketPath)

	response := sendIPCForTest(t, "status")
	if response.State != string(DaemonStateInactive) || response.Status != "inactive" || response.Active {
		t.Fatalf("initial status = %+v, want inactive", response)
	}
	if response.PID != os.Getpid() || response.Version == "" {
		t.Fatalf("status lacks process metadata: %+v", response)
	}

	response = sendIPCForTest(t, "show")
	if response.State != string(DaemonStateOverlayShown) || response.Status != "active" || !response.Active {
		t.Fatalf("show response = %+v, want active overlay", response)
	}
	if got := wayland.Count("surface_create"); got != 1 {
		t.Fatalf("surface_create count after show = %d, want 1", got)
	}

	response = sendIPCForTest(t, "status")
	if response.State != string(DaemonStateOverlayShown) || response.Status != "active" || !response.Active {
		t.Fatalf("active status = %+v, want overlay shown", response)
	}

	response = sendIPCForTest(t, "show")
	if response.State != string(DaemonStateInactive) || response.Status != "inactive" || response.Active {
		t.Fatalf("second show response = %+v, want inactive toggle-off", response)
	}
	if got := wayland.Count("destroy"); got != 1 {
		t.Fatalf("destroy count after show toggle = %d, want 1", got)
	}
	if events := pointer.Events(); len(events) != 0 {
		t.Fatalf("show toggle emitted pointer events: %+v", events)
	}

	response = sendIPCForTest(t, "hide")
	if response.State != string(DaemonStateInactive) || response.Status != "inactive" || response.Active {
		t.Fatalf("hide response while inactive = %+v, want inactive no-op", response)
	}
	if got := wayland.Count("destroy"); got != 1 {
		t.Fatalf("destroy count after inactive hide = %d, want unchanged 1", got)
	}

	sendIPCForTest(t, "show")
	response = sendIPCForTest(t, "hide")
	if response.State != string(DaemonStateInactive) || response.Status != "inactive" || response.Active {
		t.Fatalf("hide response while active = %+v, want inactive", response)
	}
	if got := wayland.Count("destroy"); got != 2 {
		t.Fatalf("destroy count after active hide = %d, want 2", got)
	}

	cancel()
	if err := waitDaemonStopped(t, errCh); err != nil {
		t.Fatalf("daemon loop returned error: %v", err)
	}
	assertNoSocket(t, socketPath)
}

func TestIPCServerRefusesSecondLiveDaemon(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- runDaemonLoop(ctx, testLogger(), noopTraceRecorder{}, NewStubDaemonController(noopTraceRecorder{}))
	}()
	defer cancel()

	socketPath := filepath.Join(runtimeDir, IPCSocketName)
	waitForSocket(t, socketPath)

	err := runDaemonLoop(context.Background(), testLogger(), noopTraceRecorder{}, NewStubDaemonController(noopTraceRecorder{}))
	if err == nil {
		t.Fatalf("second daemon loop started successfully, want occupied socket error")
	}
	if !strings.Contains(err.Error(), "another live Mouse Keys daemon") {
		t.Fatalf("second daemon error = %q, want live daemon refusal", err.Error())
	}

	cancel()
	if err := waitDaemonStopped(t, errCh); err != nil {
		t.Fatalf("first daemon loop returned error: %v", err)
	}
	assertNoSocket(t, socketPath)
}

func TestRunClientCommandPrintsStructuredStatus(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- runDaemonLoop(ctx, testLogger(), noopTraceRecorder{}, NewStubDaemonController(noopTraceRecorder{}))
	}()
	defer cancel()
	waitForSocket(t, filepath.Join(runtimeDir, IPCSocketName))

	var stdout bytes.Buffer
	oldStdout := os.Stdout
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	os.Stdout = writePipe
	runErr := runClientCommand("status", nil, testLogger())
	_ = writePipe.Close()
	os.Stdout = oldStdout
	if runErr != nil {
		t.Fatalf("run status client command: %v", runErr)
	}
	if _, err := io.Copy(&stdout, readPipe); err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	_ = readPipe.Close()

	if !strings.Contains(stdout.String(), `"state":"inactive"`) {
		t.Fatalf("status stdout = %q, want structured inactive state", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"status":"inactive"`) {
		t.Fatalf("status stdout = %q, want plain inactive status", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"pid":`) || !strings.Contains(stdout.String(), `"version":`) {
		t.Fatalf("status stdout = %q, want pid and version", stdout.String())
	}

	cancel()
	if err := waitDaemonStopped(t, errCh); err != nil {
		t.Fatalf("daemon loop returned error: %v", err)
	}
	assertNoSocket(t, filepath.Join(runtimeDir, IPCSocketName))
}

func sendIPCForTest(t *testing.T, command string) IPCResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	response, err := SendIPCCommand(ctx, command)
	if err != nil {
		t.Fatalf("send IPC command %q: %v", command, err)
	}
	if !response.OK {
		t.Fatalf("IPC command %q returned unsuccessful response: %+v", command, response)
	}
	return response
}

func waitForSocket(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		info, err := os.Lstat(path)
		if err == nil && info.Mode()&os.ModeSocket != 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	assertSocketExists(t, path)
}

func assertSocketExists(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat socket %q: %v", path, err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("%q exists but is not a socket: mode=%s", path, info.Mode())
	}
}

func assertNoSocket(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("socket %q should be removed, stat err=%v", path, err)
	}
}

func waitDaemonStopped(t *testing.T, errCh <-chan error) error {
	t.Helper()
	select {
	case err := <-errCh:
		return err
	case <-time.After(2 * time.Second):
		t.Fatalf("daemon loop did not stop")
		return nil
	}
}

func testLogger() *logger {
	return &logger{out: io.Discard}
}
