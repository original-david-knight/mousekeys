package main

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/rajveermalviya/go-wayland/wayland/client"
)

func TestKeyboardInputTranslatorLettersCommandsAndRepeats(t *testing.T) {
	translator := newTestKeyboardInputTranslator(t)

	events := []KeyboardEvent{
		{Kind: KeyboardEventKeymap, Keymap: &KeyboardKeymapFD{Data: []byte("keymap"), Size: 6}},
		{Kind: KeyboardEventEnter},
		{Kind: KeyboardEventKey, Key: "m", State: KeyPressed},
		{Kind: KeyboardEventKey, Key: "m", State: KeyReleased},
		{Kind: KeyboardEventKey, Key: "M", State: KeyPressed, Modifiers: ModifierState{Shift: true}},
		{Kind: KeyboardEventKey, Key: "M", State: KeyReleased, Modifiers: ModifierState{Shift: true}},
		{Kind: KeyboardEventModifiers, Modifiers: ModifierState{}},
		{Kind: KeyboardEventKey, Key: "BackSpace", State: KeyPressed},
		{Kind: KeyboardEventKey, Key: "BackSpace", State: KeyReleased},
	}

	var tokens []KeyboardInputToken
	for _, event := range events {
		token, ok, err := translator.Apply(event)
		if err != nil {
			t.Fatalf("Apply(%+v) returned error: %v", event, err)
		}
		if ok {
			tokens = append(tokens, token)
		}
	}

	if len(tokens) != 3 {
		t.Fatalf("tokens = %+v, want 3", tokens)
	}
	if tokens[0].Kind != KeyboardTokenLetter || tokens[0].Letter != "M" {
		t.Fatalf("lowercase letter token = %+v, want M", tokens[0])
	}
	if tokens[1].Kind != KeyboardTokenLetter || tokens[1].Letter != "M" {
		t.Fatalf("shifted letter token = %+v, want M", tokens[1])
	}
	if tokens[2].Command != KeyboardCommandBackspace {
		t.Fatalf("command token = %+v, want backspace", tokens[2])
	}

	translator = newTestKeyboardInputTranslator(t)
	first, ok, err := translator.Apply(KeyboardEvent{Kind: KeyboardEventKey, Key: "space", State: KeyPressed})
	if err != nil || !ok || first.Command != KeyboardCommandLeftClick {
		t.Fatalf("first space token = %+v, %v, %v; want left click", first, ok, err)
	}
	repeated, ok, err := translator.Apply(KeyboardEvent{Kind: KeyboardEventKey, Key: "space", State: KeyPressed})
	if err != nil {
		t.Fatalf("repeated space returned error: %v", err)
	}
	if ok {
		t.Fatalf("repeated space emitted token %+v", repeated)
	}
	release, ok, err := translator.Apply(KeyboardEvent{Kind: KeyboardEventKey, Key: "space", State: KeyReleased})
	if err != nil {
		t.Fatalf("space release returned error: %v", err)
	}
	if ok {
		t.Fatalf("space release emitted token %+v", release)
	}
}

