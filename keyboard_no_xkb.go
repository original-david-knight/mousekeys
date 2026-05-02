//go:build !cgo || !linux

package main

import "fmt"

func newKeyboardKeymapState(format uint32, keymap []byte) (keyboardKeymapState, error) {
	if format != keyboardKeymapFormatXKBV1 {
		return nil, fmt.Errorf("unsupported wl_keyboard keymap format %d", format)
	}
	if len(keymap) == 0 {
		return nil, fmt.Errorf("wl_keyboard keymap is empty")
	}
	return nil, fmt.Errorf("wl_keyboard xkb keymap translation requires linux cgo with xkbcommon")
}

func compileXKBKeymapFromNames(model string, layout string) ([]byte, error) {
	return nil, fmt.Errorf("compiling xkb keymaps requires linux cgo with xkbcommon")
}
