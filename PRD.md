# Mouse Keys — Product Requirements Document

## Overview
A keyboard-driven mouse control utility for Arch Linux + Hyprland. Triggered by a global hotkey, it overlays a labeled grid on the focused monitor and lets the user move and click the mouse without touching the physical pointing device.

## Goals
- Move and click the mouse using only the keyboard.
- Fast: from hotkey press to click in roughly 3–5 keystrokes.
- Native to Hyprland/Wayland; no X11 fallback in v1.

## Non-Goals (v1)
- Click-and-drag (deferred to a later version).
- Scroll wheel emulation (out of scope unless added explicitly).
- X11 / non-Hyprland support.
- Multi-monitor coordinate spanning.

## Platform & Stack
- **OS:** Arch Linux
- **Compositor:** Hyprland (wlroots / Wayland). Documentation must include current Hyprland Lua config examples for Hyprland 0.55+ and legacy hyprlang examples for Hyprland <=0.54.
- **Language:** Go
- **Overlay:** `wlr-layer-shell` via a direct Wayland client (no GTK); software-rendered with a shm buffer, font labels pre-rasterized via FreeType bindings or a pre-baked atlas.
- **Input synthesis:** Wayland `wlr-virtual-pointer-unstable-v1` protocol, spoken directly from the daemon. Prefer `create_virtual_pointer_with_output` mapped to the focused monitor's `wl_output` when available. No `ydotool`/`uinput` dependency.
- **IPC:** Unix domain socket (e.g., `$XDG_RUNTIME_DIR/mousekeys.sock`)

## Process Model
- `mousekeys` runs as a **persistent user-level daemon**.
- Primary launch: **systemd user unit** (`mousekeys.service`), enabled by the user.
  - Logs via `journalctl --user -u mousekeys`.
  - `Restart=on-failure`.
  - Runs only inside a Hyprland Wayland session with `XDG_RUNTIME_DIR`, `WAYLAND_DISPLAY`, and `HYPRLAND_INSTANCE_SIGNATURE` available to the service.
  - Recommended with UWSM or another setup that imports the Hyprland session environment into the systemd user manager.
- Alternative (documented): launch `mousekeys daemon` from Hyprland autostart instead of systemd.
- A short-lived client invocation (`mousekeys show`) sends an IPC message to the daemon to display the grid.

## Trigger
- Global hotkey is registered in the user's Hyprland config.
- Default suggested binding (documented, not auto-installed), Hyprland 0.55+:
  ```lua
  hl.bind("SUPER + period", hl.dsp.exec_cmd("mousekeys show"))
  ```
- Legacy hyprlang equivalent:
  ```
  bind = SUPER, period, exec, mousekeys show
  ```
- If `mousekeys show` is invoked while the grid is already shown, it **toggles the overlay off** (equivalent to pressing Escape — cancels and exits without clicking).
- In v1, the overlay does **not** separately bind or interpret `Super+period`; `Esc` is the guaranteed in-overlay cancel path.

## User Flow

### 1. Main grid
- On `mousekeys show`, the daemon overlays a **26×26 grid** on the **focused monitor only**.
- Grid geometry is calculated in monitor-local logical pixels using the focused monitor's logical size. Wayland buffer scaling handles physical pixel density.
- Cell bounds cover the full focused monitor. Uneven divisions are distributed deterministically so every logical pixel belongs to exactly one cell.
- Grid lines are **full and semi-transparent**.
- **Letter labels** (A–Z) are rendered on **all four edges**:
  - Top and bottom edges show column letters (horizontal axis).
  - Left and right edges show row letters (vertical axis).
- Labels appear only at edge cells; interior cells are unlabeled.
- The keyboard is grabbed by the overlay (layer-shell with `keyboard_interactivity = exclusive`).

### 2. Coordinate entry
- User types **two letters**: first = horizontal (column), second = vertical (row).
- Example: `MK` → column M, row K.
- After the **first letter**, the grid dims/hides all non-matching columns; only the selected column remains highlighted.
- A small **HUD** displays the typed input (e.g., `M_`).
- **Backspace** removes the last typed character within the current coordinate input.
- **Invalid keys** (non-letter, non-command) are ignored.

