//go:build cgo && linux

package main

/*
#cgo pkg-config: xkbcommon
#include <stdlib.h>
#include <xkbcommon/xkbcommon.h>

static char* mousekeys_compile_keymap_from_names(const char *model, const char *layout) {
	struct xkb_context *context = xkb_context_new(XKB_CONTEXT_NO_FLAGS);
	if (context == NULL) {
		return NULL;
	}
	struct xkb_rule_names names = {
		.rules = NULL,
		.model = model,
		.layout = layout,
		.variant = NULL,
		.options = NULL,
	};
	struct xkb_keymap *keymap = xkb_keymap_new_from_names(context, &names, XKB_KEYMAP_COMPILE_NO_FLAGS);
	if (keymap == NULL) {
		xkb_context_unref(context);
		return NULL;
	}
	char *out = xkb_keymap_get_as_string(keymap, XKB_KEYMAP_FORMAT_TEXT_V1);
	xkb_keymap_unref(keymap);
	xkb_context_unref(context);
	return out;
}

static int mousekeys_shift_active(struct xkb_state *state) {
	return xkb_state_mod_name_is_active(state, XKB_MOD_NAME_SHIFT, XKB_STATE_MODS_EFFECTIVE) > 0;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

type xkbKeymapState struct {
	context *C.struct_xkb_context
	keymap  *C.struct_xkb_keymap
	state   *C.struct_xkb_state
}

func newKeyboardKeymapState(format uint32, keymap []byte) (keyboardKeymapState, error) {
	if format != keyboardKeymapFormatXKBV1 {
		return nil, fmt.Errorf("unsupported wl_keyboard keymap format %d", format)
	}
	if len(keymap) == 0 {
		return nil, fmt.Errorf("wl_keyboard keymap is empty")
	}

	context := C.xkb_context_new(C.XKB_CONTEXT_NO_FLAGS)
	if context == nil {
		return nil, fmt.Errorf("create xkb context")
	}

	cKeymap := C.CString(string(keymap))
	defer C.free(unsafe.Pointer(cKeymap))

	compiled := C.xkb_keymap_new_from_string(
		context,
		cKeymap,
		C.XKB_KEYMAP_FORMAT_TEXT_V1,
		C.XKB_KEYMAP_COMPILE_NO_FLAGS,
	)
	if compiled == nil {
		C.xkb_context_unref(context)
		return nil, fmt.Errorf("compile wl_keyboard xkb keymap")
	}

	state := C.xkb_state_new(compiled)
	if state == nil {
		C.xkb_keymap_unref(compiled)
		C.xkb_context_unref(context)
		return nil, fmt.Errorf("create xkb state")
	}

	return &xkbKeymapState{
		context: context,
		keymap:  compiled,
		state:   state,
	}, nil
}

func compileXKBKeymapFromNames(model string, layout string) ([]byte, error) {
	if model == "" {
		model = "pc105"
	}
	if layout == "" {
		layout = "us"
	}

	cModel := C.CString(model)
	defer C.free(unsafe.Pointer(cModel))
	cLayout := C.CString(layout)
	defer C.free(unsafe.Pointer(cLayout))

	cKeymap := C.mousekeys_compile_keymap_from_names(cModel, cLayout)
	if cKeymap == nil {
		return nil, fmt.Errorf("compile xkb keymap model=%q layout=%q", model, layout)
	}
	defer C.free(unsafe.Pointer(cKeymap))

	keymap := []byte(C.GoString(cKeymap))
	return append(keymap, 0), nil
}

func (s *xkbKeymapState) KeySymName(keycode uint32) (string, error) {
	if s == nil || s.state == nil {
		return "", fmt.Errorf("xkb state is not initialized")
	}

	sym := C.xkb_state_key_get_one_sym(s.state, C.xkb_keycode_t(keycode+8))
	if sym == C.XKB_KEY_NoSymbol {
		return "", nil
	}

	var buf [128]C.char
	n := C.xkb_keysym_get_name(C.xkb_keysym_t(sym), &buf[0], C.size_t(len(buf)))
	if n <= 0 {
		return "", nil
	}
	if int(n) >= len(buf) {
		return "", fmt.Errorf("xkb keysym name exceeded %d bytes", len(buf))
	}
	return C.GoString(&buf[0]), nil
}

func (s *xkbKeymapState) Modifiers() KeyboardModifiers {
	if s == nil || s.state == nil {
		return KeyboardModifiers{}
	}
	return KeyboardModifiers{
		Shift: C.mousekeys_shift_active(s.state) != 0,
	}
}

func (s *xkbKeymapState) UpdateKey(keycode uint32, pressed bool) {
	if s == nil || s.state == nil {
		return
	}
	direction := C.enum_xkb_key_direction(C.XKB_KEY_UP)
	if pressed {
		direction = C.enum_xkb_key_direction(C.XKB_KEY_DOWN)
	}
	C.xkb_state_update_key(s.state, C.xkb_keycode_t(keycode+8), direction)
}

func (s *xkbKeymapState) UpdateMask(depressed, latched, locked, group uint32) {
	if s == nil || s.state == nil {
		return
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
}

func (s *xkbKeymapState) Reset() {
	s.UpdateMask(0, 0, 0, 0)
}

func (s *xkbKeymapState) Close() {
	if s == nil {
		return
	}
	if s.state != nil {
		C.xkb_state_unref(s.state)
		s.state = nil
	}
	if s.keymap != nil {
		C.xkb_keymap_unref(s.keymap)
		s.keymap = nil
	}
	if s.context != nil {
		C.xkb_context_unref(s.context)
		s.context = nil
	}
}
