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
	"strings"
	"time"
)

const (
	hyprlandIPCSocketName = ".socket.sock"
	hyprlandMonitorQuery  = "j/monitors"
	hyprlandIPCTimeout    = 2 * time.Second
)

type HyprlandIPCClient struct {
	getenv  getenvFunc
	timeout time.Duration
}

func NewHyprlandIPCClient(getenv getenvFunc) *HyprlandIPCClient {
	if getenv == nil {
		getenv = os.Getenv
	}
	return &HyprlandIPCClient{
		getenv:  getenv,
		timeout: hyprlandIPCTimeout,
	}
}

func (c *HyprlandIPCClient) FocusedMonitor(ctx context.Context) (Monitor, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	getenv := os.Getenv
	timeout := hyprlandIPCTimeout
	if c != nil {
		if c.getenv != nil {
			getenv = c.getenv
		}
		timeout = c.timeout
	}

	socketPath, err := hyprlandIPCSocketPathFromEnv(getenv)
	if err != nil {
		return Monitor{}, fmt.Errorf("locate Hyprland IPC socket for focused monitor: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Monitor{}, fmt.Errorf("query focused monitor from Hyprland IPC at %q: %w", socketPath, err)
	}

	if timeout > 0 {
		if _, ok := ctx.Deadline(); !ok {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
	}

	response, err := queryHyprlandIPC(ctx, socketPath, hyprlandMonitorQuery)
	if err != nil {
		return Monitor{}, fmt.Errorf("query focused monitor from Hyprland IPC: %w", err)
	}

	monitor, err := focusedMonitorFromHyprlandResponse(response)
	if err != nil {
		return Monitor{}, fmt.Errorf("parse focused monitor from Hyprland IPC response to %q: %w", hyprlandMonitorQuery, err)
	}
	return monitor, nil
}

func hyprlandIPCSocketPathFromEnv(getenv getenvFunc) (string, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	runtimeDir := strings.TrimSpace(getenv("XDG_RUNTIME_DIR"))
	signature := strings.TrimSpace(getenv("HYPRLAND_INSTANCE_SIGNATURE"))
	var missing []string
	if runtimeDir == "" {
		missing = append(missing, "XDG_RUNTIME_DIR")
	}
	if signature == "" {
		missing = append(missing, "HYPRLAND_INSTANCE_SIGNATURE")
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("missing required Hyprland IPC environment: %s", strings.Join(missing, ", "))
	}
	if !filepath.IsAbs(runtimeDir) {
		return "", fmt.Errorf("invalid XDG_RUNTIME_DIR %q for Hyprland IPC: must be an absolute path", runtimeDir)
	}
	info, err := os.Stat(runtimeDir)
	if err != nil {
		return "", fmt.Errorf("invalid XDG_RUNTIME_DIR %q for Hyprland IPC: %w", runtimeDir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("invalid XDG_RUNTIME_DIR %q for Hyprland IPC: not a directory", runtimeDir)
	}

	socketPath := filepath.Join(runtimeDir, "hypr", signature, hyprlandIPCSocketName)
	info, err = os.Lstat(socketPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("Hyprland IPC socket %q does not exist for HYPRLAND_INSTANCE_SIGNATURE %q: %w", socketPath, signature, err)
		}
		return "", fmt.Errorf("stat Hyprland IPC socket %q: %w", socketPath, err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return "", fmt.Errorf("Hyprland IPC path %q is not a Unix socket", socketPath)
	}
	return socketPath, nil
}

func queryHyprlandIPC(ctx context.Context, socketPath, command string) ([]byte, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("connect to Hyprland IPC socket %q: %w", socketPath, err)
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if _, err := io.WriteString(conn, command); err != nil {
		return nil, fmt.Errorf("send Hyprland IPC command %q to %q: %w", command, socketPath, err)
	}
	response, err := io.ReadAll(conn)
	if err != nil {
		return nil, fmt.Errorf("read Hyprland IPC response for %q from %q: %w", command, socketPath, err)
	}
	if len(bytes.TrimSpace(response)) == 0 {
		return nil, fmt.Errorf("empty Hyprland IPC response for %q from %q", command, socketPath)
	}
	return response, nil
}

type hyprlandMonitorJSON struct {
	Name           string  `json:"name"`
	Width          int     `json:"width"`
	Height         int     `json:"height"`
	PhysicalWidth  int     `json:"physicalWidth"`
	PhysicalHeight int     `json:"physicalHeight"`
	X              int     `json:"x"`
	Y              int     `json:"y"`
	Scale          float64 `json:"scale"`
	Transform      int     `json:"transform"`
	Focused        bool    `json:"focused"`
}

func focusedMonitorFromHyprlandResponse(response []byte) (Monitor, error) {
	var raw []hyprlandMonitorJSON
	if err := json.Unmarshal(response, &raw); err != nil {
		return Monitor{}, fmt.Errorf("decode Hyprland monitors JSON: %w; response prefix %q", err, errorPrefix(response, 160))
	}
	if len(raw) == 0 {
		return Monitor{}, fmt.Errorf("no focused monitor in Hyprland monitors response: monitor list is empty")
	}

	for _, candidate := range raw {
		if !candidate.Focused {
			continue
		}
		monitor, err := candidate.toMonitor()
		if err != nil {
			name := candidate.Name
			if name == "" {
				name = "<unnamed>"
			}
			return Monitor{}, fmt.Errorf("focused Hyprland monitor %q is invalid: %w", name, err)
		}
		return monitor, nil
	}
	return Monitor{}, fmt.Errorf("no focused monitor in Hyprland monitors response with %d monitor(s)", len(raw))
}

func (m hyprlandMonitorJSON) toMonitor() (Monitor, error) {
	if m.Scale <= 0 {
		return Monitor{}, fmt.Errorf("scale must be positive, got %v", m.Scale)
	}
	logicalWidth := normalizeHyprlandLogicalSize(m.Width, m.Scale)
	logicalHeight := normalizeHyprlandLogicalSize(m.Height, m.Scale)
	if hyprlandTransformSwapsAxes(m.Transform) {
		logicalWidth, logicalHeight = logicalHeight, logicalWidth
	}
	monitor := Monitor{
		Name:             m.Name,
		OriginX:          m.X,
		OriginY:          m.Y,
		LogicalWidth:     logicalWidth,
		LogicalHeight:    logicalHeight,
		PhysicalWidthMM:  m.PhysicalWidth,
		PhysicalHeightMM: m.PhysicalHeight,
		Scale:            m.Scale,
	}
	if err := monitor.Validate(); err != nil {
		return Monitor{}, err
	}
	return monitor, nil
}

func normalizeHyprlandLogicalSize(pixelSize int, scale float64) int {
	return int(math.Round(float64(pixelSize) / scale))
}

func hyprlandTransformSwapsAxes(transform int) bool {
	switch transform {
	case 1, 3, 5, 7:
		return true
	default:
		return false
	}
}

func errorPrefix(data []byte, limit int) string {
	data = bytes.TrimSpace(data)
	if len(data) <= limit {
		return string(data)
	}
	return string(data[:limit]) + "..."
}
