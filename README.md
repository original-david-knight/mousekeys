# Mouse Keys

Mouse Keys is a keyboard-driven mouse control utility for Hyprland on Wayland.

## systemd User Service

The packaged `mousekeys.service` file is a systemd user unit, not a system unit. It starts the persistent daemon with the installed `mousekeys daemon` command and logs to the user journal:

```sh
journalctl --user -u mousekeys
```

Install the `mousekeys` binary somewhere the systemd user manager can resolve it, or edit `ExecStart` in the unit to use an absolute path. The shipped unit uses `/usr/bin/env mousekeys daemon`.

Copy and enable the unit:

```sh
mkdir -p ~/.config/systemd/user
cp mousekeys.service ~/.config/systemd/user/mousekeys.service
systemctl --user daemon-reload
systemctl --user enable --now mousekeys
```

The service must run inside a Hyprland Wayland session. `XDG_RUNTIME_DIR`, `WAYLAND_DISPLAY`, and `HYPRLAND_INSTANCE_SIGNATURE` must be available in the systemd user manager environment before the service starts. UWSM is the recommended setup; otherwise use an equivalent environment import for the Hyprland session.

Disable and stop the user unit:

```sh
systemctl --user disable --now mousekeys
```
