package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestRootHelpListsCommands(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--help"}, &stdout, &stderr, emptyEnv)
	if code != 0 {
		t.Fatalf("run returned %d, want 0", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	for _, command := range []string{"daemon", "show", "hide", "status"} {
		if !strings.Contains(stdout.String(), command) {
			t.Fatalf("help output missing command %q:\n%s", command, stdout.String())
		}
	}
}

func TestStatusRequiresIPCRuntimeDir(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"status"}, &stdout, &stderr, emptyEnv)
	if code != 1 {
		t.Fatalf("run returned %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "XDG_RUNTIME_DIR") {
		t.Fatalf("stderr missing XDG_RUNTIME_DIR error:\n%s", stderr.String())
	}
}

func TestLoggerDefaultsToInfoAndDebugSwitchesOnEnv(t *testing.T) {
	var infoOnly bytes.Buffer
	logger := newLogger(&infoOnly, emptyEnv)
	logger.Debug("hidden")
	logger.Info("visible")

	logs := infoOnly.String()
	if !strings.Contains(logs, "visible") {
		t.Fatalf("info log missing from default logger: %q", logs)
	}
	if strings.Contains(logs, "hidden") {
		t.Fatalf("debug log was emitted by default logger: %q", logs)
	}

	var debugEnabled bytes.Buffer
	logger = newLogger(&debugEnabled, mapEnv(map[string]string{"MOUSEKEYS_LOG": "debug"}))
	logger.Debug("visible_debug")
	if !strings.Contains(debugEnabled.String(), "visible_debug") {
		t.Fatalf("debug log missing when MOUSEKEYS_LOG=debug: %q", debugEnabled.String())
	}
}

func TestDaemonRejectsMissingSessionEnv(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"daemon"}, &stdout, &stderr, emptyEnv)
	if code != 1 {
		t.Fatalf("run returned %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	for _, want := range []string{"daemon startup failed", "XDG_RUNTIME_DIR", "WAYLAND_DISPLAY", "HYPRLAND_INSTANCE_SIGNATURE"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr.String())
		}
	}
}

func TestDaemonRunsUntilContextCancelled(t *testing.T) {
	var stdout, stderr bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	env := mapEnv(map[string]string{
		"XDG_RUNTIME_DIR":             t.TempDir(),
		"XDG_CONFIG_HOME":             t.TempDir(),
		"WAYLAND_DISPLAY":             "wayland-1",
		"HYPRLAND_INSTANCE_SIGNATURE": "test-signature",
	})

	go func() {
		done <- run(ctx, []string{"daemon"}, &stdout, &stderr, env)
	}()

	select {
	case code := <-done:
		t.Fatalf("daemon exited early with %d; stderr=%q", code, stderr.String())
	case <-time.After(50 * time.Millisecond):
	}

	cancel()

	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("daemon returned %d after cancellation; stderr=%q", code, stderr.String())
		}
	case <-time.After(time.Second):
		t.Fatal("daemon did not exit after context cancellation")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func emptyEnv(string) string {
	return ""
}

func mapEnv(values map[string]string) getenvFunc {
	return func(key string) string {
		return values[key]
	}
}