### 3. Cursor move + sub-grid
- After the second letter, the cursor moves to the center of the selected cell.
- The main grid is **hidden** immediately after selection, leaving only the outline of the selected cell visible.
- A hidden sub-cell grid represents the selected main-grid cell:
  - It targets **~5 logical pixels per sub-cell**, capped at **26 sub-cells per axis**.
  - Sub-cell count per axis is `min(26, max(1, round(main_cell_axis_size / subgrid_pixel_size)))`; actual sub-cell size may be larger than the target on high-resolution displays.
  - The user may press Vim movement keys or arrow keys to move by hidden sub-cell steps: `H`/Left, `J`/Down, `K`/Up, `L`/Right.
  - Movement may continue outside the selected main-grid cell and is clamped only by the focused monitor edges.
  - The hidden sub-cell grid itself is not rendered.

### 4. Commit / click
Configurable key bindings (defaults shown):
- `Space` → **left click**
- `Shift` + `Space` → **right click**
- `Space` `Space` within `double_click_timeout_ms` → **double click**
- `Esc` → **commit cursor position without clicking** and **exit** the tool

For the default double-click binding, the daemon waits up to `double_click_timeout_ms` after the first `Space`. If a second `Space` arrives before the timeout, it emits a double click; otherwise it emits a single left click and then applies stay-active behavior.

### 5. Stay-active behavior
- **Default:** after a click, the tool **stays active** — the main grid re-appears for the next coordinate.
- Configurable: `stay_active = false` exits after a single click.
- `Esc` always exits regardless of stay-active setting.

## Visual Design
- Grid lines: thin, semi-transparent.
- Edge labels: high-contrast, large enough to read but proportionate to cell size.
- HUD: small, fixed location (e.g., bottom-center or near cursor), shows in-progress input.
- Cursor remains visible during grid display.

## Configuration

### File location
`~/.config/mousekeys/config.toml`

### Behavior
- Auto-created with defaults on first daemon start if missing.
- Reloaded on daemon restart (no live-reload required in v1).
- Key names use xkbcommon keysym names, case-sensitive (for example `Return`, `space`, `Tab`, `Escape`, `BackSpace`). The optional `Shift-` prefix expresses shifted key chords, for example `Shift-space`.

### Default contents (illustrative)
```toml
[grid]
size = 26                    # 26x26 main grid
subgrid_pixel_size = 5       # target pixel size per sub-cell

[keybinds]
left_click = "space"
right_click = "Shift-space"
double_click = "space space"
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

> Note: the global trigger hotkey (`Super+.`) is **not** in this config — it is set in the user's Hyprland config.

## Hyprland Integration
User adds to Hyprland config.

Hyprland 0.55+:
```lua
hl.bind("SUPER + period", hl.dsp.exec_cmd("mousekeys show"))
```

Legacy hyprlang:
```
bind = SUPER, period, exec, mousekeys show
```

Optional autostart (only if not using systemd), Hyprland 0.55+:
```lua
hl.on("hyprland.start", function()
  hl.exec_cmd("mousekeys daemon")
end)
```

Legacy hyprlang:
```
exec-once = mousekeys daemon
```

## IPC Protocol (internal)
- Socket: `$XDG_RUNTIME_DIR/mousekeys.sock`
- Commands (v1):
  - `show` — display the main grid if inactive; hide it if already active.
  - `hide` — cancel and hide any active overlay.
  - `status` — return daemon state.

## Logging
- Daemon writes to stderr; systemd captures into the journal.
- Log levels: `info` default, `debug` via env var or config.

## Multi-Monitor
- v1: grid renders only on the **currently focused monitor** (determined via Hyprland IPC at trigger time).
- Pointer motion targets the focused monitor's output coordinate space when `create_virtual_pointer_with_output` is available; fallback code must explicitly account for the monitor's virtual-layout origin.
- Multi-monitor selection / spanning is out of scope.

## Acceptance Checks
- `mousekeys show` displays a 26x26 grid on the focused monitor and captures keyboard input.
- A normal left click completes in the expected 3-5 keystrokes from trigger to click.
- After coordinate entry, only the selected-cell outline remains visible and `H/J/K/L` plus arrow keys move the cursor through the hidden sub-cell grid, beyond the selected cell if needed.
- Double-click emits two left-button clicks at the same committed cursor position without reopening the main grid between clicks.
- Focused-monitor behavior works when the focused monitor has a non-zero virtual-layout origin.
- Grid and pointer targeting remain correct on a scaled monitor.
- `Esc` exits without clicking and overrides `stay_active`.

## Out of Scope / Future
- Click-and-drag (planned).
- Scroll wheel emulation.
- Multi-monitor selection.
- X11 support.
- GUI configuration tool.
- Live config reload.
- Recursive (sub-sub-grid) refinement.

## Open Questions (to resolve during implementation)
_All resolved — see body of PRD._
