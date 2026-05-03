package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestIPCCommandsDriveOverlayStatusAndToggle(t *testing.T) {
	runtimeDir := t.TempDir()
	env := daemonTestEnv(t, runtimeDir)
	overlay := &recordingOverlayDriver{}
	pointer := newPointerRecorder(nil)
	socketPath, stop := startTestDaemon(t, env, overlay)
	defer stop()

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"status"}, &stdout, &stderr, env)
	if code != 0 {
		t.Fatalf("status returned %d; stderr=%q", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("status stderr = %q, want empty", stderr.String())
	}
	var status statusOutput
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatalf("status output is not JSON: %v\n%s", err, stdout.String())
	}
	if status.Active || status.State != "inactive" {
		t.Fatalf("initial status active/state = %v/%q, want inactive", status.Active, status.State)
	}
	if status.PID != os.Getpid() || status.BuildID == "" || status.Build.Version == "" || status.Build.Commit == "" || status.Build.GoVersion == "" {
		t.Fatalf("status missing daemon process/build metadata: %+v", status)
	}
	if status.Socket != socketPath || status.RuntimeDir != runtimeDir {
		t.Fatalf("status socket/runtime = %q/%q, want %q/%q", status.Socket, status.RuntimeDir, socketPath, runtimeDir)
	}
	if status.Executable == "" || status.Binary.Executable == "" || status.Service.UnitName != "mousekeys.service" || status.Client == nil || status.Client.Executable == "" {
		t.Fatalf("status missing binary/service/client metadata: %+v", status)
	}
	if runtime.GOOS == "linux" {
		if status.Binary.ProcessExecutable == "" || status.Binary.ProcessFile == nil || status.Binary.ProcessFile.SHA256 == "" || status.Binary.ProcessFile.Inode == 0 {
			t.Fatalf("status missing daemon process executable identity: %+v", status.Binary)
		}
		if status.Binary.PathFile == nil || status.Binary.PathFile.SHA256 == "" {
			t.Fatalf("status missing daemon executable path identity: %+v", status.Binary)
		}
		if status.Client.ProcessExecutable == "" || status.Client.ProcessFile == nil || status.Client.ProcessFile.SHA256 == "" {
			t.Fatalf("status missing client process executable identity: %+v", status.Client)
		}
	}

	stdout.Reset()
	stderr.Reset()
	code = run(context.Background(), []string{"show"}, &stdout, &stderr, env)
	if code != 0 {
		t.Fatalf("show returned %d; stderr=%q", code, stderr.String())
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("show stdout/stderr = %q/%q, want empty", stdout.String(), stderr.String())
	}
	assertOverlayEvents(t, overlay.Events(), []string{"show"})
	assertIPCStatusActive(t, env, true)

	response, err := sendIPCCommand(context.Background(), env, "show")
	if err != nil {
		t.Fatalf("toggle show IPC returned error: %v", err)
	}
	if response.Active || response.State != "inactive" || response.Action != "hidden" {
		t.Fatalf("toggle show response = %+v, want hidden inactive", response)
	}
	assertOverlayEvents(t, overlay.Events(), []string{"show", "cancel:show_toggle"})
	assertIPCStatusActive(t, env, false)

	response, err = sendIPCCommand(context.Background(), env, "hide")
	if err != nil {
		t.Fatalf("inactive hide IPC returned error: %v", err)
	}
	if response.Active || response.Action != "noop" {
		t.Fatalf("inactive hide response = %+v, want noop inactive", response)
	}
	assertOverlayEvents(t, overlay.Events(), []string{"show", "cancel:show_toggle"})

	response, err = sendIPCCommand(context.Background(), env, "show")
	if err != nil {
		t.Fatalf("second show IPC returned error: %v", err)
	}
	if !response.Active || response.Action != "shown" {
		t.Fatalf("second show response = %+v, want shown active", response)
	}
	response, err = sendIPCCommand(context.Background(), env, "hide")
	if err != nil {
		t.Fatalf("active hide IPC returned error: %v", err)
	}
	if response.Active || response.Action != "hidden" {
		t.Fatalf("active hide response = %+v, want hidden inactive", response)
	}
	assertOverlayEvents(t, overlay.Events(), []string{"show", "cancel:show_toggle", "show", "cancel:hide"})
	if events := pointer.Events(); len(events) != 0 {
		t.Fatalf("show/hide/toggle emitted pointer events: %+v", events)
	}
}

