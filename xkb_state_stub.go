//go:build !cgo

package main

import "fmt"

type xkbKeyboardState struct{}

func newXKBKeyboardState() (*xkbKeyboardState, error) {
	return &xkbKeyboardState{}, nil
}

func (s *xkbKeyboardState) Close() {}

func (s *xkbKeyboardState) SetKeymap(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("xkb keymap is empty")
	}
	return fmt.Errorf("xkbcommon support requires cgo")
}

func (s *xkbKeyboardState) UpdateMask(depressed, latched, locked, group uint32) ModifierState {
	return ModifierState{}
}

func (s *xkbKeyboardState) Modifiers() ModifierState {
	return ModifierState{}
}

func (s *xkbKeyboardState) KeyName(rawKey uint32) (string, bool) {
	return "", false
}
