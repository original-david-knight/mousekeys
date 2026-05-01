package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigCreatesDefaultFileUnderXDGConfigHome(t *testing.T) {
	xdgConfigHome := t.TempDir()
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgConfigHome)
	t.Setenv("HOME", home)

	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	configPath := filepath.Join(xdgConfigHome, "mousekeys", "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	if got := string(data); got != DefaultConfigTOML {
		t.Fatalf("generated config mismatch:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "mousekeys", "config.toml")); !os.IsNotExist(err) {
		t.Fatalf("config should not be written under HOME when XDG_CONFIG_HOME is set, stat err=%v", err)
	}

	assertDefaultConfig(t, config)
}

func TestLoadConfigAppliesUserValuesAndDefaultsMissingFields(t *testing.T) {
	configPath := writeConfigForTest(t, `[grid]
size = 13

[keybinds]
left_click = "space Return"

[behavior]
stay_active = false

[appearance]
grid_opacity = 0.75
`)

	config, err := LoadConfigFile(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if config.Grid.Size != 13 {
		t.Fatalf("grid.size = %d, want 13", config.Grid.Size)
	}
	if config.Grid.SubgridPixelSize != 5 {
		t.Fatalf("grid.subgrid_pixel_size = %d, want default 5", config.Grid.SubgridPixelSize)
	}
	assertSequence(t, config.Keybinds.LeftClick, "space", "Return")
	assertSequence(t, config.Keybinds.RightClick, "space")
	if config.Behavior.StayActive {
		t.Fatalf("behavior.stay_active = true, want false")
	}
	if config.Behavior.DoubleClickTimeoutMS != 250 {
		t.Fatalf("behavior.double_click_timeout_ms = %d, want default 250", config.Behavior.DoubleClickTimeoutMS)
	}
	if config.Appearance.GridOpacity != 0.75 {
		t.Fatalf("appearance.grid_opacity = %g, want 0.75", config.Appearance.GridOpacity)
	}
	if config.Appearance.GridLineWidth != 1 {
		t.Fatalf("appearance.grid_line_width = %d, want default 1", config.Appearance.GridLineWidth)
	}
}

func TestLoadConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name: "invalid keysym",
			content: `[keybinds]
left_click = "NotAKeysym"
`,
			want: "invalid xkbcommon keysym name",
		},
		{
			name: "case sensitive keysym",
			content: `[keybinds]
left_click = "return"
`,
			want: "invalid xkbcommon keysym name",
		},
		{
			name: "empty key sequence",
			content: `[keybinds]
left_click = ""
`,
			want: "key sequence must contain at least one",
		},
		{
			name: "invalid grid size",
			content: `[grid]
size = 0
`,
			want: "grid.size",
		},
		{
			name: "invalid opacity",
			content: `[appearance]
grid_opacity = 1.5
`,
			want: "appearance.grid_opacity",
		},
		{
			name: "unknown trigger hotkey",
			content: `[trigger]
hotkey = "Super_L period"
`,
			want: "unknown config key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadConfigFile(writeConfigForTest(t, tt.content))
			if err == nil {
				t.Fatalf("load config succeeded, want error containing %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tt.want)
			}
		})
	}
}

func TestParseKeySequence(t *testing.T) {
	sequence, err := ParseKeySequence("Return Return")
	if err != nil {
		t.Fatalf("parse key sequence: %v", err)
	}
	assertSequence(t, sequence, "Return", "Return")
	if got := sequence.String(); got != "Return Return" {
		t.Fatalf("sequence.String() = %q, want %q", got, "Return Return")
	}

	sequence, err = ParseKeySequence("space Tab BackSpace")
	if err != nil {
		t.Fatalf("parse multi-key sequence: %v", err)
	}
	assertSequence(t, sequence, "space", "Tab", "BackSpace")

	if _, err := ParseKeySequence("Return NotAKeysym"); err == nil {
		t.Fatalf("parse invalid sequence succeeded")
	}
	if _, err := ParseKeySequence("Space"); err == nil {
		t.Fatalf("parse case-mismatched space succeeded")
	}
}

func assertDefaultConfig(t *testing.T, config Config) {
	t.Helper()
	if config.Grid.Size != 26 {
		t.Fatalf("grid.size = %d, want 26", config.Grid.Size)
	}
	if config.Grid.SubgridPixelSize != 5 {
		t.Fatalf("grid.subgrid_pixel_size = %d, want 5", config.Grid.SubgridPixelSize)
	}
	assertSequence(t, config.Keybinds.LeftClick, "Return")
	assertSequence(t, config.Keybinds.RightClick, "space")
	assertSequence(t, config.Keybinds.DoubleClick, "Return", "Return")
	assertSequence(t, config.Keybinds.CommitPartial, "Tab")
	assertSequence(t, config.Keybinds.Exit, "Escape")
	assertSequence(t, config.Keybinds.Backspace, "BackSpace")
	if !config.Behavior.StayActive {
		t.Fatalf("behavior.stay_active = false, want true")
	}
	if config.Behavior.DoubleClickTimeoutMS != 250 {
		t.Fatalf("behavior.double_click_timeout_ms = %d, want 250", config.Behavior.DoubleClickTimeoutMS)
	}
	if config.Appearance.GridOpacity != 0.4 {
		t.Fatalf("appearance.grid_opacity = %g, want 0.4", config.Appearance.GridOpacity)
	}
	if config.Appearance.GridLineWidth != 1 {
		t.Fatalf("appearance.grid_line_width = %d, want 1", config.Appearance.GridLineWidth)
	}
	if config.Appearance.LabelFontSize != 14 {
		t.Fatalf("appearance.label_font_size = %d, want 14", config.Appearance.LabelFontSize)
	}
}

func assertSequence(t *testing.T, got KeySequence, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("sequence length = %d, want %d: %+v", len(got), len(want), got.Names())
	}
	for i, name := range want {
		if string(got[i]) != name {
			t.Fatalf("sequence[%d] = %q, want %q: %+v", i, got[i], name, got.Names())
		}
	}
}

func writeConfigForTest(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mousekeys", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