func TestKeyboardInputTranslatorShiftSpaceDoesNotAdvanceDoubleClick(t *testing.T) {
	translator := newTestKeyboardInputTranslator(t)

	token, ok, err := translator.Apply(KeyboardEvent{Kind: KeyboardEventKey, Key: "space", State: KeyPressed, Modifiers: ModifierState{Shift: true}})
	if err != nil || !ok || token.Command != KeyboardCommandRightClick {
		t.Fatalf("Shift-space token = %+v, %v, %v; want right click", token, ok, err)
	}
	if translator.LastSequenceMatch() != KeySequenceNoMatch {
		t.Fatalf("Shift-space advanced double-click sequence: %s", translator.LastSequenceMatch())
	}

	_, _, err = translator.Apply(KeyboardEvent{Kind: KeyboardEventKey, Key: "space", State: KeyReleased, Modifiers: ModifierState{Shift: true}})
	if err != nil {
		t.Fatalf("Shift-space release returned error: %v", err)
	}
	_, _, err = translator.Apply(KeyboardEvent{Kind: KeyboardEventModifiers, Modifiers: ModifierState{}})
	if err != nil {
		t.Fatalf("modifier reset returned error: %v", err)
	}
	first, ok, err := translator.Apply(KeyboardEvent{Kind: KeyboardEventKey, Key: "space", State: KeyPressed})
	if err != nil || !ok || first.Command != KeyboardCommandLeftClick {
		t.Fatalf("first unshifted space token = %+v, %v, %v; want left click", first, ok, err)
	}
	if translator.LastSequenceMatch() != KeySequencePartial {
		t.Fatalf("first unshifted space sequence = %s, want partial", translator.LastSequenceMatch())
	}
	_, _, err = translator.Apply(KeyboardEvent{Kind: KeyboardEventKey, Key: "space", State: KeyReleased})
	if err != nil {
		t.Fatalf("space release returned error: %v", err)
	}
	second, ok, err := translator.Apply(KeyboardEvent{Kind: KeyboardEventKey, Key: "space", State: KeyPressed})
	if err != nil || !ok || second.Command != KeyboardCommandDoubleClick {
		t.Fatalf("second unshifted space token = %+v, %v, %v; want double click", second, ok, err)
	}
}

func TestKeyboardInputTranslatorKeymapDoesNotCancelDoubleClickSequence(t *testing.T) {
	translator := newTestKeyboardInputTranslator(t)

	first, ok, err := translator.Apply(KeyboardEvent{Kind: KeyboardEventKey, Key: "space", State: KeyPressed})
	if err != nil || !ok || first.Command != KeyboardCommandLeftClick {
		t.Fatalf("first space token = %+v, %v, %v; want left click", first, ok, err)
	}
	_, _, err = translator.Apply(KeyboardEvent{Kind: KeyboardEventKey, Key: "space", State: KeyReleased})
	if err != nil {
		t.Fatalf("space release returned error: %v", err)
	}
	_, ok, err = translator.Apply(KeyboardEvent{Kind: KeyboardEventKeymap, Keymap: &KeyboardKeymapFD{Data: []byte("keymap"), Size: 6}})
	if err != nil || ok {
		t.Fatalf("keymap Apply = ok %v err %v, want no token and no error", ok, err)
	}
	second, ok, err := translator.Apply(KeyboardEvent{Kind: KeyboardEventKey, Key: "space", State: KeyPressed})
	if err != nil || !ok || second.Command != KeyboardCommandDoubleClick {
		t.Fatalf("second space token after keymap = %+v, %v, %v; want double click", second, ok, err)
	}
}

func TestKeyboardInputTranslatorRejectsCtrlAltSuperChords(t *testing.T) {
	for _, event := range []KeyboardEvent{
		{Kind: KeyboardEventKey, Key: "space", State: KeyPressed, Modifiers: ModifierState{Ctrl: true}},
		{Kind: KeyboardEventKey, Key: "m", State: KeyPressed, Modifiers: ModifierState{Super: true}},
		{Kind: KeyboardEventKey, Key: "space", State: KeyPressed, Modifiers: ModifierState{Alt: true}},
	} {
		t.Run(event.Key, func(t *testing.T) {
			translator := newTestKeyboardInputTranslator(t)
			token, ok, err := translator.Apply(event)
			if err != nil {
				t.Fatalf("Apply returned error: %v", err)
			}
			if ok {
				t.Fatalf("modified chord emitted token %+v", token)
			}
			if translator.LastSequenceMatch() != KeySequenceNoMatch {
				t.Fatalf("modified chord advanced double-click sequence: %s", translator.LastSequenceMatch())
			}
		})
	}
}

