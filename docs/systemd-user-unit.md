# Systemd User Unit Stub

`mousekeys.service` is packaged as a systemd user unit. Install it under the
user manager, not as a system unit.

Build and install the binary somewhere on the user service `PATH`:

```sh
go build -o ~/.local/bin/mousekeys .
```

For smoke tests and non-standard install locations, the unit also honors an
explicit `MOUSEKEYS_INSTALL_PATH` imported into the user manager:

```sh
systemctl --user set-environment MOUSEKEYS_INSTALL_PATH="$HOME/.local/bin/mousekeys"
```

Install and verify the user unit:

```sh
mkdir -p ~/.config/systemd/user
install -m 0644 mousekeys.service ~/.config/systemd/user/mousekeys.service
systemd-analyze --user verify ~/.config/systemd/user/mousekeys.service
systemctl --user daemon-reload
```

Make the Hyprland session environment visible to the systemd user manager before
starting the service. UWSM can handle this; otherwise import the relevant names
from inside the Hyprland session:

```sh
systemctl --user import-environment XDG_RUNTIME_DIR WAYLAND_DISPLAY HYPRLAND_INSTANCE_SIGNATURE
```

Enable, restart, inspect logs, and disable:

```sh
systemctl --user enable --now mousekeys.service
systemctl --user restart mousekeys.service
mousekeys status
journalctl --user -u mousekeys.service -e
systemctl --user disable --now mousekeys.service
```

`mousekeys status` includes the daemon PID, build fields, executable path,
process executable identity, and binary hashes. The real Hyprland smoke harness
should compare those fields with the just-installed binary after restarting the
user service so it can reject a stale daemon from an earlier build.

The full Hyprland and install guide should fold these steps into `README.md` in
the later `docs-hyprland-and-install` task.
