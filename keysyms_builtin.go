//go:build !cgo || !linux

package main

import "fmt"

var builtinXKBKeysymNames = func() map[string]struct{} {
	names := map[string]struct{}{
		"BackSpace": {}, "Tab": {}, "Return": {}, "Escape": {}, "Delete": {},
		"Insert": {}, "Home": {}, "End": {}, "Page_Up": {}, "Page_Down": {},
		"Left": {}, "Up": {}, "Right": {}, "Down": {}, "ISO_Left_Tab": {},
		"Shift_L": {}, "Shift_R": {}, "Control_L": {}, "Control_R": {},
		"Alt_L": {}, "Alt_R": {}, "Meta_L": {}, "Meta_R": {},
		"Super_L": {}, "Super_R": {}, "Menu": {},
		"space": {}, "exclam": {}, "quotedbl": {}, "numbersign": {},
		"dollar": {}, "percent": {}, "ampersand": {}, "apostrophe": {},
		"parenleft": {}, "parenright": {}, "asterisk": {}, "plus": {},
		"comma": {}, "minus": {}, "period": {}, "slash": {}, "colon": {},
		"semicolon": {}, "less": {}, "equal": {}, "greater": {}, "question": {},
		"at": {}, "bracketleft": {}, "backslash": {}, "bracketright": {},
		"asciicircum": {}, "underscore": {}, "grave": {}, "braceleft": {},
		"bar": {}, "braceright": {}, "asciitilde": {},
	}
	for ch := 'A'; ch <= 'Z'; ch++ {
		names[string(ch)] = struct{}{}
	}
	for ch := 'a'; ch <= 'z'; ch++ {
		names[string(ch)] = struct{}{}
	}
	for ch := '0'; ch <= '9'; ch++ {
		names[string(ch)] = struct{}{}
	}
	for i := 1; i <= 35; i++ {
		names[fmt.Sprintf("F%d", i)] = struct{}{}
	}
	for i := 0; i <= 9; i++ {
		names[fmt.Sprintf("KP_%d", i)] = struct{}{}
	}
	for _, name := range []string{"KP_Space", "KP_Tab", "KP_Enter", "KP_F1", "KP_F2", "KP_F3", "KP_F4", "KP_Home", "KP_Left", "KP_Up", "KP_Right", "KP_Down", "KP_Page_Up", "KP_Page_Down", "KP_End", "KP_Begin", "KP_Insert", "KP_Delete", "KP_Equal", "KP_Multiply", "KP_Add", "KP_Separator", "KP_Subtract", "KP_Decimal", "KP_Divide"} {
		names[name] = struct{}{}
	}
	return names
}()

func validXKBKeysymName(name string) bool {
	_, ok := builtinXKBKeysymNames[name]
	return ok
}
