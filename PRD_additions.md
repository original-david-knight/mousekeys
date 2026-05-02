# Mouse Keys PRD Additions

## Overlay Visibility Treatment

The grid overlay must remain translucent while staying legible across light, dark, and mixed desktop backgrounds.

### Grid Lines

- Main-grid and subgrid lines use a translucent bluish core color instead of white.
- Each line also renders a wider, darker blue halo behind the core stroke.
- The halo must not turn the overlay into an opaque panel; empty cells remain transparent.
- `appearance.grid_opacity` continues to control the visible grid-line strength.
- `appearance.grid_line_width` continues to control the core stroke width, with the halo derived from that width.

### Edge Labels

- Main-grid edge labels and subgrid edge labels use the same high-contrast treatment as the grid lines.
- Labels render with a green-tinted foreground and a dark outline/halo.
- Labels remain clipped to their existing edge cells:
  - Main-grid column labels stay on the top and bottom edges.
  - Main-grid row labels stay on the left and right edges.
  - Subgrid labels stay on the top and left edges.
- Labels must remain readable on both bright and dark backgrounds without requiring an opaque label box.

### Wayland Alpha Handling

- The renderer stores logical overlay pixels as straight ARGB for internal composition and snapshot tests.
- Before sending pixels to a Wayland `wl_shm` ARGB buffer, the upload path premultiplies alpha.
- This prevents semi-transparent overlay pixels from appearing as solid white or otherwise over-bright on compositors that consume SHM ARGB as premultiplied alpha.

## Acceptance Checks

- Showing the overlay displays translucent bluish grid lines rather than solid white lines.
- The grid remains transparent between strokes and labels.
- Grid lines remain visible on both very light and very dark backgrounds due to the dark halo.
- Edge letters are green-tinted, outlined, and readable against varied backgrounds.
- The subgrid uses the same visual treatment as the main grid.
- Scaled-monitor rendering still uses logical coordinates and expands pixels correctly for Wayland buffer scale.
