package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestKeyboardInputMapperLettersCommandsReleasesAndRepeats(t *testing.T) {
	config := DefaultConfig()
	config.Keybinds.LeftClick = mustParseKeySequence("space")
	config.Keybinds.RightClick = mustParseKeySequence("Return")
	mapper, err := NewKeyboardInputMapper(config)
	if err != nil {
		t.Fatalf("new keyboard input mapper: %v", err)
	}

	tests := []struct {
		name      string
		event     KeyboardEvent
		wantKind  KeyboardTokenKind
		wantLet   byte
		wantSym   KeySym
		wantCmd   KeyboardCommand
		wantToken bool
	}{
		{
			name:      "lowercase letter",
			event:     KeyboardEvent{Key: "a", Pressed: true},
			wantKind:  KeyboardTokenLetter,
			wantLet:   'A',
			wantSym:   "a",
			wantToken: true,
		},
		{
			name:      "shifted uppercase letter",
			event:     KeyboardEvent{Key: "A", Pressed: true},
			wantKind:  KeyboardTokenLetter,
			wantLet:   'A',
			wantSym:   "A",
			wantToken: true,
		},
		{
			name:      "configured command uses keysym name",
			event:     KeyboardEvent{Key: "Return", Pressed: true},
			wantKind:  KeyboardTokenCommand,
			wantSym:   "Return",
			wantCmd:   KeyboardCommandRightClick,
			wantToken: true,
		},
		{
			name:      "keypad enter satisfies Return binding",
			event:     KeyboardEvent{Key: "KP_Enter", Pressed: true},
			wantKind:  KeyboardTokenCommand,
			wantSym:   "KP_Enter",
			wantCmd:   KeyboardCommandRightClick,
			wantToken: true,
		},
		{
			name:      "lowercase xkbcommon space name",
			event:     KeyboardEvent{Key: "space", Pressed: true},
			wantKind:  KeyboardTokenCommand,
			wantSym:   "space",
			wantCmd:   KeyboardCommandLeftClick,
			wantToken: true,
		},
		{
			name:      "release ignored",
			event:     KeyboardEvent{Key: "Return", Pressed: false},
			wantToken: false,
		},
		{
			name:      "repeat ignored",
			event:     KeyboardEvent{Key: "Return", Pressed: true, Repeat: true},
			wantToken: false,
		},
		{
			name:      "unbound non-letter ignored",
			event:     KeyboardEvent{Key: "Delete", Pressed: true},
			wantToken: false,
		},
		{
			name:      "leave ignored",
			event:     KeyboardEvent{Kind: KeyboardEventLeave},
			wantToken: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token, ok := mapper.Translate(tt.event)
			if ok != tt.wantToken {
				t.Fatalf("token emitted = %v, want %v: %+v", ok, tt.wantToken, token)
			}
			if !ok {
				return
			}
			if token.Kind != tt.wantKind || token.Letter != tt.wantLet || token.KeySym != tt.wantSym {
				t.Fatalf("token = %+v, want kind=%s letter=%q keysym=%q", token, tt.wantKind, tt.wantLet, tt.wantSym)
			}
			if tt.wantCmd != "" && !tokenHasCommand(token, tt.wantCmd) {
				t.Fatalf("token commands = %+v, want %s", token.Commands, tt.wantCmd)
			}
		})
	}
}

func TestKeyboardInputMapperRejectsInvalidConfigKeysyms(t *testing.T) {
	config := DefaultConfig()
	config.Keybinds.LeftClick = KeySequence{{KeySym: KeySym("NotAKeysym")}}

	_, err := NewKeyboardInputMapper(config)
	if err == nil {
		t.Fatalf("new keyboard input mapper succeeded with invalid keysym")
	}
	if !strings.Contains(err.Error(), "invalid xkbcommon keysym name") {
		t.Fatalf("error = %q, want invalid keysym", err.Error())
	}
}

