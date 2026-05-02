package main

import "testing"

func TestMainCoordinateEntryFSMFirstLetterSelectsColumnAndHUD(t *testing.T) {
	fsm := NewMainCoordinateEntryFSM(26, Monitor{Name: "test", Width: 260, Height: 260})

	result := fsm.HandleToken(KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'M'})
	if !result.Changed || result.Selected != nil {
		t.Fatalf("first letter result = %+v, want changed without selection", result)
	}
	if got, want := fsm.HUD(), "M_"; got != want {
		t.Fatalf("HUD = %q, want %q", got, want)
	}
	col, ok := fsm.SelectedColumn()
	if !ok || col != 12 {
		t.Fatalf("selected column = %d,%v, want M/12", col, ok)
	}
}

func TestMainCoordinateEntryFSMSecondLetterEmitsResolvedSelection(t *testing.T) {
	monitor := Monitor{Name: "test", Width: 260, Height: 520}
	fsm := NewMainCoordinateEntryFSM(26, monitor)
	fsm.HandleToken(KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'M'})

	result := fsm.HandleToken(KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'K'})
	if !result.Changed || result.Selected == nil {
		t.Fatalf("second letter result = %+v, want selected event", result)
	}
	if got, want := fsm.HUD(), "MK"; got != want {
		t.Fatalf("HUD = %q, want %q", got, want)
	}

	wantBounds, err := GridCellBounds(monitor, 26, 12, 10)
	if err != nil {
		t.Fatalf("expected grid bounds: %v", err)
	}
	want := MainCoordinateSelectedEvent{
		Column:       12,
		Row:          10,
		ColumnLetter: 'M',
		RowLetter:    'K',
		Bounds:       wantBounds,
		Center:       wantBounds.Center(),
	}
	if *result.Selected != want {
		t.Fatalf("selected = %+v, want %+v", *result.Selected, want)
	}
}

func TestMainCoordinateEntryFSMBackspaceRemovesLastCharacter(t *testing.T) {
	fsm := NewMainCoordinateEntryFSM(26, Monitor{Name: "test", Width: 260, Height: 260})
	fsm.HandleToken(KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'M'})
	fsm.HandleToken(KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'K'})

	result := fsm.HandleToken(KeyboardToken{Kind: KeyboardTokenCommand, Commands: []KeyboardCommand{KeyboardCommandBackspace}})
	if !result.Changed {
		t.Fatalf("first backspace result = %+v, want changed", result)
	}
	if got, want := fsm.HUD(), "M_"; got != want {
		t.Fatalf("HUD after first backspace = %q, want %q", got, want)
	}
	col, ok := fsm.SelectedColumn()
	if !ok || col != 12 {
		t.Fatalf("selected column after first backspace = %d,%v, want M/12", col, ok)
	}

	result = fsm.HandleToken(KeyboardToken{Kind: KeyboardTokenCommand, Commands: []KeyboardCommand{KeyboardCommandBackspace}})
	if !result.Changed {
		t.Fatalf("second backspace result = %+v, want changed", result)
	}
	if got, want := fsm.HUD(), DefaultMainGridHUD; got != want {
		t.Fatalf("HUD after second backspace = %q, want %q", got, want)
	}
	if col, ok := fsm.SelectedColumn(); ok {
		t.Fatalf("selected column after second backspace = %d,%v, want none", col, ok)
	}

	result = fsm.HandleToken(KeyboardToken{Kind: KeyboardTokenCommand, Commands: []KeyboardCommand{KeyboardCommandBackspace}})
	if result.Changed || result.Selected != nil {
		t.Fatalf("empty backspace result = %+v, want ignored", result)
	}
}

func TestMainCoordinateEntryFSMInvalidKeysAreIgnored(t *testing.T) {
	fsm := NewMainCoordinateEntryFSM(4, Monitor{Name: "test", Width: 40, Height: 40})

	for _, token := range []KeyboardToken{
		{Kind: KeyboardTokenCommand, KeySym: "Delete"},
		{Kind: KeyboardTokenLetter, Letter: 'Z'},
		{Kind: KeyboardTokenKind("unknown"), Letter: 'A'},
		{Kind: KeyboardTokenLetter, Letter: '1'},
	} {
		if result := fsm.HandleToken(token); result.Changed || result.Selected != nil {
			t.Fatalf("invalid token %+v produced result %+v", token, result)
		}
	}
	if got, want := fsm.HUD(), DefaultMainGridHUD; got != want {
		t.Fatalf("HUD after invalid keys = %q, want %q", got, want)
	}
}

func TestMainCoordinateEntryFSMResetClearsInputAndColumn(t *testing.T) {
	fsm := NewMainCoordinateEntryFSM(26, Monitor{Name: "test", Width: 260, Height: 260})
	fsm.HandleToken(KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'M'})

	fsm.Reset()

	if got, want := fsm.HUD(), DefaultMainGridHUD; got != want {
		t.Fatalf("HUD after reset = %q, want %q", got, want)
	}
	if got := fsm.Input(); got != "" {
		t.Fatalf("input after reset = %q, want empty", got)
	}
	if col, ok := fsm.SelectedColumn(); ok {
		t.Fatalf("selected column after reset = %d,%v, want none", col, ok)
	}
}
