package main

import (
	"fmt"
	"strings"
	"unicode"
)

type KeyboardTokenKind string

const (
	KeyboardTokenLetter  KeyboardTokenKind = "letter"
	KeyboardTokenCommand KeyboardTokenKind = "command"
)

type KeyboardCommand string

const (
	KeyboardCommandLeftClick   KeyboardCommand = "left_click"
	KeyboardCommandRightClick  KeyboardCommand = "right_click"
	KeyboardCommandDoubleClick KeyboardCommand = "double_click"
	KeyboardCommandExit        KeyboardCommand = "exit"
	KeyboardCommandBackspace   KeyboardCommand = "backspace"
)

type KeyboardInputToken struct {
	Kind    KeyboardTokenKind `json:"kind"`
	Letter  string            `json:"letter,omitempty"`
	Command KeyboardCommand   `json:"command,omitempty"`
	Chord   KeyChord          `json:"chord"`
}

type KeyboardInputTranslator struct {
	config       Config
	session      KeyboardSessionState
	doubleClick  *KeySequenceMatcher
	lastSequence KeySequenceMatch
}

func NewKeyboardInputTranslator(config Config) (*KeyboardInputTranslator, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &KeyboardInputTranslator{
		config:      config,
		doubleClick: NewKeySequenceMatcher(config.Parsed.DoubleClick),
	}, nil
}

func (t *KeyboardInputTranslator) SessionState() KeyboardSessionState {
	return t.session
}

func (t *KeyboardInputTranslator) LastSequenceMatch() KeySequenceMatch {
	return t.lastSequence
}

func (t *KeyboardInputTranslator) Apply(event KeyboardEvent) (KeyboardInputToken, bool, error) {
	if t == nil {
		return KeyboardInputToken{}, false, fmt.Errorf("keyboard input translator is nil")
	}
	event, err := t.session.ApplyEvent(event)
	if err != nil {
		return KeyboardInputToken{}, false, err
	}
	switch event.Kind {
	case KeyboardEventKeymap, KeyboardEventLeave, KeyboardEventDestroy:
		t.resetSequences()
		return KeyboardInputToken{}, false, nil
	case KeyboardEventKey:
	default:
		return KeyboardInputToken{}, false, nil
	}
	if event.State != KeyPressed || event.Repeated {
		return KeyboardInputToken{}, false, nil
	}

	chord, ok := KeyChordFromEvent(event)
	if !ok {
		t.resetSequences()
		return KeyboardInputToken{}, false, nil
	}
	if token, ok := t.singleCommandToken(chord); ok {
		t.resetSequences()
		return token, true, nil
	}

	t.lastSequence = t.doubleClick.Push(chord)
	if t.lastSequence == KeySequenceComplete {
		return KeyboardInputToken{
			Kind:    KeyboardTokenCommand,
			Command: KeyboardCommandDoubleClick,
			Chord:   chord,
		}, true, nil
	}

	if len(t.config.Parsed.LeftClick) == 1 && t.config.Parsed.LeftClick[0] == chord {
		return KeyboardInputToken{
			Kind:    KeyboardTokenCommand,
			Command: KeyboardCommandLeftClick,
			Chord:   chord,
		}, true, nil
	}
	if letter, ok := coordinateLetterFromKey(event.Key); ok {
		return KeyboardInputToken{
			Kind:   KeyboardTokenLetter,
			Letter: letter,
			Chord:  chord,
		}, true, nil
	}
	return KeyboardInputToken{}, false, nil
}

func (t *KeyboardInputTranslator) singleCommandToken(chord KeyChord) (KeyboardInputToken, bool) {
	commands := []struct {
		sequence KeySequence
		command  KeyboardCommand
	}{
		{sequence: t.config.Parsed.RightClick, command: KeyboardCommandRightClick},
		{sequence: t.config.Parsed.Exit, command: KeyboardCommandExit},
		{sequence: t.config.Parsed.Backspace, command: KeyboardCommandBackspace},
	}
	for _, candidate := range commands {
		if len(candidate.sequence) == 1 && candidate.sequence[0] == chord {
			return KeyboardInputToken{
				Kind:    KeyboardTokenCommand,
				Command: candidate.command,
				Chord:   chord,
			}, true
		}
	}
	return KeyboardInputToken{}, false
}

func (t *KeyboardInputTranslator) resetSequences() {
	t.doubleClick = NewKeySequenceMatcher(t.config.Parsed.DoubleClick)
	t.lastSequence = KeySequenceNoMatch
}

func KeyChordFromEvent(event KeyboardEvent) (KeyChord, bool) {
	if event.Kind != KeyboardEventKey || event.Key == "" {
		return KeyChord{}, false
	}
	if event.Modifiers.Ctrl || event.Modifiers.Alt || event.Modifiers.Super {
		return KeyChord{}, false
	}
	return KeyChord{Key: event.Key, Shift: event.Modifiers.Shift}, true
}

func coordinateLetterFromKey(key string) (string, bool) {
	if len(key) != 1 {
		return "", false
	}
	r := []rune(key)[0]
	if !unicode.IsLetter(r) {
		return "", false
	}
	upper := strings.ToUpper(string(r))
	if len(upper) != 1 || upper[0] < 'A' || upper[0] > 'Z' {
		return "", false
	}
	return upper, true
}