func TestKeyboardInputMapperDoesNotEmitDoubleClickSequenceCommand(t *testing.T) {
	mapper, err := NewKeyboardInputMapper(DefaultConfig())
	if err != nil {
		t.Fatalf("new keyboard input mapper: %v", err)
	}

	token, ok := mapper.Translate(KeyboardEvent{Key: "space", Pressed: true})
	if !ok {
		t.Fatalf("space did not produce a token")
	}
	if tokenHasCommand(token, KeyboardCommandDoubleClick) {
		t.Fatalf("space token includes double_click command even though double-click is a sequence: %+v", token.Commands)
	}
	if !tokenHasCommand(token, KeyboardCommandLeftClick) {
		t.Fatalf("space token lacks left_click command: %+v", token.Commands)
	}
}

func TestKeyboardInputMapperEmitsArrowKeysForSubgridNavigation(t *testing.T) {
	mapper, err := NewKeyboardInputMapper(DefaultConfig())
	if err != nil {
		t.Fatalf("new keyboard input mapper: %v", err)
	}

	for _, key := range []string{"Left", "Down", "Up", "Right", "KP_Left", "KP_Down", "KP_Up", "KP_Right"} {
		token, ok := mapper.Translate(KeyboardEvent{Key: key, Pressed: true})
		if !ok {
			t.Fatalf("%s did not produce a token", key)
		}
		if token.Kind != KeyboardTokenCommand || token.KeySym != KeySym(key) || len(token.Commands) != 0 {
			t.Fatalf("%s token = %+v, want command token without configured commands", key, token)
		}
	}
}

func TestKeyboardInputMapperEmitsReleaseTokensForSubgridNavigation(t *testing.T) {
	mapper, err := NewKeyboardInputMapper(DefaultConfig())
	if err != nil {
		t.Fatalf("new keyboard input mapper: %v", err)
	}

	for _, tt := range []struct {
		key      string
		wantKind KeyboardTokenKind
		wantLet  byte
		wantSym  KeySym
	}{
		{key: "h", wantKind: KeyboardTokenLetter, wantLet: 'H', wantSym: "h"},
		{key: "L", wantKind: KeyboardTokenLetter, wantLet: 'L', wantSym: "L"},
		{key: "Left", wantKind: KeyboardTokenCommand, wantSym: "Left"},
		{key: "KP_Down", wantKind: KeyboardTokenCommand, wantSym: "KP_Down"},
	} {
		t.Run(tt.key, func(t *testing.T) {
			token, ok := mapper.Translate(KeyboardEvent{Key: tt.key, Keycode: 42, Pressed: false})
			if !ok {
				t.Fatalf("%s release did not produce a token", tt.key)
			}
			if !token.Released || token.Kind != tt.wantKind || token.Letter != tt.wantLet || token.KeySym != tt.wantSym || token.Keycode != 42 || len(token.Commands) != 0 {
				t.Fatalf("%s release token = %+v, want released %s letter=%q keysym=%q keycode=42 without commands", tt.key, token, tt.wantKind, tt.wantLet, tt.wantSym)
			}
		})
	}

	for _, key := range []string{"a", "Return"} {
		if token, ok := mapper.Translate(KeyboardEvent{Key: key, Pressed: false}); ok {
			t.Fatalf("%s release token = %+v, want ignored", key, token)
		}
	}
}

