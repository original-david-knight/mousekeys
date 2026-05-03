package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type sessionEnv struct {
	XDGRuntimeDir             string
	WaylandDisplay            string
	HyprlandInstanceSignature string
}

func validateSessionEnv(getenv getenvFunc) (sessionEnv, error) {
	session := sessionEnv{
		XDGRuntimeDir:             strings.TrimSpace(getenv("XDG_RUNTIME_DIR")),
		WaylandDisplay:            strings.TrimSpace(getenv("WAYLAND_DISPLAY")),
		HyprlandInstanceSignature: strings.TrimSpace(getenv("HYPRLAND_INSTANCE_SIGNATURE")),
	}

	var missing []string
	if session.XDGRuntimeDir == "" {
		missing = append(missing, "XDG_RUNTIME_DIR")
	}
	if session.WaylandDisplay == "" {
		missing = append(missing, "WAYLAND_DISPLAY")
	}
	if session.HyprlandInstanceSignature == "" {
		missing = append(missing, "HYPRLAND_INSTANCE_SIGNATURE")
	}
	if len(missing) > 0 {
		return session, fmt.Errorf("missing required Hyprland session environment: %s; start mousekeys inside Hyprland/Wayland or import the session environment into the systemd user manager", strings.Join(missing, ", "))
	}

	if !filepath.IsAbs(session.XDGRuntimeDir) {
		return session, fmt.Errorf("invalid XDG_RUNTIME_DIR %q: must be an absolute path", session.XDGRuntimeDir)
	}
	info, err := os.Stat(session.XDGRuntimeDir)
	if err != nil {
		return session, fmt.Errorf("invalid XDG_RUNTIME_DIR %q: %w", session.XDGRuntimeDir, err)
	}
	if !info.IsDir() {
		return session, fmt.Errorf("invalid XDG_RUNTIME_DIR %q: not a directory", session.XDGRuntimeDir)
	}

	return session, nil
}
