//go:build cgo && linux

package main

import (
	"context"
	"testing"
	"time"
)

const (
	evdevKeyEscape    uint32 = 1
	evdevKeyBackspace uint32 = 14
	evdevKeyTab       uint32 = 15
	evdevKeyReturn    uint32 = 28
	evdevKeyA         uint32 = 30
	evdevKeyLeftShift uint32 = 42
	evdevKeySpace     uint32 = 57
	evdevKeyKPEnter   uint32 = 96
)

func TestXKBKeyboardSourceTranslatesRawKeymapEventsToTokens(t *testing.T) {
	raw := newFakeRawKeyboardEventSource(32)
	source := NewXKBKeyboardEventSource(raw)
	mapper, err := NewKeyboardInputMapper(DefaultConfig())
	if err != nil {
		t.Fatalf("new keyboard input mapper: %v", err)
	}
	tokens, err := mapper.Tokens(context.Background(), source)
	if err != nil {
		t.Fatalf("keyboard tokens: %v", err)
	}

	now := time.Date(2026, 5, 1, 13, 0, 0, 0, time.UTC)
	raw.SendKeymap(defaultUSKeymapForTest(t))
	raw.SendKey(evdevKeyA, true, now)
	assertKeyboardToken(t, tokens, KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'A', KeySym: "a"})
	raw.SendKey(evdevKeyA, false, now)
	assertNoKeyboardToken(t, tokens)

	raw.SendKey(evdevKeyLeftShift, true, now)
	assertNoKeyboardToken(t, tokens)
	raw.SendKey(evdevKeyA, true, now)
	assertKeyboardToken(t, tokens, KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'A', KeySym: "A"})
	raw.SendKey(evdevKeyA, false, now)
	raw.SendKey(evdevKeyLeftShift, false, now)
	assertNoKeyboardToken(t, tokens)

	raw.SendKey(evdevKeyReturn, true, now)
	token := receiveKeyboardToken(t, tokens)
	if token.Kind != KeyboardTokenCommand || token.KeySym != "Return" || !tokenHasCommand(token, KeyboardCommandLeftClick) {
		t.Fatalf("token = %+v, want Return left_click command", token)
	}

	raw.SendKey(evdevKeySpace, true, now)
	token = receiveKeyboardToken(t, tokens)
	if token.Kind != KeyboardTokenCommand || token.KeySym != "space" || !tokenHasCommand(token, KeyboardCommandRightClick) {
		t.Fatalf("token = %+v, want space right_click command", token)
	}

	raw.SendKey(evdevKeyTab, true, now)
	token = receiveKeyboardToken(t, tokens)
	if token.Kind != KeyboardTokenCommand || token.KeySym != "Tab" || !tokenHasCommand(token, KeyboardCommandCommitPartial) {
		t.Fatalf("token = %+v, want Tab commit_partial command", token)
	}

	raw.SendKey(evdevKeyEscape, true, now)
	token = receiveKeyboardToken(t, tokens)
	if token.Kind != KeyboardTokenCommand || token.KeySym != "Escape" || !tokenHasCommand(token, KeyboardCommandExit) {
		t.Fatalf("token = %+v, want Escape exit command", token)
	}

	raw.SendKey(evdevKeyBackspace, true, now)
	token = receiveKeyboardToken(t, tokens)
	if token.Kind != KeyboardTokenCommand || token.KeySym != "BackSpace" || !tokenHasCommand(token, KeyboardCommandBackspace) {
		t.Fatalf("token = %+v, want BackSpace backspace command", token)
	}
}

func TestXKBKeyboardSourceIgnoresReleasesAndRepeatsAndResetsOnLeaveDestroy(t *testing.T) {
	raw := newFakeRawKeyboardEventSource(32)
	source := NewXKBKeyboardEventSource(raw)
	mapper, err := NewKeyboardInputMapper(DefaultConfig())
	if err != nil {
		t.Fatalf("new keyboard input mapper: %v", err)
	}
	tokens, err := mapper.Tokens(context.Background(), source)
	if err != nil {
		t.Fatalf("keyboard tokens: %v", err)
	}

	now := time.Date(2026, 5, 1, 13, 30, 0, 0, time.UTC)
	raw.SendKeymap(defaultUSKeymapForTest(t))

	raw.SendKey(evdevKeyReturn, true, now)
	requireCommandToken(t, tokens, "Return", KeyboardCommandLeftClick)
	raw.SendKey(evdevKeyReturn, true, now)
	assertNoKeyboardToken(t, tokens)

	raw.SendLeave(now)
	assertNoKeyboardToken(t, tokens)
	raw.SendKey(evdevKeyReturn, true, now)
	requireCommandToken(t, tokens, "Return", KeyboardCommandLeftClick)

	raw.SendDestroy(now)
	assertNoKeyboardToken(t, tokens)
	raw.SendKeymap(defaultUSKeymapForTest(t))
	raw.SendKey(evdevKeyReturn, true, now)
	requireCommandToken(t, tokens, "Return", KeyboardCommandLeftClick)

	raw.SendKey(evdevKeyReturn, false, now)
	assertNoKeyboardToken(t, tokens)
	raw.SendKey(evdevKeyReturn, true, now)
	requireCommandToken(t, tokens, "Return", KeyboardCommandLeftClick)
}

