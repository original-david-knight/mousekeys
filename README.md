# mousekeys

Keyboard-driven mouse control for Arch Linux + Hyprland. `mousekeys` runs as a
persistent user daemon; `mousekeys show` asks that daemon to show or toggle off a
26x26 grid on the focused monitor.

## Install

Build and install the binary somewhere the systemd user service can find through
`PATH`:

```sh
go build -o ~/.local/bin/mousekeys .
```

For non-standard install locations and the real-session smoke harness, the
systemd unit also accepts an explicit binary path:

```sh
systemctl --user set-environment MOUSEKEYS_INSTALL_PATH="$HOME/.local/bin/mousekeys"
```

Install the packaged user unit:

```sh
mkdir -p ~/.config/systemd/user
install -m 0644 mousekeys.service ~/.config/systemd/user/mousekeys.service
systemd-analyze --user verify ~/.config/systemd/user/mousekeys.service
systemctl --user daemon-reload
```

`mousekeys.service` is a user unit. Do not install it under
`/etc/systemd/system`.

## Hyprland Trigger

The global trigger hotkey lives in your Hyprland config, not in
`~/.config/mousekeys/config.toml`. Mousekeys does not install or register this
binding for you.

Hyprland 0.55+ Lua config:

```lua
hl.bind("SUPER + period", hl.dsp.exec_cmd("mousekeys show"))
```

Legacy hyprlang config:

```ini
bind = SUPER, period, exec, mousekeys show
```

If your setup uses descriptive `bindd` entries, such as Omarchy-style Hyprland
bindings, the equivalent form is:

```ini
bindd = SUPER, period, Mousekeys overlay, exec, mousekeys show
```

Pressing the trigger while the grid is already shown toggles it off. Inside the
overlay, `Escape` exits without clicking.

## Run With Systemd

The recommended launch path is the `mousekeys.service` systemd user unit. The
service must see the live Hyprland session environment:
`XDG_RUNTIME_DIR`, `WAYLAND_DISPLAY`, and `HYPRLAND_INSTANCE_SIGNATURE`.

Use UWSM or an equivalent session setup that imports the Hyprland environment
into the systemd user manager. Without UWSM, run this from inside the Hyprland
session before starting or restarting the service:

```sh
systemctl --user import-environment XDG_RUNTIME_DIR WAYLAND_DISPLAY HYPRLAND_INSTANCE_SIGNATURE
```

Enable, restart, inspect, and disable the service:

```sh
systemctl --user enable --now mousekeys.service
systemctl --user restart mousekeys.service
mousekeys status
journalctl --user -u mousekeys.service -e
systemctl --user disable --now mousekeys.service
```

After rebuilding or reinstalling `mousekeys`, restart the user service and check
`mousekeys status`. The status output includes daemon PID, build metadata,
executable path, and binary identity fields so smoke checks can catch a stale
installed daemon that is still running an older binary.

## Optional Hyprland Autostart

Use Hyprland autostart only when you are not using `mousekeys.service`. Running
both can leave duplicate or stale daemons.

Hyprland 0.55+ Lua config:

```lua
hl.on("hyprland.start", function()
  hl.exec_cmd("mousekeys daemon")
end)
```

Legacy hyprlang config:

```ini
exec-once = mousekeys daemon
```

## Configuration

Mousekeys reads:

```text
~/.config/mousekeys/config.toml
```

or `$XDG_CONFIG_HOME/mousekeys/config.toml` when `XDG_CONFIG_HOME` is set. The
daemon creates the file with defaults on first start if it does not exist.
Existing config files are not rewritten when built-in defaults change; add new
fields manually if you want to pick up changed defaults after an upgrade.

Current default config:

```toml
[grid]
size = 26
subgrid_pixel_size = 5

[keybinds]
left_click = "space"
right_click = "Shift-space"
double_click = "space space"
exit = "Escape"
backspace = "BackSpace"

[behavior]
stay_active = true
double_click_timeout_ms = 250

[appearance]
grid_opacity = 0.4
grid_line_width = 1
label_font_size = 14
```

The default click bindings are `Space` for left click, `Shift-space` for right
click, and `Space Space` within `double_click_timeout_ms` for double click.
`Shift-space` is a modifier chord, not a two-key sequence.

The global `Super+.` trigger is intentionally excluded from this config. Put
global triggers in Hyprland config; put in-overlay behavior and click bindings in
`config.toml`.

## Smoke Checks

Source-level checks:

```sh
go build ./...
go test ./...
go vet ./...
```

Installed-service checks in a live Hyprland session:

```sh
scripts/smoke_real_hyprland.sh
```

The smoke script prints machine-readable JSON with `status` set to `pass`,
`fail`, or `skip`. A skip is only valid when no live Hyprland session is
detectable. In a live session it builds and installs the binary, installs the
checked-in user unit, restarts `mousekeys.service`, verifies `mousekeys status`
against the rebuilt binary, opens the overlay through
`hyprctl dispatch exec 'mousekeys show'`, injects `M K Space` with an available
test input tool such as `wtype`, `ydotool`, or `dotool`, checks
`hyprctl cursorpos`, checks trace ordering for overlay-unmap-before-click,
repeats after hide/show/show and after service restart, and repeats with
Chrome/Chromium focused when a matching window is present. The literal Hyprland
dispatch check requires Hyprland's exec environment to find `mousekeys` on
`PATH`.

Optional smoke variables:

```sh
MOUSEKEYS_INSTALL_PATH="$HOME/.local/bin/mousekeys" scripts/smoke_real_hyprland.sh
MOUSEKEYS_SMOKE_RESULT=/tmp/mousekeys-smoke.json scripts/smoke_real_hyprland.sh
MOUSEKEYS_SMOKE_TRACE=/tmp/mousekeys-trace.jsonl scripts/smoke_real_hyprland.sh
```

## Troubleshooting

Stale installed daemon:

- Rebuild and reinstall the binary, then run
  `systemctl --user restart mousekeys.service`.
- Run `mousekeys status` and compare the daemon executable/build fields with the
  binary you just installed.
- Check which binary the service can find with `systemctl --user show
  mousekeys.service -p ExecStart` and `command -v mousekeys` from the same
  session.
- Disable Hyprland autostart if the systemd unit is enabled.

Keyboard focus or keymap failures:

- Confirm the daemon is running inside a Hyprland Wayland session, not from a
  plain TTY or non-Hyprland compositor.
- Import `XDG_RUNTIME_DIR`, `WAYLAND_DISPLAY`, and
  `HYPRLAND_INSTANCE_SIGNATURE` into the systemd user manager, or use UWSM to do
  that automatically.
- Inspect logs with `journalctl --user -u mousekeys.service -e`. Missing
  environment, missing Wayland socket, missing Hyprland IPC socket, and keyboard
  keymap errors should be visible there.

Overlay appears but clicks do not reach the app:

- Make sure you are running the rebuilt installed daemon, then restart the
  service. Older daemons may not have the latest overlay-unmap-before-click
  behavior.
- Test with a simple focused Wayland app first, then repeat with the target app.
- If the pointer moves but the button does not land, capture logs and, when
  needed, set `MOUSEKEYS_TRACE_JSONL=/tmp/mousekeys-trace.jsonl` before starting
  the daemon so the overlay lifecycle and pointer button events can be compared.

## v1 Non-Goals

- Click-and-drag.
- Scroll wheel emulation.
- X11 or non-Hyprland support.
- Multi-monitor selection or coordinate spanning.
- GUI configuration.
- Live config reload.
- Recursive sub-sub-grid refinement.
