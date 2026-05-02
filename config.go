package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const DefaultConfigTOML = `[grid]
size = 26                    # 26x26 main grid
subgrid_pixel_size = 5       # target pixel size per sub-cell

[keybinds]
left_click = "Return"
right_click = "space"
double_click = "Return Return"
exit = "Escape"
backspace = "BackSpace"

[behavior]
stay_active = true           # main grid reappears after click
double_click_timeout_ms = 250

[appearance]
grid_opacity = 0.4
grid_line_width = 1
label_font_size = 14
`

type Config struct {
	Grid       GridConfig       `toml:"grid"`
	Keybinds   KeybindsConfig   `toml:"keybinds"`
	Behavior   BehaviorConfig   `toml:"behavior"`
	Appearance AppearanceConfig `toml:"appearance"`
}

type GridConfig struct {
	Size             int `toml:"size"`
	SubgridPixelSize int `toml:"subgrid_pixel_size"`
}

type KeybindsConfig struct {
	LeftClick     KeySequence `toml:"left_click"`
	RightClick    KeySequence `toml:"right_click"`
	DoubleClick   KeySequence `toml:"double_click"`
	CommitPartial KeySequence `toml:"commit_partial"`
	Exit          KeySequence `toml:"exit"`
	Backspace     KeySequence `toml:"backspace"`
}

type BehaviorConfig struct {
	StayActive           bool `toml:"stay_active"`
	DoubleClickTimeoutMS int  `toml:"double_click_timeout_ms"`
}

type AppearanceConfig struct {
	GridOpacity   float64 `toml:"grid_opacity"`
	GridLineWidth int     `toml:"grid_line_width"`
	LabelFontSize int     `toml:"label_font_size"`
}

type KeySym string

type KeySequence []KeySym

func DefaultConfig() Config {
	return Config{
		Grid: GridConfig{
			Size:             26,
			SubgridPixelSize: 5,
		},
		Keybinds: KeybindsConfig{
			LeftClick:     mustParseKeySequence("Return"),
			RightClick:    mustParseKeySequence("space"),
			DoubleClick:   mustParseKeySequence("Return Return"),
			CommitPartial: mustParseKeySequence("Tab"),
			Exit:          mustParseKeySequence("Escape"),
			Backspace:     mustParseKeySequence("BackSpace"),
		},
		Behavior: BehaviorConfig{
			StayActive:           true,
			DoubleClickTimeoutMS: 250,
		},
		Appearance: AppearanceConfig{
			GridOpacity:   0.4,
			GridLineWidth: 1,
			LabelFontSize: 14,
		},
	}
}

func ConfigPath() (string, error) {
	configHome, err := xdgConfigHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(configHome, "mousekeys", "config.toml"), nil
}

func LoadConfig() (Config, error) {
	path, err := ConfigPath()
	if err != nil {
		return Config{}, err
	}
	return LoadConfigFile(path)
}

