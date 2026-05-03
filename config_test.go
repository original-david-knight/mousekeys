package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDaemonCreatesDefaultConfigUsingXDGConfigHome(t *testing.T) {
	configHome := t.TempDir()
	home := t.TempDir()
	runtimeDir := t.TempDir()
	configPath := filepath.Join(configHome, "mousekeys", "config.toml")
	homeConfigPath := filepath.Join(home, ".config", "mousekeys", "config.toml")

	var stdout, stderr bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	env := mapEnv(map[string]string{
		"XDG_RUNTIME_DIR":             runtimeDir,
		"XDG_CONFIG_HOME":             configHome,
		"HOME":                        home,
		"WAYLAND_DISPLAY":             "wayland-test",
		"HYPRLAND_INSTANCE_SIGNATURE": "config-test",
	})

	go func() {
		done <- run(ctx, []string{"daemon"}, &stdout, &stderr, env)
	}()

	waitForFile(t, configPath, done)
	cancel()

	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("daemon returned %d; stderr=%q", code, stderr.String())
		}
	case <-time.After(time.Second):
		t.Fatal("daemon did not exit after cancellation")
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) returned error: %v", configPath, err)
	}
	if string(data) != defaultConfigTOML {
		t.Fatalf("default config contents differ\n--- got ---\n%s\n--- want ---\n%s", string(data), defaultConfigTOML)
	}
	if _, err := os.Stat(homeConfigPath); !os.IsNotExist(err) {
		t.Fatalf("daemon wrote HOME config path %s despite XDG_CONFIG_HOME; stat err=%v", homeConfigPath, err)
	}
}

func TestLoadConfigAppliesUserValuesAndDefaultsMissingFields(t *testing.T) {
	configHome := t.TempDir()
	configPath := filepath.Join(configHome, "mousekeys", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`[keybinds]
left_click = "Return"
right_click = "Shift-Return"

[behavior]
stay_active = false

[appearance]
grid_opacity = 0.75
`), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	loaded, err := LoadConfig(mapEnv(map[string]string{"XDG_CONFIG_HOME": configHome}))
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	if loaded.Created {
		t.Fatal("LoadConfig recreated an existing config")
	}
	config := loaded.Config
	if config.Grid.Size != 26 || config.Grid.SubgridPixelSize != 5 {
		t.Fatalf("grid defaults not applied: %+v", config.Grid)
	}
	if config.Keybinds.LeftClick != "Return" || config.Keybinds.RightClick != "Shift-Return" {
		t.Fatalf("user keybinds not applied: %+v", config.Keybinds)
	}
	if config.Keybinds.DoubleClick != "space space" || config.Keybinds.Exit != "Escape" || config.Keybinds.Backspace != "BackSpace" {
		t.Fatalf("missing keybind defaults not applied: %+v", config.Keybinds)
	}
	if config.Behavior.StayActive || config.Behavior.DoubleClickTimeoutMS != 250 {
		t.Fatalf("behavior values/defaults unexpected: %+v", config.Behavior)
	}
	if config.Appearance.GridOpacity != 0.75 || config.Appearance.GridLineWidth != 1 || config.Appearance.LabelFontSize != 14 {
		t.Fatalf("appearance values/defaults unexpected: %+v", config.Appearance)
	}
	if got := config.DoubleClickTimeout(); got != 250*time.Millisecond {
		t.Fatalf("DoubleClickTimeout = %s, want 250ms", got)
	}
}

func TestConfigRejectsInvalidValuesAndKeyNames(t *testing.T) {
	tests := []struct {
		name    string
		toml    string
		wantErr string
	}{
		{
			name: "invalid grid size",
			toml: `[grid]
size = 0
`,
			wantErr: "grid.size",
		},
		{
			name: "invalid opacity",
			toml: `[appearance]
grid_opacity = 1.5
`,
			wantErr: "appearance.grid_opacity",
		},
		{
			name: "invalid keysym",
			toml: `[keybinds]
left_click = "NotAKey"
`,
			wantErr: `keybinds.left_click: token "NotAKey": invalid key name "NotAKey"`,
		},
		{
			name: "unsupported modifier",
			toml: `[keybinds]
right_click = "Ctrl-space"
`,
			wantErr: "only Shift- is supported",
		},
		{
			name: "single-token double click",
			toml: `[keybinds]
double_click = "space"
`,
			wantErr: "keybinds.double_click must contain at least two key tokens",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tt.toml), 0o600); err != nil {
				t.Fatalf("WriteFile returned error: %v", err)
			}
			_, err := LoadConfigFile(path)
			if err == nil {
				t.Fatal("LoadConfigFile returned nil error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestKeySequenceParsing(t *testing.T) {
	sequence, err := ParseKeySequence("space space")
	if err != nil {
		t.Fatalf("ParseKeySequence returned error: %v", err)
	}
	want := KeySequence{{Key: "space"}, {Key: "space"}}
	if !sameKeySequence(sequence, want) {
		t.Fatalf("sequence = %+v, want %+v", sequence, want)
	}

	event := KeyboardEvent{Kind: KeyboardEventKey, Key: "space", State: KeyPressed}
	if !sequence[0].MatchesEvent(event) {
		t.Fatalf("unshifted space chord did not match event: %+v", event)
	}
	shiftEvent := KeyboardEvent{Kind: KeyboardEventKey, Key: "space", State: KeyPressed, Modifiers: ModifierState{Shift: true}}
	if sequence[0].MatchesEvent(shiftEvent) {
		t.Fatalf("unshifted space chord matched shifted event: %+v", shiftEvent)
	}
}

func TestShiftedChordParsingDoesNotAdvanceDoubleClickSequence(t *testing.T) {
	doubleClick, err := ParseKeySequence("space space")
	if err != nil {
		t.Fatalf("ParseKeySequence(space space) returned error: %v", err)
	}
	rightClick, err := ParseKeySequence("Shift-space")
	if err != nil {
		t.Fatalf("ParseKeySequence(Shift-space) returned error: %v", err)
	}
	if len(rightClick) != 1 || rightClick[0].Key != "space" || !rightClick[0].Shift {
		t.Fatalf("Shift-space parsed incorrectly: %+v", rightClick)
	}
	if rightClick[0] == doubleClick[0] {
		t.Fatalf("Shift-space equals unshifted double-click token: %+v", rightClick[0])
	}

	matcher := NewKeySequenceMatcher(doubleClick)
	if got := matcher.Push(rightClick[0]); got != KeySequenceNoMatch {
		t.Fatalf("Shift-space started double-click matcher: %s", got)
	}
	if got := matcher.Push(doubleClick[0]); got != KeySequencePartial {
		t.Fatalf("first space matcher result = %s, want partial", got)
	}
	if got := matcher.Push(rightClick[0]); got != KeySequenceNoMatch {
		t.Fatalf("Shift-space completed double-click matcher after first space: %s", got)
	}
}

func waitForFile(t *testing.T, path string, done <-chan int) {
	t.Helper()
	deadline := time.After(time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case code := <-done:
			t.Fatalf("daemon exited with %d before creating %s", code, path)
		case <-deadline:
			t.Fatalf("timed out waiting for %s", path)
		case <-ticker.C:
		}
	}
}

func sameKeySequence(a, b KeySequence) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
