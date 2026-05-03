package main

import (
	"context"
	"fmt"
	"sort"
	"time"
)

const keyboardKeymapFormatXKBV1 uint32 = 1

type RawKeyboardEventSource interface {
	RawEvents(context.Context) (<-chan RawKeyboardEvent, error)
}

type RawKeyboardEventKind string

const (
	RawKeyboardEventKeymap     RawKeyboardEventKind = "keymap"
	RawKeyboardEventEnter      RawKeyboardEventKind = "enter"
	RawKeyboardEventKey        RawKeyboardEventKind = "key"
	RawKeyboardEventModifiers  RawKeyboardEventKind = "modifiers"
	RawKeyboardEventLeave      RawKeyboardEventKind = "leave"
	RawKeyboardEventDestroy    RawKeyboardEventKind = "destroy"
	RawKeyboardEventRepeatInfo RawKeyboardEventKind = "repeat_info"
	RawKeyboardEventError      RawKeyboardEventKind = "error"
)

type RawKeyboardEvent struct {
	Kind          RawKeyboardEventKind
	KeymapFormat  uint32
	Keymap        []byte
	Keycode       uint32
	PressedKeys   []uint32
	Pressed       bool
	Time          time.Time
	ModsDepressed uint32
	ModsLatched   uint32
	ModsLocked    uint32
	Group         uint32
	RepeatRate    int32
	RepeatDelayMS int32
	Err           error
}

type KeyboardTokenKind string

const (
	KeyboardTokenLetter  KeyboardTokenKind = "letter"
	KeyboardTokenCommand KeyboardTokenKind = "command"
)

type KeyboardCommand string

const (
	KeyboardCommandLeftClick     KeyboardCommand = "left_click"
	KeyboardCommandRightClick    KeyboardCommand = "right_click"
	KeyboardCommandDoubleClick   KeyboardCommand = "double_click"
	KeyboardCommandCommitPartial KeyboardCommand = "commit_partial"
	KeyboardCommandExit          KeyboardCommand = "exit"
	KeyboardCommandBackspace     KeyboardCommand = "backspace"
)

type KeyboardToken struct {
	Kind      KeyboardTokenKind
	Letter    byte
	KeySym    KeySym
	Keycode   uint32
	Modifiers KeyboardModifiers
	Commands  []KeyboardCommand
	Time      time.Time
	Repeat    bool
	Released  bool
}

type KeyboardInputMapper struct {
	bindings         []keyboardBinding
	interestingKeys  map[KeySym]struct{}
	sequenceBindings []keyboardBinding
}

type keyboardBinding struct {
	command  KeyboardCommand
	sequence KeySequence
}

var keySymInputAliases = map[KeySym][]KeySym{
	"Return": {"KP_Enter"},
}

var subgridNavigationKeySyms = map[KeySym]struct{}{
	"Left":     {},
	"Down":     {},
	"Up":       {},
	"Right":    {},
	"KP_Left":  {},
	"KP_Down":  {},
	"KP_Up":    {},
	"KP_Right": {},
}

func keySymSatisfiesBinding(input KeySym, binding KeySym) bool {
	if input == binding {
		return true
	}
	for _, alias := range keySymInputAliases[binding] {
		if input == alias {
			return true
		}
	}
	return false
}

func keyStepAcceptedInputs(binding KeyBindingStep) []KeySym {
	inputs := []KeySym{binding.KeySym}
	inputs = append(inputs, keySymInputAliases[binding.KeySym]...)
	return inputs
}

func NewKeyboardInputMapper(config Config) (*KeyboardInputMapper, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	bindings := []keyboardBinding{
		{command: KeyboardCommandLeftClick, sequence: config.Keybinds.LeftClick},
		{command: KeyboardCommandRightClick, sequence: config.Keybinds.RightClick},
		{command: KeyboardCommandDoubleClick, sequence: config.Keybinds.DoubleClick},
		{command: KeyboardCommandCommitPartial, sequence: config.Keybinds.CommitPartial},
		{command: KeyboardCommandExit, sequence: config.Keybinds.Exit},
		{command: KeyboardCommandBackspace, sequence: config.Keybinds.Backspace},
	}
	interesting := make(map[KeySym]struct{})
	var sequenceBindings []keyboardBinding
	for _, binding := range bindings {
		for _, step := range binding.sequence {
			for _, input := range keyStepAcceptedInputs(step) {
				interesting[input] = struct{}{}
			}
		}
		if len(binding.sequence) > 1 {
			sequenceBindings = append(sequenceBindings, binding)
		}
	}
	for keysym := range subgridNavigationKeySyms {
		interesting[keysym] = struct{}{}
	}
	return &KeyboardInputMapper{
		bindings:         bindings,
		interestingKeys:  interesting,
		sequenceBindings: sequenceBindings,
	}, nil
}

