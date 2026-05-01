# Wayland Binding Strategy

Mouse Keys uses the pure-Go `github.com/rajveermalviya/go-wayland/wayland`
client bindings pinned in `go.mod`. Core Wayland bindings and `zxdg-output`
come from that pinned module.

The wlroots protocols required by the PRD are checked in under
`protocols/wlr/` from wlroots `0.18.2`:

- `wlr-layer-shell-unstable-v1.xml`
- `wlr-virtual-pointer-unstable-v1.xml`

Generated Go bindings for those XML files are checked in under
`internal/waylandprotocols/`. Regenerate them reproducibly with:

```sh
go generate ./internal/waylandprotocols/...
```

The generation commands pin
`github.com/rajveermalviya/go-wayland/cmd/go-wayland-scanner@v0.0.0-20230130181619-0ad78d1310b2`
in the package `doc.go` files. The build must not depend on system protocol
XML files such as `/usr/share/wayland-protocols`, on hand-edited generated
code, or on an unrecorded local scanner checkout.

The compositor path remains a direct Wayland client: no GTK, EGL, or GPU
overlay stack is introduced. Buffers for future rendering tasks should use
`wl_shm`.
