package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

const defaultConfigTOML = `[grid]
size = 26                    # 26x26 main grid
subgrid_pixel_size = 5       # target pixel size per sub-cell

[keybinds]
left_click = "space"
right_click = "Shift-space"
double_click = "space space"
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
	Grid       GridConfig       `toml:"grid" json:"grid"`
	Keybinds   KeybindsConfig   `toml:"keybinds" json:"keybinds"`
	Behavior   BehaviorConfig   `toml:"behavior" json:"behavior"`
	Appearance AppearanceConfig `toml:"appearance" json:"appearance"`
	Parsed     ParsedKeybinds   `toml:"-" json:"-"`
}

type GridConfig struct {
	Size             int `toml:"size" json:"size"`
	SubgridPixelSize int `toml:"subgrid_pixel_size" json:"subgrid_pixel_size"`
}

type KeybindsConfig struct {
	LeftClick   string `toml:"left_click" json:"left_click"`
	RightClick  string `toml:"right_click" json:"right_click"`
	DoubleClick string `toml:"double_click" json:"double_click"`
	Exit        string `toml:"exit" json:"exit"`
	Backspace   string `toml:"backspace" json:"backspace"`
}

type BehaviorConfig struct {
	StayActive           bool `toml:"stay_active" json:"stay_active"`
	DoubleClickTimeoutMS int  `toml:"double_click_timeout_ms" json:"double_click_timeout_ms"`
}

type AppearanceConfig struct {
	GridOpacity   float64 `toml:"grid_opacity" json:"grid_opacity"`
	GridLineWidth int     `toml:"grid_line_width" json:"grid_line_width"`
	LabelFontSize int     `toml:"label_font_size" json:"label_font_size"`
}

type ParsedKeybinds struct {
	LeftClick   KeySequence
	RightClick  KeySequence
	DoubleClick KeySequence
	Exit        KeySequence
	Backspace   KeySequence
}

type LoadedConfig struct {
	Config  Config
	Path    string
	Created bool
}

func DefaultConfig() Config {
	config := Config{
		Grid: GridConfig{
			Size:             26,
			SubgridPixelSize: 5,
		},
		Keybinds: KeybindsConfig{
			LeftClick:   "space",
			RightClick:  "Shift-space",
			DoubleClick: "space space",
			Exit:        "Escape",
			Backspace:   "BackSpace",
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
	if err := config.Validate(); err != nil {
		panic(fmt.Sprintf("invalid built-in default config: %v", err))
	}
	return config
}

func LoadConfig(getenv getenvFunc) (LoadedConfig, error) {
	path, err := ConfigFilePath(getenv)
	if err != nil {
		return LoadedConfig{}, err
	}

	created, err := createDefaultConfigIfMissing(path)
	if err != nil {
		return LoadedConfig{}, err
	}

	config, err := LoadConfigFile(path)
	if err != nil {
		return LoadedConfig{}, err
	}
	return LoadedConfig{Config: config, Path: path, Created: created}, nil
}

func ConfigFilePath(getenv getenvFunc) (string, error) {
	dir, err := xdgConfigDir(getenv)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "mousekeys", "config.toml"), nil
}

func xdgConfigDir(getenv getenvFunc) (string, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	if xdg := strings.TrimSpace(getenv("XDG_CONFIG_HOME")); xdg != "" {
		if !filepath.IsAbs(xdg) {
			return "", fmt.Errorf("invalid XDG_CONFIG_HOME %q: must be an absolute path", xdg)
		}
		return xdg, nil
	}

	home := strings.TrimSpace(getenv("HOME"))
	if home == "" {
		return "", fmt.Errorf("cannot locate config directory: set XDG_CONFIG_HOME or HOME")
	}
	if !filepath.IsAbs(home) {
		return "", fmt.Errorf("invalid HOME %q: must be an absolute path", home)
	}
	return filepath.Join(home, ".config"), nil
}

func createDefaultConfigIfMissing(path string) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, fmt.Errorf("create config directory %q: %w", filepath.Dir(path), err)
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("create default config %q: %w", path, err)
	}
	defer file.Close()

	if _, err := file.WriteString(defaultConfigTOML); err != nil {
		return true, fmt.Errorf("write default config %q: %w", path, err)
	}
	if err := file.Sync(); err != nil {
		return true, fmt.Errorf("sync default config %q: %w", path, err)
	}
	return true, nil
}

func LoadConfigFile(path string) (Config, error) {
	config := DefaultConfig()
	metadata, err := toml.DecodeFile(path, &config)
	if err != nil {
		return Config{}, fmt.Errorf("load config %q: %w", path, err)
	}
	if undecoded := metadata.Undecoded(); len(undecoded) > 0 {
		fields := make([]string, len(undecoded))
		for i, key := range undecoded {
			fields[i] = key.String()
		}
		return Config{}, fmt.Errorf("load config %q: unknown field(s): %s", path, strings.Join(fields, ", "))
	}
	if err := config.Validate(); err != nil {
		return Config{}, fmt.Errorf("load config %q: %w", path, err)
	}
	return config, nil
}

func (c *Config) Validate() error {
	if c.Grid.Size < 1 || c.Grid.Size > 26 {
		return fmt.Errorf("grid.size must be between 1 and 26, got %d", c.Grid.Size)
	}
	if c.Grid.SubgridPixelSize < 1 {
		return fmt.Errorf("grid.subgrid_pixel_size must be positive, got %d", c.Grid.SubgridPixelSize)
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

	parsed, err := parseKeybinds(c.Keybinds)
	if err != nil {
		return err
	}
	c.Parsed = parsed
	return nil
}

func (c Config) DoubleClickTimeout() time.Duration {
	return time.Duration(c.Behavior.DoubleClickTimeoutMS) * time.Millisecond
}

func parseKeybinds(keybinds KeybindsConfig) (ParsedKeybinds, error) {
	leftClick, err := parseSingleChord("keybinds.left_click", keybinds.LeftClick)
	if err != nil {
		return ParsedKeybinds{}, err
	}
	rightClick, err := parseSingleChord("keybinds.right_click", keybinds.RightClick)
	if err != nil {
		return ParsedKeybinds{}, err
	}
	doubleClick, err := parseNamedSequence("keybinds.double_click", keybinds.DoubleClick)
	if err != nil {
		return ParsedKeybinds{}, err
	}
	if len(doubleClick) < 2 {
		return ParsedKeybinds{}, fmt.Errorf("keybinds.double_click must contain at least two key tokens, got %q", keybinds.DoubleClick)
	}
	exit, err := parseSingleChord("keybinds.exit", keybinds.Exit)
	if err != nil {
		return ParsedKeybinds{}, err
	}
	backspace, err := parseSingleChord("keybinds.backspace", keybinds.Backspace)
	if err != nil {
		return ParsedKeybinds{}, err
	}

	return ParsedKeybinds{
		LeftClick:   leftClick,
		RightClick:  rightClick,
		DoubleClick: doubleClick,
		Exit:        exit,
		Backspace:   backspace,
	}, nil
}

func parseSingleChord(field, raw string) (KeySequence, error) {
	sequence, err := parseNamedSequence(field, raw)
	if err != nil {
		return nil, err
	}
	if len(sequence) != 1 {
		return nil, fmt.Errorf("%s must contain exactly one key token, got %q", field, raw)
	}
	return sequence, nil
}

func parseNamedSequence(field, raw string) (KeySequence, error) {
	sequence, err := ParseKeySequence(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", field, err)
	}
	return sequence, nil
}

type KeyChord struct {
	Key   string `json:"key"`
	Shift bool   `json:"shift,omitempty"`
}

func (c KeyChord) String() string {
	if c.Shift {
		return "Shift-" + c.Key
	}
	return c.Key
}

func (c KeyChord) MatchesEvent(event KeyboardEvent) bool {
	return event.Kind == KeyboardEventKey &&
		event.State == KeyPressed &&
		!event.Repeated &&
		event.Key == c.Key &&
		event.Modifiers.Shift == c.Shift &&
		!event.Modifiers.Ctrl &&
		!event.Modifiers.Alt &&
		!event.Modifiers.Super
}

type KeySequence []KeyChord

func ParseKeySequence(raw string) (KeySequence, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("key sequence is empty")
	}

	tokens := strings.Fields(trimmed)
	sequence := make(KeySequence, 0, len(tokens))
	for _, token := range tokens {
		chord, err := ParseKeyChord(token)
		if err != nil {
			return nil, fmt.Errorf("token %q: %w", token, err)
		}
		sequence = append(sequence, chord)
	}
	return sequence, nil
}

func ParseKeyChord(token string) (KeyChord, error) {
	if token == "" {
		return KeyChord{}, fmt.Errorf("key token is empty")
	}

	shift := false
	name := token
	if strings.HasPrefix(token, "Shift-") {
		shift = true
		name = strings.TrimPrefix(token, "Shift-")
		if name == "" {
			return KeyChord{}, fmt.Errorf("Shift- prefix must be followed by a key name")
		}
	} else if strings.Contains(token, "-") {
		return KeyChord{}, fmt.Errorf("unsupported modifier prefix in %q; only Shift- is supported", token)
	}
	if strings.Contains(name, "-") {
		return KeyChord{}, fmt.Errorf("invalid key name %q: xkbcommon keysym names do not use '-' characters", name)
	}
	if !validKeysymName(name) {
		return KeyChord{}, fmt.Errorf("invalid key name %q: expected a case-sensitive xkbcommon keysym name", name)
	}
	return KeyChord{Key: name, Shift: shift}, nil
}

type KeySequenceMatch string

const (
	KeySequenceNoMatch  KeySequenceMatch = "none"
	KeySequencePartial  KeySequenceMatch = "partial"
	KeySequenceComplete KeySequenceMatch = "complete"
)

type KeySequenceMatcher struct {
	sequence KeySequence
	matched  int
}

func NewKeySequenceMatcher(sequence KeySequence) *KeySequenceMatcher {
	copied := make(KeySequence, len(sequence))
	copy(copied, sequence)
	return &KeySequenceMatcher{sequence: copied}
}

func (m *KeySequenceMatcher) Push(chord KeyChord) KeySequenceMatch {
	if len(m.sequence) == 0 {
		m.matched = 0
		return KeySequenceNoMatch
	}
	if chord == m.sequence[m.matched] {
		m.matched++
		if m.matched == len(m.sequence) {
			m.matched = 0
			return KeySequenceComplete
		}
		return KeySequencePartial
	}

	m.matched = 0
	if chord == m.sequence[0] {
		if len(m.sequence) == 1 {
			return KeySequenceComplete
		}
		m.matched = 1
		return KeySequencePartial
	}
	return KeySequenceNoMatch
}

func validKeysymName(name string) bool {
	_, ok := xkbKeysymNames[name]
	return ok
}