func (m *KeyboardInputMapper) Tokens(ctx context.Context, source KeyboardEventSource) (<-chan KeyboardToken, error) {
	if m == nil {
		return nil, fmt.Errorf("keyboard input mapper is nil")
	}
	if source == nil {
		return nil, fmt.Errorf("keyboard event source is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	events, err := source.Events(ctx)
	if err != nil {
		return nil, err
	}

	tokens := make(chan KeyboardToken, 32)
	go func() {
		defer close(tokens)
		matcher := newKeyboardSequenceMatcher(m.sequenceBindings)
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-events:
				if !ok {
					return
				}
				token, ok := m.Translate(event)
				if !ok {
					continue
				}
				if !token.Released && !token.Repeat {
					matcher.Apply(&token)
				}
				select {
				case tokens <- token:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return tokens, nil
}

func (m *KeyboardInputMapper) Translate(event KeyboardEvent) (KeyboardToken, bool) {
	if m == nil {
		return KeyboardToken{}, false
	}
	kind := event.Kind
	if kind == "" {
		kind = KeyboardEventKey
	}
	if kind != KeyboardEventKey || event.Key == "" || event.Repeat {
		return KeyboardToken{}, false
	}

	keysym := KeySym(event.Key)
	if !event.Pressed {
		return subgridNavigationReleaseToken(event, keysym)
	}

	commands := m.commandsForEvent(keysym, event.Modifiers)
	if letter, ok := letterFromKeysymName(event.Key); ok {
		return KeyboardToken{
			Kind:      KeyboardTokenLetter,
			Letter:    letter,
			KeySym:    keysym,
			Keycode:   event.Keycode,
			Modifiers: event.Modifiers,
			Commands:  commands,
			Time:      event.Time,
		}, true
	}
	if len(commands) == 0 {
		if _, interesting := m.interestingKeys[keysym]; !interesting {
			return KeyboardToken{}, false
		}
	}
	return KeyboardToken{
		Kind:      KeyboardTokenCommand,
		KeySym:    keysym,
		Keycode:   event.Keycode,
		Modifiers: event.Modifiers,
		Commands:  commands,
		Time:      event.Time,
	}, true
}

func subgridNavigationReleaseToken(event KeyboardEvent, keysym KeySym) (KeyboardToken, bool) {
	token := KeyboardToken{
		KeySym:    keysym,
		Keycode:   event.Keycode,
		Modifiers: event.Modifiers,
		Time:      event.Time,
		Released:  true,
	}
	if letter, ok := letterFromKeysymName(event.Key); ok {
		token.Kind = KeyboardTokenLetter
		token.Letter = letter
		if _, ok := subgridMoveDirectionFromToken(token); !ok {
			return KeyboardToken{}, false
		}
		return token, true
	}
	token.Kind = KeyboardTokenCommand
	if _, ok := arrowSubgridMoveDirection(keysym); !ok {
		return KeyboardToken{}, false
	}
	return token, true
}

func (m *KeyboardInputMapper) commandsForEvent(keysym KeySym, modifiers KeyboardModifiers) []KeyboardCommand {
	if commands := m.commandsForSingleStep(keysym, modifiers, true, true); len(commands) > 0 {
		return commands
	}
	if commands := m.commandsForSingleStep(keysym, modifiers, true, false); len(commands) > 0 {
		return commands
	}
	if modifiers.Empty() {
		return nil
	}
	if commands := m.commandsForSingleStep(keysym, KeyboardModifiers{}, false, true); len(commands) > 0 {
		return commands
	}
	return m.commandsForSingleStep(keysym, KeyboardModifiers{}, false, false)
}

func (m *KeyboardInputMapper) commandsForSingleStep(keysym KeySym, modifiers KeyboardModifiers, exactModifiers bool, exactKey bool) []KeyboardCommand {
	var commands []KeyboardCommand
	for _, binding := range m.bindings {
		if len(binding.sequence) != 1 {
			continue
		}
		step := binding.sequence[0]
		if exactModifiers {
			if step.Modifiers != modifiers {
				continue
			}
		} else if !step.Modifiers.Empty() {
			continue
		}
		if exactKey {
			if step.KeySym != keysym {
				continue
			}
		} else if !keySymSatisfiesBinding(keysym, step.KeySym) {
			continue
		}
		commands = append(commands, binding.command)
	}
	sort.Slice(commands, func(i, j int) bool {
		return commands[i] < commands[j]
	})
	return commands
}

type keyboardSequenceMatcher struct {
	bindings []keyboardBinding
	progress []int
}

func newKeyboardSequenceMatcher(bindings []keyboardBinding) *keyboardSequenceMatcher {
	return &keyboardSequenceMatcher{
		bindings: append([]keyboardBinding(nil), bindings...),
		progress: make([]int, len(bindings)),
	}
}

func (m *keyboardSequenceMatcher) Apply(token *KeyboardToken) {
	if m == nil || token == nil || token.KeySym == "" {
		return
	}

	for i, binding := range m.bindings {
		if len(binding.sequence) <= 1 {
			continue
		}
		progress := m.progress[i]
		if progress < 0 || progress >= len(binding.sequence) {
			progress = 0
		}

		switch {
		case keyStepSatisfiesToken(*token, binding.sequence[progress]):
			progress++
			if progress == len(binding.sequence) {
				token.Commands = appendKeyboardCommand(token.Commands, binding.command)
				progress = 0
			}
		case keyStepSatisfiesToken(*token, binding.sequence[0]):
			progress = 1
		default:
			progress = 0
		}
		m.progress[i] = progress
	}
	sort.Slice(token.Commands, func(i, j int) bool {
		return token.Commands[i] < token.Commands[j]
	})
}

func keyStepSatisfiesToken(token KeyboardToken, binding KeyBindingStep) bool {
	if token.Modifiers != binding.Modifiers {
		return false
	}
	return keySymSatisfiesBinding(token.KeySym, binding.KeySym)
}

func appendKeyboardCommand(commands []KeyboardCommand, command KeyboardCommand) []KeyboardCommand {
	for _, got := range commands {
		if got == command {
			return commands
		}
	}
	return append(commands, command)
}

func letterFromKeysymName(name string) (byte, bool) {
	if len(name) != 1 {
		return 0, false
	}
	ch := name[0]
	switch {
	case ch >= 'A' && ch <= 'Z':
		return ch, true
	case ch >= 'a' && ch <= 'z':
		return ch - 'a' + 'A', true
	default:
		return 0, false
	}
}

type xkbKeyboardEventSource struct {
	source RawKeyboardEventSource
	trace  TraceRecorder
}

func NewXKBKeyboardEventSource(source RawKeyboardEventSource, traces ...TraceRecorder) KeyboardEventSource {
	trace := TraceRecorder(noopTraceRecorder{})
	if len(traces) > 0 && traces[0] != nil {
		trace = traces[0]
	}
	return &xkbKeyboardEventSource{source: source, trace: trace}
}

func (s *xkbKeyboardEventSource) recordTrace(action string, fields map[string]any) {
	if s == nil || s.trace == nil {
		return
	}
	s.trace.Record("keyboard", action, fields)
}

func (s *xkbKeyboardEventSource) Events(ctx context.Context) (<-chan KeyboardEvent, error) {
	if s == nil || s.source == nil {
		return nil, fmt.Errorf("raw keyboard event source is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	rawEvents, err := s.source.RawEvents(ctx)
	if err != nil {
		return nil, err
	}

	events := make(chan KeyboardEvent, 32)
	go s.translateRawEvents(ctx, rawEvents, events)
	return events, nil
}

func (s *xkbKeyboardEventSource) translateRawEvents(ctx context.Context, rawEvents <-chan RawKeyboardEvent, events chan<- KeyboardEvent) {
	defer close(events)

	var keymap keyboardKeymapState
	defer func() {
		if keymap != nil {
			keymap.Close()
		}
	}()
	pressed := map[uint32]struct{}{}

	for {
		select {
		case <-ctx.Done():
			return
		case raw, ok := <-rawEvents:
			if !ok {
				return
			}
			switch raw.Kind {
			case RawKeyboardEventKeymap:
				if keymap != nil {
					keymap.Close()
					keymap = nil
				}
				next, err := newKeyboardKeymapState(raw.KeymapFormat, raw.Keymap)
				if err != nil {
					s.recordTrace("xkb_error", map[string]any{"error": err.Error()})
					s.emit(ctx, events, KeyboardEvent{Kind: KeyboardEventError, Time: raw.Time, Err: err})
					continue
				}
				keymap = next
				clear(pressed)
				s.recordTrace("xkb_keymap_ready", nil)
			case RawKeyboardEventEnter:
				clear(pressed)
				if keymap != nil {
					keymap.Reset()
					for _, keycode := range raw.PressedKeys {
						pressed[keycode] = struct{}{}
						keymap.UpdateKey(keycode, true)
					}
				}
				s.recordTrace("xkb_enter", map[string]any{"pressed_key_count": len(raw.PressedKeys)})
				s.emit(ctx, events, KeyboardEvent{Kind: KeyboardEventEnter, Time: raw.Time})
			case RawKeyboardEventModifiers:
				if keymap != nil {
					keymap.UpdateMask(raw.ModsDepressed, raw.ModsLatched, raw.ModsLocked, raw.Group)
				}
			case RawKeyboardEventKey:
				event, ok := translateRawKeyEvent(keymap, pressed, raw)
				if ok {
					fields := map[string]any{
						"keycode": event.Keycode,
						"pressed": event.Pressed,
						"repeat":  event.Repeat,
					}
					if event.Key != "" {
						fields["key"] = event.Key
					}
					if event.Err != nil {
						fields["error"] = event.Err.Error()
					}
					s.recordTrace("xkb_key", fields)
					s.emit(ctx, events, event)
				}
			case RawKeyboardEventLeave:
				clear(pressed)
				if keymap != nil {
					keymap.Reset()
				}
				s.recordTrace("xkb_leave", nil)
				s.emit(ctx, events, KeyboardEvent{Kind: KeyboardEventLeave, Time: raw.Time})
			case RawKeyboardEventDestroy:
				clear(pressed)
				if keymap != nil {
					keymap.Reset()
					keymap.Close()
					keymap = nil
				}
				s.recordTrace("xkb_destroy", nil)
				s.emit(ctx, events, KeyboardEvent{Kind: KeyboardEventDestroy, Time: raw.Time})
			case RawKeyboardEventError:
				if raw.Err != nil {
					s.recordTrace("raw_event_error", map[string]any{"error": raw.Err.Error()})
				}
				s.emit(ctx, events, KeyboardEvent{Kind: KeyboardEventError, Time: raw.Time, Err: raw.Err})
			case RawKeyboardEventRepeatInfo:
			}
		}
	}
}

func translateRawKeyEvent(keymap keyboardKeymapState, pressed map[uint32]struct{}, raw RawKeyboardEvent) (KeyboardEvent, bool) {
	if keymap == nil {
		return KeyboardEvent{}, false
	}

	keysym, err := keymap.KeySymName(raw.Keycode)
	if err != nil {
		return KeyboardEvent{Kind: KeyboardEventError, Time: raw.Time, Err: err}, true
	}
	if keysym == "" {
		if !raw.Pressed {
			delete(pressed, raw.Keycode)
			keymap.UpdateKey(raw.Keycode, false)
		}
		return KeyboardEvent{}, false
	}

	_, repeat := pressed[raw.Keycode]
	if raw.Pressed {
		if !repeat {
			pressed[raw.Keycode] = struct{}{}
			keymap.UpdateKey(raw.Keycode, true)
		}
	} else {
		delete(pressed, raw.Keycode)
		keymap.UpdateKey(raw.Keycode, false)
	}

	return KeyboardEvent{
		Kind:      KeyboardEventKey,
		Key:       keysym,
		Keycode:   raw.Keycode,
		Pressed:   raw.Pressed,
		Repeat:    raw.Pressed && repeat,
		Modifiers: keymap.Modifiers(),
		Time:      raw.Time,
	}, true
}

func (s *xkbKeyboardEventSource) emit(ctx context.Context, events chan<- KeyboardEvent, event KeyboardEvent) bool {
	select {
	case events <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

type keyboardKeymapState interface {
	KeySymName(keycode uint32) (string, error)
	Modifiers() KeyboardModifiers
	UpdateKey(keycode uint32, pressed bool)
	UpdateMask(depressed, latched, locked, group uint32)
	Reset()
	Close()
}
