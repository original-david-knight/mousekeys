//go:build cgo && linux

package main

/*
#cgo pkg-config: xkbcommon
#include <stdlib.h>
#include <xkbcommon/xkbcommon.h>
*/
import "C"

import "unsafe"

func validXKBKeysymName(name string) bool {
	if name == "" {
		return false
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	return C.xkb_keysym_from_name(cName, C.XKB_KEYSYM_NO_FLAGS) != C.XKB_KEY_NoSymbol
}
