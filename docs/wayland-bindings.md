# Wayland Binding Reproduction

Mousekeys uses `github.com/rajveermalviya/go-wayland/wayland` pinned in `go.mod`
for the core Wayland client protocol and xdg-output bindings. The wlroots
protocols that Hyprland needs are checked in under `internal/wayland/wlr`:

- `wlr-layer-shell-unstable-v1.xml`
- `wlr-virtual-pointer-unstable-v1.xml`

Those XMLs were imported from `wayland-protocols-wlr` 0.3.12. After import,
regeneration depends only on the committed XMLs and pinned Go modules.

The generated Go files in that directory are produced by the pinned
`github.com/rajveermalviya/go-wayland/cmd/go-wayland-scanner` tool. Regenerate
them with:

```sh
go generate ./internal/wayland/wlr
```

Do not hand-edit `layer_shell.go` or `virtual_pointer.go`; edit the committed
XMLs or the pinned scanner version, then regenerate.