func TestXKBKeyboardSourceMapsKeypadEnterToDefaultClickCommand(t *testing.T) {
	raw := newFakeRawKeyboardEventSource(16)
	source := NewXKBKeyboardEventSource(raw)
	mapper, err := NewKeyboardInputMapper(DefaultConfig())
	if err != nil {
		t.Fatalf("new keyboard input mapper: %v", err)
	}
	tokens, err := mapper.Tokens(context.Background(), source)
	if err != nil {
		t.Fatalf("keyboard tokens: %v", err)
	}

	now := time.Date(2026, 5, 1, 13, 35, 0, 0, time.UTC)
	raw.SendKeymap(defaultUSKeymapForTest(t))
	raw.SendKey(evdevKeyKPEnter, true, now)
	requireCommandToken(t, tokens, "KP_Enter", KeyboardCommandLeftClick)
}

func TestXKBKeyboardSourceEnterSeedsPressedKeys(t *testing.T) {
	raw := newFakeRawKeyboardEventSource(16)
	source := NewXKBKeyboardEventSource(raw)
	mapper, err := NewKeyboardInputMapper(DefaultConfig())
	if err != nil {
		t.Fatalf("new keyboard input mapper: %v", err)
	}
	tokens, err := mapper.Tokens(context.Background(), source)
	if err != nil {
		t.Fatalf("keyboard tokens: %v", err)
	}

	now := time.Date(2026, 5, 1, 13, 40, 0, 0, time.UTC)
	raw.SendKeymap(defaultUSKeymapForTest(t))
	raw.SendEnter(now, evdevKeyReturn)
	assertNoKeyboardToken(t, tokens)
	raw.SendKey(evdevKeyReturn, true, now)
	assertNoKeyboardToken(t, tokens)
	raw.SendKey(evdevKeyReturn, false, now)
	assertNoKeyboardToken(t, tokens)
	raw.SendKey(evdevKeyReturn, true, now)
	requireCommandToken(t, tokens, "Return", KeyboardCommandLeftClick)
}

func TestXKBKeyboardSourceLeaveClearsPressedModifiers(t *testing.T) {
	raw := newFakeRawKeyboardEventSource(16)
	source := NewXKBKeyboardEventSource(raw)
	mapper, err := NewKeyboardInputMapper(DefaultConfig())
	if err != nil {
		t.Fatalf("new keyboard input mapper: %v", err)
	}
	tokens, err := mapper.Tokens(context.Background(), source)
	if err != nil {
		t.Fatalf("keyboard tokens: %v", err)
	}

	now := time.Date(2026, 5, 1, 13, 45, 0, 0, time.UTC)
	raw.SendKeymap(defaultUSKeymapForTest(t))
	raw.SendKey(evdevKeyLeftShift, true, now)
	assertNoKeyboardToken(t, tokens)
	raw.SendLeave(now)
	assertNoKeyboardToken(t, tokens)
	raw.SendKey(evdevKeyA, true, now)
	assertKeyboardToken(t, tokens, KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'A', KeySym: "a"})
}

func TestXKBKeyboardSourceModifiersAndSecondKeymapReset(t *testing.T) {
	raw := newFakeRawKeyboardEventSource(24)
	source := NewXKBKeyboardEventSource(raw)
	mapper, err := NewKeyboardInputMapper(DefaultConfig())
	if err != nil {
		t.Fatalf("new keyboard input mapper: %v", err)
	}
	tokens, err := mapper.Tokens(context.Background(), source)
	if err != nil {
		t.Fatalf("keyboard tokens: %v", err)
	}

	now := time.Date(2026, 5, 1, 13, 50, 0, 0, time.UTC)
	raw.SendKeymap(defaultUSKeymapForTest(t))
	raw.SendModifiers(1, 0, 0, 0)
	assertNoKeyboardToken(t, tokens)
	raw.SendKey(evdevKeyA, true, now)
	assertKeyboardToken(t, tokens, KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'A', KeySym: "A"})

	raw.SendKeymap(defaultUSKeymapForTest(t))
	assertNoKeyboardToken(t, tokens)
	raw.SendKey(evdevKeyA, true, now)
	assertKeyboardToken(t, tokens, KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'A', KeySym: "a"})
}

