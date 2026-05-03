//go:build cgo

package main

/*
#cgo pkg-config: xkbcommon
#include <stdlib.h>
#include <xkbcommon/xkbcommon.h>
*/
import "C"

import (
	"fmt"
	"unsafe"
)

const waylandXKBKeycodeOffset = 8

type xkbKeyboardState struct {
	context *C.struct_xkb_context
	keymap  *C.struct_xkb_keymap
	state   *C.struct_xkb_state
}

func newXKBKeyboardState() (*xkbKeyboardState, error) {
	context := C.xkb_context_new(C.XKB_CONTEXT_NO_FLAGS)
	if context == nil {
		return nil, fmt.Errorf("create xkb context")
	}
	return &xkbKeyboardState{context: context}, nil
}

func (s *xkbKeyboardState) Close() {
	if s == nil {
		return
	}
	s.resetKeymap()
	if s.context != nil {
		C.xkb_context_unref(s.context)
		s.context = nil
	}
}

func (s *xkbKeyboardState) SetKeymap(data []byte) error {
	if s == nil || s.context == nil {
		return fmt.Errorf("xkb state is not initialized")
	}
	if len(data) == 0 {
		return fmt.Errorf("xkb keymap is empty")
	}
	cdata := C.malloc(C.size_t(len(data) + 1))
	if cdata == nil {
		return fmt.Errorf("allocate xkb keymap string")
	}
	defer C.free(cdata)
	keymapBytes := unsafe.Slice((*byte)(cdata), len(data)+1)
	copy(keymapBytes, data)
	keymapBytes[len(data)] = 0

	keymap := C.xkb_keymap_new_from_string(
		s.context,
		(*C.char)(cdata),
		C.XKB_KEYMAP_FORMAT_TEXT_V1,
		C.XKB_KEYMAP_COMPILE_NO_FLAGS,
	)
	if keymap == nil {
		return fmt.Errorf("compile xkb keymap")
	}
	state := C.xkb_state_new(keymap)
	if state == nil {
		C.xkb_keymap_unref(keymap)
		return fmt.Errorf("create xkb state")
	}

	s.resetKeymap()
	s.keymap = keymap
	s.state = state
	return nil
}

func (s *xkbKeyboardState) UpdateMask(depressed, latched, locked, group uint32) ModifierState {
	if s == nil || s.state == nil {
		return ModifierState{}
	}
	C.xkb_state_update_mask(
		s.state,
		C.xkb_mod_mask_t(depressed),
		C.xkb_mod_mask_t(latched),
		C.xkb_mod_mask_t(locked),
		0,
		0,
		C.xkb_layout_index_t(group),
	)
	return s.Modifiers()
}

func (s *xkbKeyboardState) Modifiers() ModifierState {
	if s == nil || s.state == nil {
		return ModifierState{}
	}
	return ModifierState{
		Shift: s.modActive("Shift"),
		Ctrl:  s.modActive("Control"),
		Alt:   s.modActive("Mod1"),
		Super: s.modActive("Mod4"),
	}
}

func (s *xkbKeyboardState) KeyName(rawKey uint32) (string, bool) {
	if s == nil || s.state == nil {
		return "", false
	}
	sym := C.xkb_state_key_get_one_sym(s.state, C.xkb_keycode_t(rawKey+waylandXKBKeycodeOffset))
	if sym == C.XKB_KEY_NoSymbol {
		return "", false
	}
	var name [128]C.char
	n := C.xkb_keysym_get_name(sym, &name[0], C.size_t(len(name)))
	if n <= 0 || int(n) >= len(name) {
		return fmt.Sprintf("keysym:%d", uint32(sym)), true
	}
	return C.GoString(&name[0]), true
}

func (s *xkbKeyboardState) modActive(name string) bool {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	return C.xkb_state_mod_name_is_active(s.state, cname, C.XKB_STATE_MODS_EFFECTIVE) > 0
}

func (s *xkbKeyboardState) resetKeymap() {
	if s.state != nil {
		C.xkb_state_unref(s.state)
		s.state = nil
	}
	if s.keymap != nil {
		C.xkb_keymap_unref(s.keymap)
		s.keymap = nil
	}
}