func LoadConfigFile(path string) (Config, error) {
	if path == "" {
		return Config{}, fmt.Errorf("config path is required")
	}
	if err := ensureConfigFile(path); err != nil {
		return Config{}, err
	}

	config := DefaultConfig()
	meta, err := toml.DecodeFile(path, &config)
	if err != nil {
		return Config{}, fmt.Errorf("load config %q: %w", path, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, key := range undecoded {
			keys = append(keys, key.String())
		}
		return Config{}, fmt.Errorf("load config %q: unknown config key(s): %s", path, strings.Join(keys, ", "))
	}
	if err := config.Validate(); err != nil {
		return Config{}, fmt.Errorf("load config %q: %w", path, err)
	}
	return config, nil
}

func (c Config) Validate() error {
	if c.Grid.Size < 1 || c.Grid.Size > 26 {
		return fmt.Errorf("grid.size must be between 1 and 26, got %d", c.Grid.Size)
	}
	if c.Grid.SubgridPixelSize < 1 {
		return fmt.Errorf("grid.subgrid_pixel_size must be positive, got %d", c.Grid.SubgridPixelSize)
	}

	if err := validateKeySequence("keybinds.left_click", c.Keybinds.LeftClick); err != nil {
		return err
	}
	if err := validateKeySequence("keybinds.right_click", c.Keybinds.RightClick); err != nil {
		return err
	}
	if err := validateKeySequence("keybinds.double_click", c.Keybinds.DoubleClick); err != nil {
		return err
	}
	if err := validateKeySequence("keybinds.commit_partial", c.Keybinds.CommitPartial); err != nil {
		return err
	}
	if err := validateKeySequence("keybinds.exit", c.Keybinds.Exit); err != nil {
		return err
	}
	if err := validateKeySequence("keybinds.backspace", c.Keybinds.Backspace); err != nil {
		return err
	}

	if c.Behavior.DoubleClickTimeoutMS < 1 {
		return fmt.Errorf("behavior.double_click_timeout_ms must be positive, got %d", c.Behavior.DoubleClickTimeoutMS)
	}
	if c.Appearance.GridOpacity < 0 || c.Appearance.GridOpacity > 1 {
		return fmt.Errorf("appearance.grid_opacity must be between 0 and 1, got %g", c.Appearance.GridOpacity)
	}
	if c.Appearance.GridLineWidth < 1 {
		return fmt.Errorf("appearance.grid_line_width must be positive, got %d", c.Appearance.GridLineWidth)
	}
	if c.Appearance.LabelFontSize < 1 {
		return fmt.Errorf("appearance.label_font_size must be positive, got %d", c.Appearance.LabelFontSize)
	}
	return nil
}

func ParseKeySequence(value string) (KeySequence, error) {
	names := strings.Fields(value)
	if len(names) == 0 {
		return nil, fmt.Errorf("key sequence must contain at least one xkbcommon keysym name")
	}

	sequence := make(KeySequence, 0, len(names))
	for _, name := range names {
		if !validXKBKeysymName(name) {
			return nil, fmt.Errorf("invalid xkbcommon keysym name %q", name)
		}
		sequence = append(sequence, KeySym(name))
	}
	return sequence, nil
}

func (s *KeySequence) UnmarshalText(text []byte) error {
	sequence, err := ParseKeySequence(string(text))
	if err != nil {
		return err
	}
	*s = sequence
	return nil
}

func (s KeySequence) String() string {
	names := make([]string, 0, len(s))
	for _, sym := range s {
		names = append(names, string(sym))
	}
	return strings.Join(names, " ")
}

func (s KeySequence) Names() []string {
	names := make([]string, 0, len(s))
	for _, sym := range s {
		names = append(names, string(sym))
	}
	return names
}

func ensureConfigFile(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat config file %q: %w", path, err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config directory %q: %w", filepath.Dir(path), err)
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("create default config file %q: %w", path, err)
	}

	_, writeErr := file.WriteString(DefaultConfigTOML)
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("write default config file %q: %w", path, writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close default config file %q: %w", path, closeErr)
	}
	return nil
}

func xdgConfigHome() (string, error) {
	if value := os.Getenv("XDG_CONFIG_HOME"); value != "" && filepath.IsAbs(value) {
		return value, nil
	}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("determine config directory: XDG_CONFIG_HOME is unset or relative and HOME is unavailable")
	}
	return filepath.Join(home, ".config"), nil
}

func validateKeySequence(field string, sequence KeySequence) error {
	if len(sequence) == 0 {
		return fmt.Errorf("%s must contain at least one xkbcommon keysym name", field)
	}
	for _, sym := range sequence {
		name := string(sym)
		if !validXKBKeysymName(name) {
			return fmt.Errorf("%s contains invalid xkbcommon keysym name %q", field, name)
		}
	}
	return nil
}

func mustParseKeySequence(value string) KeySequence {
	sequence, err := ParseKeySequence(value)
	if err != nil {
		panic(err)
	}
	return sequence
}
