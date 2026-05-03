// Package wlr contains generated wlroots protocol bindings used by mousekeys.
//
//go:generate go run github.com/rajveermalviya/go-wayland/cmd/go-wayland-scanner -pkg wlr -prefix zwlr -suffix v1 -o layer_shell.go -i wlr-layer-shell-unstable-v1.xml
//go:generate go run github.com/rajveermalviya/go-wayland/cmd/go-wayland-scanner -pkg wlr -prefix zwlr -suffix v1 -o virtual_pointer.go -i wlr-virtual-pointer-unstable-v1.xml
package wlr
