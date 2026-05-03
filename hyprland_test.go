package main

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestHyprlandIPCClientFocusedMonitorSuccess(t *testing.T) {
	tests := []struct {
		name      string
		signature string
		response  string
		want      Monitor
	}{
		{
			name:      "offset scaled focused monitor with physical size",
			signature: "sig-offset",
			response: `[
				{"name":"eDP-1","width":1920,"height":1080,"physicalWidth":340,"physicalHeight":190,"x":0,"y":0,"scale":1.0,"focused":false},
				{"name":"DP-1","width":2560,"height":1440,"physicalWidth":600,"physicalHeight":340,"x":1920,"y":120,"scale":1.25,"focused":true}
			]`,
			want: Monitor{
				Name:             "DP-1",
				OriginX:          1920,
				OriginY:          120,
				LogicalWidth:     2048,
				LogicalHeight:    1152,
				PhysicalWidthMM:  600,
				PhysicalHeightMM: 340,
				Scale:            1.25,
			},
		},
		{
			name:      "negative origin focused monitor without physical size",
			signature: "sig-neg",
			response: `[
				{"name":"HDMI-A-1","width":1280,"height":720,"x":-1280,"y":-360,"scale":1.0,"focused":true}
			]`,
			want: Monitor{
				Name:          "HDMI-A-1",
				OriginX:       -1280,
				OriginY:       -360,
				LogicalWidth:  1280,
				LogicalHeight: 720,
				Scale:         1.0,
			},
		},
		{
			name:      "fractional scale normalizes pixel size to logical size",
			signature: "sig-scale",
			response: `[
				{"name":"WL-1","width":3000,"height":1800,"physicalWidth":520,"physicalHeight":320,"x":320,"y":0,"scale":1.5,"focused":true}
			]`,
			want: Monitor{
				Name:             "WL-1",
				OriginX:          320,
				OriginY:          0,
				LogicalWidth:     2000,
				LogicalHeight:    1200,
				PhysicalWidthMM:  520,
				PhysicalHeightMM: 320,
				Scale:            1.5,
			},
		},
		{
			name:      "rotated monitor swaps logical axes after scale normalization",
			signature: "sig-rotate",
			response: `[
				{"name":"DP-4","width":2560,"height":1440,"physicalWidth":600,"physicalHeight":340,"x":-1440,"y":0,"scale":1.0,"transform":1,"focused":true}
			]`,
			want: Monitor{
				Name:             "DP-4",
				OriginX:          -1440,
				OriginY:          0,
				LogicalWidth:     1440,
				LogicalHeight:    2560,
				PhysicalWidthMM:  600,
				PhysicalHeightMM: 340,
				Scale:            1.0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtimeDir := shortTempRuntimeDir(t)
			socket := startFakeHyprlandMonitorSocket(t, runtimeDir, tt.signature, tt.response)
			client := NewHyprlandIPCClient(hyprlandTestEnv(runtimeDir, tt.signature))

			got, err := client.FocusedMonitor(context.Background())
			if err != nil {
				t.Fatalf("FocusedMonitor returned error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("FocusedMonitor = %+v, want %+v", got, tt.want)
			}
			if commands := socket.Commands(); !reflect.DeepEqual(commands, []string{hyprlandMonitorQuery}) {
				t.Fatalf("Hyprland IPC commands = %q, want %q", commands, []string{hyprlandMonitorQuery})
			}
		})
	}
}

func TestHyprlandIPCClientFocusedMonitorErrors(t *testing.T) {
	t.Run("missing environment", func(t *testing.T) {
		client := NewHyprlandIPCClient(emptyEnv)
		_, err := client.FocusedMonitor(context.Background())
		assertErrorContains(t, err, "locate Hyprland IPC socket", "XDG_RUNTIME_DIR", "HYPRLAND_INSTANCE_SIGNATURE")
	})

	t.Run("missing socket", func(t *testing.T) {
		runtimeDir := shortTempRuntimeDir(t)
		signature := "missing-socket"
		client := NewHyprlandIPCClient(hyprlandTestEnv(runtimeDir, signature))
		_, err := client.FocusedMonitor(context.Background())
		assertErrorContains(t, err, "Hyprland IPC socket", ".socket.sock", "does not exist", signature)
	})

	t.Run("unreachable socket", func(t *testing.T) {
		runtimeDir := shortTempRuntimeDir(t)
		signature := "stale-socket"
		socketPath := hyprlandSocketPathForTest(runtimeDir, signature)
		createStaleUnixSocket(t, socketPath)

		client := NewHyprlandIPCClient(hyprlandTestEnv(runtimeDir, signature))
		_, err := client.FocusedMonitor(context.Background())
		assertErrorContains(t, err, "query focused monitor from Hyprland IPC", "connect to Hyprland IPC socket", socketPath)
	})

	t.Run("malformed response", func(t *testing.T) {
		runtimeDir := shortTempRuntimeDir(t)
		signature := "malformed-response"
		startFakeHyprlandMonitorSocket(t, runtimeDir, signature, `not-json`)

		client := NewHyprlandIPCClient(hyprlandTestEnv(runtimeDir, signature))
		_, err := client.FocusedMonitor(context.Background())
		assertErrorContains(t, err, "parse focused monitor", "decode Hyprland monitors JSON", `response prefix "not-json"`)
	})

	t.Run("no focused monitor", func(t *testing.T) {
		runtimeDir := shortTempRuntimeDir(t)
		signature := "no-focused-monitor"
		startFakeHyprlandMonitorSocket(t, runtimeDir, signature, `[
			{"name":"DP-1","width":2560,"height":1440,"x":0,"y":0,"scale":1.0,"focused":false}
		]`)

		client := NewHyprlandIPCClient(hyprlandTestEnv(runtimeDir, signature))
		_, err := client.FocusedMonitor(context.Background())
		assertErrorContains(t, err, "no focused monitor", "1 monitor")
	})

	t.Run("invalid focused monitor", func(t *testing.T) {
		runtimeDir := shortTempRuntimeDir(t)
		signature := "invalid-focused-monitor"
		startFakeHyprlandMonitorSocket(t, runtimeDir, signature, `[
			{"name":"DP-1","width":2560,"height":1440,"x":0,"y":0,"scale":0,"focused":true}
		]`)

		client := NewHyprlandIPCClient(hyprlandTestEnv(runtimeDir, signature))
		_, err := client.FocusedMonitor(context.Background())
		assertErrorContains(t, err, "focused Hyprland monitor", "DP-1", "scale must be positive")
	})
}

type fakeHyprlandMonitorSocket struct {
	listener net.Listener
	mu       sync.Mutex
	commands []string
}

func startFakeHyprlandMonitorSocket(t *testing.T, runtimeDir, signature, response string) *fakeHyprlandMonitorSocket {
	t.Helper()
	socketPath := hyprlandSocketPathForTest(runtimeDir, signature)
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		t.Fatalf("create fake Hyprland socket dir: %v", err)
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen on fake Hyprland socket: %v", err)
	}

	socket := &fakeHyprlandMonitorSocket{listener: listener}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go socket.handle(conn, response)
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("fake Hyprland socket did not stop")
		}
		_ = os.Remove(socketPath)
	})
	return socket
}

