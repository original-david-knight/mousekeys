package main

import "testing"

func TestCoordinateEntryStateEdgeCases(t *testing.T) {
	var state coordinateEntryState
	if state.Backspace() {
		t.Fatal("Backspace on empty input reported a change")
	}
	if state.Input() != "" {
		t.Fatalf("empty Backspace changed input to %q", state.Input())
	}

	changed, selected := state.AddLetter("m", 26)
	if !changed || selected || state.Input() != "M" {
		t.Fatalf("first AddLetter = changed %v selected %v input %q, want M without selection", changed, selected, state.Input())
	}
	changed, selected = state.AddLetter("k", 26)
	if !changed || !selected || state.Input() != "MK" {
		t.Fatalf("second AddLetter = changed %v selected %v input %q, want selected MK", changed, selected, state.Input())
	}
	changed, selected = state.AddLetter("A", 26)
	if changed || selected || state.Input() != "MK" {
		t.Fatalf("third AddLetter = changed %v selected %v input %q, want no-op MK", changed, selected, state.Input())
	}
}

func TestGridLetterIndexRejectsInvalidInputs(t *testing.T) {
	for _, input := range []string{"", "/", "AA", "Escape", "1"} {
		if index, ok := gridLetterIndex(input, 26); ok {
			t.Fatalf("gridLetterIndex(%q) = %d, true; want false", input, index)
		}
	}
	if index, ok := gridLetterIndex("z", 26); !ok || index != 25 {
		t.Fatalf("gridLetterIndex(z) = %d, %v; want 25, true", index, ok)
	}
	if index, ok := gridLetterIndex("Z", 25); ok {
		t.Fatalf("gridLetterIndex(Z, 25) = %d, true; want false", index)
	}
}
