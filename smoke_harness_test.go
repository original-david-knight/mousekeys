package main

import (
	"os"
	"strings"
	"testing"
)

func TestRealHyprlandSmokeHarnessContract(t *testing.T) {
	info, err := os.Stat("scripts/smoke_real_hyprland.sh")
	if err != nil {
		t.Fatalf("stat real Hyprland smoke harness: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("scripts/smoke_real_hyprland.sh is not executable: mode %v", info.Mode())
	}
	data, err := os.ReadFile("scripts/smoke_real_hyprland.sh")
	if err != nil {
		t.Fatalf("read real Hyprland smoke harness: %v", err)
	}
	script := string(data)
	for _, want := range []string{
		"\"status\":\"skip\"",
		"\"status\": status",
		"MOUSEKEYS_INSTALL_PATH",
		"MOUSEKEYS_TRACE_JSONL",
		"systemctl --user restart mousekeys.service",
		"mousekeys status",
		"hyprctl dispatch exec \"mousekeys show\"",
		"wtype",
		"ydotool",
		"dotool",
		"hyprctl cursorpos",
		"keyboard.keymap",
		"keyboard.enter",
		"keyboard.token",
		"coordinate.selected_cell",
		"pointer.motion",
		"overlay.unmapped_for_click",
		"pointer.button",
		"after_hide_show_show_flow",
		"after_service_restart_flow",
		"chrome_focused_flow",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("smoke harness missing %q", want)
		}
	}
}