func (s *fakeHyprlandMonitorSocket) handle(conn net.Conn, response string) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	buf := make([]byte, len(hyprlandMonitorQuery))
	if _, err := io.ReadFull(conn, buf); err != nil {
		return
	}
	command := string(buf)
	s.mu.Lock()
	s.commands = append(s.commands, command)
	s.mu.Unlock()
	_, _ = io.WriteString(conn, response)
}

func (s *fakeHyprlandMonitorSocket) Commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.commands))
	copy(out, s.commands)
	return out
}

func hyprlandTestEnv(runtimeDir, signature string) getenvFunc {
	return mapEnv(map[string]string{
		"XDG_RUNTIME_DIR":             runtimeDir,
		"HYPRLAND_INSTANCE_SIGNATURE": signature,
	})
}

func shortTempRuntimeDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "mk-hypr-")
	if err != nil {
		t.Fatalf("create short temp runtime dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return dir
}

func hyprlandSocketPathForTest(runtimeDir, signature string) string {
	return filepath.Join(runtimeDir, "hypr", signature, hyprlandIPCSocketName)
}

func createStaleUnixSocket(t *testing.T, socketPath string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		t.Fatalf("create stale socket dir: %v", err)
	}
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
	t.Cleanup(func() {
		_ = os.Remove(socketPath)
	})
}

func assertErrorContains(t *testing.T, err error, parts ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want substrings %q", parts)
	}
	message := err.Error()
	for _, part := range parts {
		if !strings.Contains(message, part) {
			t.Fatalf("error %q missing %q", message, part)
		}
	}
}