func TestKeyboardInputMapperUsesDefaultSpaceClickBindings(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mapper, err := NewKeyboardInputMapper(DefaultConfig())
	if err != nil {
		t.Fatalf("new keyboard input mapper: %v", err)
	}
	keyboard := newFakeKeyboardEventSource(4)
	tokens, err := mapper.Tokens(ctx, keyboard)
	if err != nil {
		t.Fatalf("keyboard tokens: %v", err)
	}

	keyboard.Send(KeyboardEvent{Key: "space", Pressed: true})
	first := receiveKeyboardToken(t, tokens)
	if first.KeySym != "space" || !tokenHasCommand(first, KeyboardCommandLeftClick) || tokenHasCommand(first, KeyboardCommandRightClick) || tokenHasCommand(first, KeyboardCommandDoubleClick) {
		t.Fatalf("first space token = %+v, want left click only", first)
	}

	keyboard.Send(KeyboardEvent{Key: "space", Pressed: true})
	second := receiveKeyboardToken(t, tokens)
	if second.KeySym != "space" || !tokenHasCommand(second, KeyboardCommandLeftClick) || tokenHasCommand(second, KeyboardCommandRightClick) || !tokenHasCommand(second, KeyboardCommandDoubleClick) {
		t.Fatalf("second space token = %+v, want left click and double click", second)
	}

	keyboard.Send(KeyboardEvent{Key: "space", Pressed: true, Modifiers: KeyboardModifiers{Shift: true}})
	shiftSpace := receiveKeyboardToken(t, tokens)
	if shiftSpace.KeySym != "space" || !shiftSpace.Modifiers.Shift || !tokenHasCommand(shiftSpace, KeyboardCommandRightClick) || tokenHasCommand(shiftSpace, KeyboardCommandLeftClick) || tokenHasCommand(shiftSpace, KeyboardCommandDoubleClick) {
		t.Fatalf("Shift-space token = %+v, want right click only", shiftSpace)
	}
}

func TestKeyboardInputMapperTreatsKeypadEnterAsReturnForConfiguredClickBindings(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	config := DefaultConfig()
	config.Keybinds.LeftClick = mustParseKeySequence("Return")
	config.Keybinds.DoubleClick = mustParseKeySequence("Return Return")
	mapper, err := NewKeyboardInputMapper(config)
	if err != nil {
		t.Fatalf("new keyboard input mapper: %v", err)
	}
	keyboard := newFakeKeyboardEventSource(4)
	tokens, err := mapper.Tokens(ctx, keyboard)
	if err != nil {
		t.Fatalf("keyboard tokens: %v", err)
	}

	keyboard.Send(KeyboardEvent{Key: "KP_Enter", Pressed: true})
	first := receiveKeyboardToken(t, tokens)
	if first.KeySym != "KP_Enter" || !tokenHasCommand(first, KeyboardCommandLeftClick) || tokenHasCommand(first, KeyboardCommandDoubleClick) {
		t.Fatalf("first KP_Enter token = %+v, want left click only", first)
	}

	keyboard.Send(KeyboardEvent{Key: "KP_Enter", Pressed: true})
	second := receiveKeyboardToken(t, tokens)
	if second.KeySym != "KP_Enter" || !tokenHasCommand(second, KeyboardCommandLeftClick) || !tokenHasCommand(second, KeyboardCommandDoubleClick) {
		t.Fatalf("second KP_Enter token = %+v, want left click and double click", second)
	}
}

func TestKeyboardInputMapperPrefersExactKeypadEnterBinding(t *testing.T) {
	config := DefaultConfig()
	config.Keybinds.LeftClick = mustParseKeySequence("Return")
	config.Keybinds.DoubleClick = mustParseKeySequence("Return Return")
	config.Keybinds.RightClick = mustParseKeySequence("KP_Enter")
	mapper, err := NewKeyboardInputMapper(config)
	if err != nil {
		t.Fatalf("new keyboard input mapper: %v", err)
	}

	token, ok := mapper.Translate(KeyboardEvent{Key: "KP_Enter", Pressed: true})
	if !ok {
		t.Fatalf("KP_Enter did not produce a token")
	}
	if !tokenHasCommand(token, KeyboardCommandRightClick) || tokenHasCommand(token, KeyboardCommandLeftClick) {
		t.Fatalf("KP_Enter commands = %+v, want exact right_click only", token.Commands)
	}
}

