# Mouse Keys PRD Additions

## Overlay Visibility Treatment

The grid overlay must remain translucent while staying legible across light, dark, and mixed desktop backgrounds.

### Grid Lines

- Main-grid and selected-cell outline lines use a translucent bluish core color instead of white.
- Each line also renders a wider, darker blue halo behind the core stroke.
- The halo must not turn the overlay into an opaque panel; empty cells remain transparent.
- `appearance.grid_opacity` continues to control the visible grid-line strength.
- `appearance.grid_line_width` continues to control the core stroke width, with the halo derived from that width.

### Edge Labels

- Main-grid edge labels use the same high-contrast treatment as the grid lines.
- Labels render with a green-tinted foreground and a dark outline/halo.
- Labels remain clipped to their existing edge cells:
  - Main-grid column labels stay on the top and bottom edges.
  - Main-grid row labels stay on the left and right edges.
- Labels must remain readable on both bright and dark backgrounds without requiring an opaque label box.

### Hidden Subcell Navigation

- After the main-grid coordinate is entered, the mouse moves immediately to the center of the selected cell.
- The full grid disappears after that move; only the selected-cell outline remains visible.
- The subcell grid is not rendered.
- Direction keys move the cursor by hidden subcell steps:
  - `H` or Left moves left.
  - `J` or Down moves down.
  - `K` or Up moves up.
  - `L` or Right moves right.
- Movement is not limited to the selected cell. It can continue across the focused monitor and clamps only at the focused monitor edges.

## Default Click Key Bindings

The default in-overlay click bindings are:

- `Space` performs a left click.
- `Shift` + `Space` performs a right click.
- `Space` `Space` within `behavior.double_click_timeout_ms` performs a double click.

Config values use xkbcommon keysym names. Shifted key chords are written with the `Shift-` prefix:

```toml
[keybinds]
left_click = "space"
right_click = "Shift-space"
double_click = "space space"
```

The right-click binding must be treated as a modifier chord, not as a two-key sequence. Pressing `Shift-space` must not start or complete the default left-click double-click timeout.

### Wayland Alpha Handling

- The renderer stores logical overlay pixels as straight ARGB for internal composition and snapshot tests.
- Before sending pixels to a Wayland `wl_shm` ARGB buffer, the upload path premultiplies alpha.
- This prevents semi-transparent overlay pixels from appearing as solid white or otherwise over-bright on compositors that consume SHM ARGB as premultiplied alpha.

## Acceptance Checks

- Showing the overlay displays translucent bluish grid lines rather than solid white lines.
- The grid remains transparent between strokes and labels.
- Grid lines remain visible on both very light and very dark backgrounds due to the dark halo.
- Edge letters are green-tinted, outlined, and readable against varied backgrounds.
- The selected-cell outline remains visible after coordinate entry while the rest of the grid is hidden.
- H/J/K/L and arrow keys move through the hidden subcell grid and can move outside the selected cell until the focused monitor edge.
- `Space` left-clicks at the committed cursor point after the double-click timeout.
- `Shift-space` right-clicks at the committed cursor point without emitting a left click.
- `Space Space` within the double-click timeout emits exactly two left-button clicks at the committed cursor point without reopening the main grid between clicks.
- Scaled-monitor rendering still uses logical coordinates and expands pixels correctly for Wayland buffer scale.
