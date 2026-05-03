package main

import (
	"os"
	"strings"
	"testing"
)

func TestSystemdUserUnitReferencesInstalledDaemonAndGuardsEnvironment(t *testing.T) {
	data, err := os.ReadFile("mousekeys.service")
	if err != nil {
		t.Fatalf("read mousekeys.service: %v", err)
	}
	unit := string(data)
	for _, want := range []string{
		"systemd user unit",
		"~/.config/systemd/user/mousekeys.service",
		"ExecStart=/usr/bin/env mousekeys daemon",
		"Restart=on-failure",
		"StandardError=journal",
		"ExecStartPre=/usr/bin/env sh -c",
		"$$XDG_RUNTIME_DIR",
		"$$WAYLAND_DISPLAY",
		"$$HYPRLAND_INSTANCE_SIGNATURE",
		"WantedBy=default.target",
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("mousekeys.service missing %q:\n%s", want, unit)
		}
	}
}

func TestSystemdUserUnitStubDocumentsLifecycleCommands(t *testing.T) {
	data, err := os.ReadFile("docs/systemd-user-unit.md")
	if err != nil {
		t.Fatalf("read docs/systemd-user-unit.md: %v", err)
	}
	doc := string(data)
	for _, want := range []string{
		"systemd user unit",
		"systemd-analyze --user verify",
		"systemctl --user daemon-reload",
		"systemctl --user enable --now mousekeys.service",
		"systemctl --user restart mousekeys.service",
		"journalctl --user -u mousekeys.service -e",
		"systemctl --user disable --now mousekeys.service",
		"mousekeys status",
		"stale daemon",
		"docs-hyprland-and-install",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("docs/systemd-user-unit.md missing %q:\n%s", want, doc)
		}
	}
}
