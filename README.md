# Mouse Keys

Mouse Keys is a keyboard-driven mouse control utility for Hyprland on Wayland.

## Install

Install the `mousekeys` binary somewhere your shell and systemd user manager can resolve it. When building from this checkout:

```sh
go build -o mousekeys .
sudo install -Dm755 ./mousekeys /usr/local/bin/mousekeys
```

The daemon is normally started as a persistent user service. The short-lived `mousekeys show` command sends an IPC request to that daemon and toggles the overlay.

## Hyprland Trigger Keybind

Add the global trigger to your Hyprland config. This hotkey is not stored in `~/.config/mousekeys/config.toml`; Mouse Keys only handles the in-overlay keys after Hyprland runs `mousekeys show`.

Hyprland 0.55+ Lua config:

```lua
hl.bind("SUPER + period", hl.dsp.exec_cmd("mousekeys show"))
```

Legacy hyprlang config:

```ini
bind = SUPER, period, exec, mousekeys show
```

Omarchy users can add a described binding to `~/.config/hypr/bindings.conf`:

```ini
bindd = SUPER, period, Mouse Keys, exec, mousekeys show
```

## systemd User Service

The packaged `mousekeys.service` file is a systemd user unit, not a system unit. It starts the persistent daemon with `/usr/bin/env mousekeys daemon`, restarts on failure, and logs to the user journal:

```sh
journalctl --user -u mousekeys
```

Copy the shipped unit:

```sh
mkdir -p ~/.config/systemd/user
cp mousekeys.service ~/.config/systemd/user/mousekeys.service
systemctl --user daemon-reload
```

The service must run inside a Hyprland Wayland session. `XDG_RUNTIME_DIR`, `WAYLAND_DISPLAY`, and `HYPRLAND_INSTANCE_SIGNATURE` must be available in the systemd user manager environment before the service starts. UWSM is the recommended setup because it imports the Hyprland session environment into the systemd user manager. The shipped unit also has an `ExecStartPre` check that fails fast when those variables are missing.

Omarchy starts Hyprland through UWSM by default, so the environment should normally already be present. If the unit fails its preflight check, run the import command below from inside the Hyprland session and restart the service.

If you are not using UWSM, use an equivalent import before starting the unit, for example from inside the Hyprland session:

```sh
systemctl --user import-environment XDG_RUNTIME_DIR WAYLAND_DISPLAY HYPRLAND_INSTANCE_SIGNATURE
```

Enable and start the user service:

```sh
systemctl --user enable --now mousekeys
```

Disable and stop the user unit:

```sh
systemctl --user disable --now mousekeys
```

## Optional Hyprland Autostart

Use Hyprland autostart only if you are not using the systemd user service.

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

Mouse Keys creates its default config on first daemon start:

```text
~/.config/mousekeys/config.toml
```

The config is reloaded when the daemon restarts. There is no live config reload in v1. Key names use xkbcommon keysym names and are case-sensitive, for example `Return`, `space`, `Tab`, `Escape`, and `BackSpace`.

Default `config.toml`:

```toml
[grid]
size = 26                    # 26x26 main grid
subgrid_pixel_size = 5       # target pixel size per sub-cell

[keybinds]
left_click = "Return"
right_click = "space"
double_click = "Return Return"
commit_partial = "Tab"
exit = "Escape"
backspace = "BackSpace"

[behavior]
stay_active = true           # main grid reappears after click
double_click_timeout_ms = 250

[appearance]
grid_opacity = 0.4
grid_line_width = 1
label_font_size = 14
```

The IPC socket is created at `$XDG_RUNTIME_DIR/mousekeys.sock`.

## v1 Non-Goals

- No click-and-drag.
- No scroll wheel emulation.
- No X11 or non-Hyprland support.
- No multi-monitor selection or coordinate spanning.
- No GUI config tool.
- No live config reload.
- No recursive sub-sub-grid refinement.