func TestDaemonRefusesSecondLiveDaemonOnSameSocket(t *testing.T) {
	runtimeDir := t.TempDir()
	env := daemonTestEnv(t, runtimeDir)
	_, stop := startTestDaemon(t, env, &recordingOverlayDriver{})
	defer stop()

	var stderr bytes.Buffer
	code := runDaemonWithOptions(context.Background(), newLogger(&stderr, env), env, daemonOptions{Overlay: &recordingOverlayDriver{}})
	if code != 1 {
		t.Fatalf("second daemon returned %d, want 1; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "another live mousekeys daemon owns IPC socket") {
		t.Fatalf("second daemon stderr missing live-owner refusal:\n%s", stderr.String())
	}
	if _, err := sendIPCCommand(context.Background(), env, "status"); err != nil {
		t.Fatalf("first daemon did not remain reachable after refused second start: %v", err)
	}
}

func TestDaemonRemovesStaleSocketAndCleansUp(t *testing.T) {
	runtimeDir := t.TempDir()
	socketPath := filepath.Join(runtimeDir, ipcSocketName)
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("create stale socket fd: %v", err)
	}
	if err := syscall.Bind(fd, &syscall.SockaddrUnix{Name: socketPath}); err != nil {
		_ = syscall.Close(fd)
		t.Fatalf("bind stale socket fd: %v", err)
	}
	if err := syscall.Close(fd); err != nil {
		t.Fatalf("close stale socket fd: %v", err)
	}
	if info, err := os.Lstat(socketPath); err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("stale socket path was not left behind as a socket: info=%v err=%v", info, err)
	}

	env := daemonTestEnv(t, runtimeDir)
	_, stop := startTestDaemon(t, env, &recordingOverlayDriver{})
	stop()
	if _, err := os.Lstat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("socket file still exists after clean shutdown: %v", err)
	}
}

type recordingOverlayDriver struct {
	mu     sync.Mutex
	events []string
}

func (r *recordingOverlayDriver) ShowOverlay(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, "show")
	return nil
}

func (r *recordingOverlayDriver) CancelOverlay(ctx context.Context, reason overlayCancelReason) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, "cancel:"+string(reason))
	return nil
}

func (r *recordingOverlayDriver) Events() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.events))
	copy(out, r.events)
	return out
}

func daemonTestEnv(t *testing.T, runtimeDir string) getenvFunc {
	t.Helper()
	return mapEnv(map[string]string{
		"XDG_RUNTIME_DIR":             runtimeDir,
		"XDG_CONFIG_HOME":             t.TempDir(),
		"WAYLAND_DISPLAY":             "wayland-ipc-test",
		"HYPRLAND_INSTANCE_SIGNATURE": "ipc-test-signature",
		"INVOCATION_ID":               "test-invocation",
		"SYSTEMD_EXEC_PID":            "12345",
	})
}

func startTestDaemon(t *testing.T, env getenvFunc, overlay overlayDriver) (string, func()) {
	t.Helper()
	socketPath, err := ipcSocketPathFromEnv(env)
	if err != nil {
		t.Fatalf("IPC socket path from env: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	var stderr bytes.Buffer
	go func() {
		done <- runDaemonWithOptions(ctx, newLogger(&stderr, env), env, daemonOptions{Overlay: overlay})
	}()
	waitForDaemonReady(t, env, done, &stderr)
	if info, err := os.Lstat(socketPath); err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("daemon did not own a Unix socket at %q while running: info=%v err=%v", socketPath, info, err)
	}

	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case code := <-done:
				if code != 0 {
					t.Fatalf("daemon returned %d after cancellation; stderr=%q", code, stderr.String())
				}
			case <-time.After(time.Second):
				t.Fatalf("daemon did not stop after cancellation; stderr=%q", stderr.String())
			}
			if _, err := os.Lstat(socketPath); !os.IsNotExist(err) {
				t.Fatalf("socket file remains after daemon shutdown: %v", err)
			}
		})
	}
	t.Cleanup(stop)
	return socketPath, stop
}

func waitForDaemonReady(t *testing.T, env getenvFunc, done <-chan int, stderr *bytes.Buffer) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		select {
		case code := <-done:
			t.Fatalf("daemon exited before becoming ready with %d; stderr=%q", code, stderr.String())
		default:
		}

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		_, err := sendIPCCommand(ctx, env, "status")
		cancel()
		if err == nil {
			return
		}
		lastErr = err
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("daemon did not become ready: %v; stderr=%q", lastErr, stderr.String())
}

func assertIPCStatusActive(t *testing.T, env getenvFunc, want bool) {
	t.Helper()
	response, err := sendIPCCommand(context.Background(), env, "status")
	if err != nil {
		t.Fatalf("status IPC returned error: %v", err)
	}
	if response.Status == nil {
		t.Fatal("status IPC returned no status payload")
	}
	if response.Status.Active != want || response.Status.State != activeState(want) {
		t.Fatalf("status active/state = %v/%q, want %v/%q", response.Status.Active, response.Status.State, want, activeState(want))
	}
}

func assertOverlayEvents(t *testing.T, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("overlay events = %v, want %v", got, want)
	}
}