func TestXKBKeyboardSourceMarksDuplicatePressesAsRepeat(t *testing.T) {
	raw := newFakeRawKeyboardEventSource(16)
	events, err := NewXKBKeyboardEventSource(raw).Events(context.Background())
	if err != nil {
		t.Fatalf("keyboard events: %v", err)
	}

	now := time.Date(2026, 5, 1, 14, 0, 0, 0, time.UTC)
	raw.SendKeymap(defaultUSKeymapForTest(t))
	raw.SendKey(evdevKeyReturn, true, now)
	first := receiveKeyboardEvent(t, events)
	if first.Key != "Return" || first.Repeat {
		t.Fatalf("first event = %+v, want non-repeat Return", first)
	}
	raw.SendKey(evdevKeyReturn, true, now)
	repeat := receiveKeyboardEvent(t, events)
	if repeat.Key != "Return" || !repeat.Repeat {
		t.Fatalf("repeat event = %+v, want repeat Return", repeat)
	}
}

func requireCommandToken(t *testing.T, tokens <-chan KeyboardToken, keysym KeySym, command KeyboardCommand) {
	t.Helper()
	token := receiveKeyboardToken(t, tokens)
	if token.Kind != KeyboardTokenCommand || token.KeySym != keysym || !tokenHasCommand(token, command) {
		t.Fatalf("token = %+v, want %s command for %s", token, command, keysym)
	}
}

func receiveKeyboardEvent(t *testing.T, events <-chan KeyboardEvent) KeyboardEvent {
	t.Helper()
	select {
	case event, ok := <-events:
		if !ok {
			t.Fatalf("keyboard event channel closed")
		}
		return event
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for keyboard event")
		return KeyboardEvent{}
	}
}

func defaultUSKeymapForTest(t *testing.T) []byte {
	t.Helper()
	keymap, err := compileXKBKeymapFromNames("pc105", "us")
	if err != nil {
		t.Fatalf("generate default US xkb keymap: %v", err)
	}
	return keymap
}

type fakeRawKeyboardEventSource struct {
	ch chan RawKeyboardEvent
}

func newFakeRawKeyboardEventSource(buffer int) *fakeRawKeyboardEventSource {
	return &fakeRawKeyboardEventSource{ch: make(chan RawKeyboardEvent, buffer)}
}

func (f *fakeRawKeyboardEventSource) RawEvents(context.Context) (<-chan RawKeyboardEvent, error) {
	return f.ch, nil
}

func (f *fakeRawKeyboardEventSource) SendKeymap(keymap []byte) {
	f.ch <- RawKeyboardEvent{Kind: RawKeyboardEventKeymap, KeymapFormat: keyboardKeymapFormatXKBV1, Keymap: keymap}
}

func (f *fakeRawKeyboardEventSource) SendEnter(at time.Time, keys ...uint32) {
	f.ch <- RawKeyboardEvent{Kind: RawKeyboardEventEnter, PressedKeys: append([]uint32(nil), keys...), Time: at}
}

func (f *fakeRawKeyboardEventSource) SendKey(keycode uint32, pressed bool, at time.Time) {
	f.ch <- RawKeyboardEvent{Kind: RawKeyboardEventKey, Keycode: keycode, Pressed: pressed, Time: at}
}

func (f *fakeRawKeyboardEventSource) SendModifiers(depressed, latched, locked, group uint32) {
	f.ch <- RawKeyboardEvent{
		Kind:          RawKeyboardEventModifiers,
		ModsDepressed: depressed,
		ModsLatched:   latched,
		ModsLocked:    locked,
		Group:         group,
	}
}

func (f *fakeRawKeyboardEventSource) SendLeave(at time.Time) {
	f.ch <- RawKeyboardEvent{Kind: RawKeyboardEventLeave, Time: at}
}

func (f *fakeRawKeyboardEventSource) SendDestroy(at time.Time) {
	f.ch <- RawKeyboardEvent{Kind: RawKeyboardEventDestroy, Time: at}
}