func TestKeyboardInputTranslatorSuppressesCompositorRepeatForCommandsAndLetters(t *testing.T) {
	for _, event := range []KeyboardEvent{
		{Kind: KeyboardEventKey, Key: "Escape", State: KeyPressed},
		{Kind: KeyboardEventKey, Key: "BackSpace", State: KeyPressed},
		{Kind: KeyboardEventKey, Key: "space", State: KeyPressed},
		{Kind: KeyboardEventKey, Key: "M", State: KeyPressed, Modifiers: ModifierState{Shift: true}},
	} {
		t.Run(event.Key, func(t *testing.T) {
			translator := newTestKeyboardInputTranslator(t)
			if _, ok, err := translator.Apply(event); err != nil || !ok {
				t.Fatalf("initial Apply(%+v) = ok %v err %v, want token", event, ok, err)
			}
			repeated, ok, err := translator.Apply(event)
			if err != nil {
				t.Fatalf("repeated Apply(%+v) returned error: %v", event, err)
			}
			if ok {
				t.Fatalf("repeated Apply(%+v) emitted token %+v", event, repeated)
			}
			if !translator.LastEvent().Repeated {
				t.Fatalf("translator did not mark repeated event for %+v", event)
			}
		})
	}
}

func TestKeyboardSessionStateResetPathsClearPressedAndHeldRepeat(t *testing.T) {
	resetEvents := []KeyboardEvent{
		{Kind: KeyboardEventLeave},
		{Kind: KeyboardEventDestroy},
		{Kind: KeyboardEventKeymap, Keymap: &KeyboardKeymapFD{Data: []byte("keymap"), Size: 6}},
	}
	for _, resetEvent := range resetEvents {
		t.Run(string(resetEvent.Kind), func(t *testing.T) {
			var state KeyboardSessionState
			if _, err := state.ApplyEvent(KeyboardEvent{Kind: KeyboardEventKey, Key: "L", State: KeyPressed}); err != nil {
				t.Fatalf("initial key press returned error: %v", err)
			}
			state.StartHeldDirectionRepeat("right")
			if _, err := state.ApplyEvent(resetEvent); err != nil {
				t.Fatalf("reset event returned error: %v", err)
			}
			if len(state.Pressed) != 0 || state.HeldRepeatActive || state.HeldDirection != "" {
				t.Fatalf("reset event left pressed/repeat state behind: %+v", state)
			}
		})
	}
}

func TestRealWaylandKeyboardEventHubSubscriptionsReplayFatalAndClose(t *testing.T) {
	backend := &realWaylandOverlayBackend{trace: NewTraceRecorder(nil, nil)}
	hub := &realWaylandKeyboardEventHub{
		backend:     backend,
		subscribers: make(map[*realWaylandKeyboardSubscription]struct{}),
	}
	hub.Attach(&client.Keyboard{})

	first := hub.Subscribe()
	keymap := &KeyboardKeymapFD{Data: []byte("keymap"), Size: 6}
	hub.enqueue(KeyboardEvent{Kind: KeyboardEventKeymap, Keymap: keymap})
	got, err := first.NextKeyboardEvent(context.Background())
	if err != nil {
		t.Fatalf("first subscription keymap returned error: %v", err)
	}
	if got.Kind != KeyboardEventKeymap || got.Keymap != keymap {
		t.Fatalf("first subscription keymap = %+v, want replayed keymap pointer", got)
	}

	second := hub.Subscribe()
	got, err = second.NextKeyboardEvent(context.Background())
	if err != nil {
		t.Fatalf("second subscription keymap replay returned error: %v", err)
	}
	if got.Kind != KeyboardEventKeymap || got.Keymap != keymap {
		t.Fatalf("second subscription replay = %+v, want last keymap", got)
	}

	hub.enqueue(KeyboardEvent{Kind: KeyboardEventEnter})
	for name, subscription := range map[string]*realWaylandKeyboardSubscription{"first": first, "second": second} {
		got, err := subscription.NextKeyboardEvent(context.Background())
		if err != nil {
			t.Fatalf("%s subscription enter returned error: %v", name, err)
		}
		if got.Kind != KeyboardEventEnter {
			t.Fatalf("%s subscription got %s, want enter", name, got.Kind)
		}
	}

	hub.fatal(assertiveFatalError("keyboard hub fatal"))
	_, err = first.NextKeyboardEvent(context.Background())
	if err == nil || err.Error() != "keyboard hub fatal" {
		t.Fatalf("subscription fatal error = %v, want keyboard hub fatal", err)
	}

	closeHub := &realWaylandKeyboardEventHub{
		backend:     backend,
		subscribers: make(map[*realWaylandKeyboardSubscription]struct{}),
	}
	subscription := closeHub.Subscribe()
	closeHub.Close()
	_, err = subscription.NextKeyboardEvent(context.Background())
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("subscription after close error = %v, want io.ErrClosedPipe", err)
	}
	if len(closeHub.subscribers) != 0 {
		t.Fatalf("closed hub retained subscribers: %d", len(closeHub.subscribers))
	}
}

