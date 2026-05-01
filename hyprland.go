package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	HyprlandCommandSocketName = ".socket.sock"
	hyprlandMonitorsCommand   = "j/monitors"
	hyprlandIPCTimeout        = 2 * time.Second
	hyprlandResponseMaxBytes  = 4 << 20
)

type HyprlandIPCClient struct {
	socketPath string
	timeout    time.Duration
}

func NewHyprlandIPCClientFromEnv() HyprlandIPCClient {
	return HyprlandIPCClient{timeout: hyprlandIPCTimeout}
}

func NewHyprlandIPCClient(socketPath string) HyprlandIPCClient {
	return HyprlandIPCClient{socketPath: socketPath, timeout: hyprlandIPCTimeout}
}

func HyprlandCommandSocketPathFromEnv() (string, error) {
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		return "", fmt.Errorf("locate Hyprland IPC socket: XDG_RUNTIME_DIR is required")
	}
	if !filepath.IsAbs(runtimeDir) {
		return "", fmt.Errorf("locate Hyprland IPC socket: XDG_RUNTIME_DIR must be an absolute path, got %q", runtimeDir)
	}

	signature := os.Getenv("HYPRLAND_INSTANCE_SIGNATURE")
	if signature == "" {
		return "", fmt.Errorf("locate Hyprland IPC socket: HYPRLAND_INSTANCE_SIGNATURE is required")
	}

	return filepath.Join(runtimeDir, "hypr", signature, HyprlandCommandSocketName), nil
}

func (c HyprlandIPCClient) FocusedMonitor(ctx context.Context) (Monitor, error) {
	socketPath := c.socketPath
	if socketPath == "" {
		var err error
		socketPath, err = HyprlandCommandSocketPathFromEnv()
		if err != nil {
			return Monitor{}, err
		}
	}

	response, err := c.roundTrip(ctx, socketPath, hyprlandMonitorsCommand)
	if err != nil {
		return Monitor{}, err
	}

	monitor, err := parseHyprlandFocusedMonitor(response)
	if err != nil {
		return Monitor{}, fmt.Errorf("parse Hyprland monitors response from %q: %w", socketPath, err)
	}
	return monitor, nil
}

func (c HyprlandIPCClient) roundTrip(ctx context.Context, socketPath string, command string) ([]byte, error) {
	if socketPath == "" {
		return nil, fmt.Errorf("Hyprland IPC socket path is required")
	}
	timeout := c.timeout
	if timeout <= 0 {
		timeout = hyprlandIPCTimeout
	}

	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("connect to Hyprland IPC socket %q: %w", socketPath, err)
	}
	defer conn.Close()

	deadline := time.Now().Add(timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	_ = conn.SetDeadline(deadline)

	if _, err := conn.Write([]byte(command)); err != nil {
		return nil, fmt.Errorf("send Hyprland IPC command %q to %q: %w", command, socketPath, err)
	}

	response, err := io.ReadAll(io.LimitReader(conn, hyprlandResponseMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read Hyprland IPC response for %q from %q: %w", command, socketPath, err)
	}
	if len(response) > hyprlandResponseMaxBytes {
		return nil, fmt.Errorf("read Hyprland IPC response for %q from %q: response exceeds %d bytes", command, socketPath, hyprlandResponseMaxBytes)
	}
	if len(bytes.TrimSpace(response)) == 0 {
		return nil, fmt.Errorf("read Hyprland IPC response for %q from %q: empty response", command, socketPath)
	}
	return response, nil
}

type hyprlandMonitorResponse struct {
	Name      string  `json:"name"`
	X         int     `json:"x"`
	Y         int     `json:"y"`
	Width     int     `json:"width"`
	Height    int     `json:"height"`
	Scale     float64 `json:"scale"`
	Transform int     `json:"transform"`
	Focused   bool    `json:"focused"`
}

func parseHyprlandFocusedMonitor(data []byte) (Monitor, error) {
	var monitors []hyprlandMonitorResponse
	if err := json.Unmarshal(data, &monitors); err != nil {
		return Monitor{}, fmt.Errorf("decode monitors JSON: %w", err)
	}
	if len(monitors) == 0 {
		return Monitor{}, fmt.Errorf("monitors JSON contained no monitors")
	}

	for _, item := range monitors {
		if !item.Focused {
			continue
		}

		width, height, err := logicalHyprlandMonitorSize(item.Width, item.Height, item.Scale, item.Transform)
		if err != nil {
			return Monitor{}, fmt.Errorf("focused monitor %q has invalid geometry: %w", item.Name, err)
		}

		monitor := Monitor{
			Name:    item.Name,
			X:       item.X,
			Y:       item.Y,
			Width:   width,
			Height:  height,
			Scale:   item.Scale,
			Focused: true,
		}
		if err := validateFocusedMonitor(monitor); err != nil {
			return Monitor{}, err
		}
		return monitor, nil
	}

	return Monitor{}, fmt.Errorf("monitors JSON did not mark a focused monitor")
}

func logicalHyprlandMonitorSize(width int, height int, scale float64, transform int) (int, int, error) {
	if width <= 0 || height <= 0 {
		return 0, 0, fmt.Errorf("physical size must be positive, got %dx%d", width, height)
	}
	if scale <= 0 {
		return 0, 0, fmt.Errorf("scale must be positive, got %g", scale)
	}

	if transform%2 != 0 {
		width, height = height, width
	}

	return int(math.Round(float64(width) / scale)), int(math.Round(float64(height) / scale)), nil
}

func validateFocusedMonitor(monitor Monitor) error {
	if monitor.Name == "" {
		return fmt.Errorf("focused monitor name is empty")
	}
	if monitor.Width <= 0 || monitor.Height <= 0 {
		return fmt.Errorf("focused monitor %q has invalid logical size %dx%d", monitor.Name, monitor.Width, monitor.Height)
	}
	if monitor.Scale <= 0 {
		return fmt.Errorf("focused monitor %q has invalid scale %g", monitor.Name, monitor.Scale)
	}
	return nil
}