func TestKeyboardInputMapperHonorsConfiguredClickKeySequences(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	config := DefaultConfig()
	config.Keybinds.LeftClick = mustParseKeySequence("F1")
	config.Keybinds.RightClick = mustParseKeySequence("F2")
	config.Keybinds.DoubleClick = mustParseKeySequence("F1 F1")
	config.Keybinds.CommitPartial = mustParseKeySequence("F3")
	config.Keybinds.Exit = mustParseKeySequence("F4")
	config.Keybinds.Backspace = mustParseKeySequence("F5")
	mapper, err := NewKeyboardInputMapper(config)
	if err != nil {
		t.Fatalf("new keyboard input mapper: %v", err)
	}
	keyboard := newFakeKeyboardEventSource(8)
	tokens, err := mapper.Tokens(ctx, keyboard)
	if err != nil {
		t.Fatalf("keyboard tokens: %v", err)
	}

	keyboard.Send(KeyboardEvent{Key: "F1", Pressed: true})
	first := receiveKeyboardToken(t, tokens)
	if !tokenHasCommand(first, KeyboardCommandLeftClick) || tokenHasCommand(first, KeyboardCommandDoubleClick) {
		t.Fatalf("first F1 token commands = %+v, want left click only", first.Commands)
	}

	keyboard.Send(KeyboardEvent{Key: "F1", Pressed: true})
	second := receiveKeyboardToken(t, tokens)
	if !tokenHasCommand(second, KeyboardCommandLeftClick) || !tokenHasCommand(second, KeyboardCommandDoubleClick) {
		t.Fatalf("second F1 token commands = %+v, want left click and double click", second.Commands)
	}

	for _, tt := range []struct {
		key     string
		command KeyboardCommand
	}{
		{key: "F2", command: KeyboardCommandRightClick},
		{key: "F3", command: KeyboardCommandCommitPartial},
		{key: "F4", command: KeyboardCommandExit},
		{key: "F5", command: KeyboardCommandBackspace},
	} {
		keyboard.Send(KeyboardEvent{Key: tt.key, Pressed: true})
		token := receiveKeyboardToken(t, tokens)
		if !tokenHasCommand(token, tt.command) {
			t.Fatalf("%s token commands = %+v, want %s", tt.key, token.Commands, tt.command)
		}
	}
}

func TestKeyboardInputTokensUsesFakeKeyboardSource(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	keyboard := newFakeKeyboardEventSource(8)
	mapper, err := NewKeyboardInputMapper(DefaultConfig())
	if err != nil {
		t.Fatalf("new keyboard input mapper: %v", err)
	}
	tokens, err := mapper.Tokens(ctx, keyboard)
	if err != nil {
		t.Fatalf("keyboard tokens: %v", err)
	}

	keyboard.Send(KeyboardEvent{Key: "b", Pressed: true})
	keyboard.Send(KeyboardEvent{Key: "BackSpace", Pressed: false})
	keyboard.Send(KeyboardEvent{Key: "BackSpace", Pressed: true})

	assertKeyboardToken(t, tokens, KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'B', KeySym: "b"})
	token := receiveKeyboardToken(t, tokens)
	if token.Kind != KeyboardTokenCommand || !tokenHasCommand(token, KeyboardCommandBackspace) {
		t.Fatalf("token = %+v, want backspace command", token)
	}
	assertNoKeyboardToken(t, tokens)
}

func tokenHasCommand(token KeyboardToken, command KeyboardCommand) bool {
	for _, got := range token.Commands {
		if got == command {
			return true
		}
	}
	return false
}

func assertKeyboardToken(t *testing.T, tokens <-chan KeyboardToken, want KeyboardToken) {
	t.Helper()
	got := receiveKeyboardToken(t, tokens)
	if got.Kind != want.Kind || got.Letter != want.Letter || got.KeySym != want.KeySym {
		t.Fatalf("token = %+v, want kind=%s letter=%q keysym=%q", got, want.Kind, want.Letter, want.KeySym)
	}
}

func receiveKeyboardToken(t *testing.T, tokens <-chan KeyboardToken) KeyboardToken {
	t.Helper()
	select {
	case token, ok := <-tokens:
		if !ok {
			t.Fatalf("keyboard token channel closed")
		}
		return token
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for keyboard token")
		return KeyboardToken{}
	}
}

func assertNoKeyboardToken(t *testing.T, tokens <-chan KeyboardToken) {
	t.Helper()
	select {
	case token, ok := <-tokens:
		if ok {
			t.Fatalf("unexpected keyboard token: %+v", token)
		}
	case <-time.After(25 * time.Millisecond):
	}
}