func TestReadWaylandKeymapFDIgnoresCurrentOffset(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "keymap")
	if err != nil {
		t.Fatalf("CreateTemp returned error: %v", err)
	}
	defer file.Close()
	if _, err := file.WriteString("keymap-data"); err != nil {
		t.Fatalf("WriteString returned error: %v", err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		t.Fatalf("seek start returned error: %v", err)
	}
	start, err := readWaylandKeymapFD(int(file.Fd()), int64(len("keymap-data")))
	if err != nil || string(start) != "keymap-data" {
		t.Fatalf("start-offset read = %q, %v; want keymap-data", string(start), err)
	}
	if _, err := file.Seek(0, 2); err != nil {
		t.Fatalf("seek EOF returned error: %v", err)
	}
	eofProne, err := readWaylandKeymapFD(int(file.Fd()), int64(len("keymap-data")))
	if err != nil || string(eofProne) != "keymap-data" {
		t.Fatalf("EOF-offset read = %q, %v; want keymap-data", string(eofProne), err)
	}
}

func TestLayerShellOverlayDriverShowHideShowReusesFakeKeyboardLifecycle(t *testing.T) {
	driver, wayland, _ := newTestLayerShellOverlayDriver(t, Monitor{
		Name:          "DP-1",
		LogicalWidth:  10,
		LogicalHeight: 6,
		Scale:         1,
	})
	controller := newDaemonController(driver, statusOutput{})

	for i := 0; i < 2; i++ {
		wayland.keyboard.Enqueue(
			KeyboardEvent{Kind: KeyboardEventKeymap, Keymap: &KeyboardKeymapFD{Data: []byte("keymap"), Size: 6}},
			KeyboardEvent{Kind: KeyboardEventEnter},
			KeyboardEvent{Kind: KeyboardEventKey, Key: "space", State: KeyPressed, Modifiers: ModifierState{Shift: true}},
			KeyboardEvent{Kind: KeyboardEventKey, Key: "space", State: KeyReleased, Modifiers: ModifierState{Shift: true}},
			KeyboardEvent{Kind: KeyboardEventLeave},
		)
		response := controller.Dispatch(context.Background(), ipcRequest{Command: "show"})
		if !response.OK || !response.Active {
			t.Fatalf("show[%d] response = %+v, want active", i, response)
		}
		waitForCondition(t, func() bool { return wayland.keyboard.PendingEvents() == 0 })
		response = controller.Dispatch(context.Background(), ipcRequest{Command: "hide"})
		if !response.OK || response.Active {
			t.Fatalf("hide[%d] response = %+v, want inactive", i, response)
		}
	}
	if got := wayland.keyboard.ShowCount(); got != 2 {
		t.Fatalf("fake keyboard show count = %d, want 2", got)
	}
}

func newTestKeyboardInputTranslator(t *testing.T) *KeyboardInputTranslator {
	t.Helper()
	translator, err := NewKeyboardInputTranslator(DefaultConfig())
	if err != nil {
		t.Fatalf("NewKeyboardInputTranslator returned error: %v", err)
	}
	return translator
}
