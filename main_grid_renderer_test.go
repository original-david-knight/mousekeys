package main

import (
	"context"
	"testing"
)

func TestMainGridCellBoundsCoverFocusedMonitorLogicalRegion(t *testing.T) {
	for _, tt := range []struct {
		name    string
		monitor Monitor
	}{
		{name: "square", monitor: Monitor{Name: "square", Width: 520, Height: 520, Scale: 1}},
		{name: "non-square", monitor: Monitor{Name: "wide", Width: 780, Height: 520, Scale: 1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			seen := make([]uint8, tt.monitor.Width*tt.monitor.Height)
			covered := 0

			for row := 0; row < 26; row++ {
				for col := 0; col < 26; col++ {
					bounds, err := GridCellBounds(tt.monitor, 26, col, row)
					if err != nil {
						t.Fatalf("grid cell bounds col=%d row=%d: %v", col, row, err)
					}
					if bounds.X < 0 || bounds.Y < 0 || bounds.X+bounds.Width > tt.monitor.Width || bounds.Y+bounds.Height > tt.monitor.Height {
						t.Fatalf("grid cell bounds escape monitor: col=%d row=%d bounds=%+v monitor=%+v", col, row, bounds, tt.monitor)
					}
					for y := bounds.Y; y < bounds.Y+bounds.Height; y++ {
						for x := bounds.X; x < bounds.X+bounds.Width; x++ {
							offset := y*tt.monitor.Width + x
							if seen[offset] != 0 {
								t.Fatalf("logical pixel %d,%d covered more than once", x, y)
							}
							seen[offset] = 1
							covered++
						}
					}
				}
			}

			if want := tt.monitor.Width * tt.monitor.Height; covered != want {
				t.Fatalf("covered logical pixels = %d, want %d", covered, want)
			}
			for offset, count := range seen {
				if count != 1 {
					t.Fatalf("logical pixel offset %d covered %d times, want once", offset, count)
				}
			}
		})
	}
}

func TestRenderMainGridOverlaySnapshotHashes(t *testing.T) {
	config := DefaultConfig()
	atlas, err := NewFontAtlasFromConfig(config)
	if err != nil {
		t.Fatalf("font atlas: %v", err)
	}

	for _, tt := range []struct {
		name   string
		width  int
		height int
		want   string
	}{
		{name: "square", width: 520, height: 520, want: "59f902f0cb62c115bbf136f68ff3bccecb49155f3c701afff5f15cf348c3f8df"},
		{name: "non-square", width: 780, height: 520, want: "553c1de62af38f537843c7e9af31c04ff95de4317b92d7aa8252d3a568535074"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			buffer, err := NewARGBBuffer(tt.width, tt.height)
			if err != nil {
				t.Fatalf("new buffer: %v", err)
			}
			if err := RenderMainGridOverlay(buffer, MainGridRenderOptions{
				GridSize:   config.Grid.Size,
				Appearance: config.Appearance,
				FontAtlas:  atlas,
				HUD:        "M_",
			}); err != nil {
				t.Fatalf("render main grid: %v", err)
			}
			hash, err := ARGBHash(buffer)
			if err != nil {
				t.Fatalf("ARGB hash: %v", err)
			}
			if hash != tt.want {
				t.Fatalf("render hash = %s, want %s", hash, tt.want)
			}
		})
	}
}

func TestRenderMainGridOverlayUsesAppearanceConfigAndHUDString(t *testing.T) {
	appearance := AppearanceConfig{
		GridOpacity:   0.25,
		GridLineWidth: 3,
		LabelFontSize: 18,
	}
	atlas, err := NewFontAtlas(FontAtlasOptions{LabelFontSize: appearance.LabelFontSize})
	if err != nil {
		t.Fatalf("font atlas: %v", err)
	}

	buffer, err := NewARGBBuffer(520, 520)
	if err != nil {
		t.Fatalf("new buffer: %v", err)
	}
	if err := RenderMainGridOverlay(buffer, MainGridRenderOptions{
		GridSize:   26,
		Appearance: appearance,
		FontAtlas:  atlas,
		HUD:        "M_",
	}); err != nil {
		t.Fatalf("render main grid: %v", err)
	}

	lineColor := gridLineCoreColor(appearance.GridOpacity)
	for _, x := range []int{99, 100, 101} {
		if got := argbAt(buffer, x, 35); got != lineColor {
			t.Fatalf("configured grid line pixel %d,35 = %#x, want %#x", x, got, lineColor)
		}
	}
	if got, want := argbAt(buffer, 98, 35), gridLineHaloColor(appearance.GridOpacity); got != want {
		t.Fatalf("configured grid halo pixel = %#x, want %#x", got, want)
	}
	if got := argbAt(buffer, 97, 35); got != 0 {
		t.Fatalf("pixel beside configured grid halo = %#x, want transparent", got)
	}

	assertEdgeHasLabelInk(t, buffer, Rect{X: 0, Y: 0, Width: buffer.Width, Height: 20}, "top")
	assertEdgeHasLabelInk(t, buffer, Rect{X: 0, Y: buffer.Height - 20, Width: buffer.Width, Height: 20}, "bottom")
	assertEdgeHasLabelInk(t, buffer, Rect{X: 0, Y: 0, Width: 20, Height: buffer.Height}, "left")
	assertEdgeHasLabelInk(t, buffer, Rect{X: buffer.Width - 20, Y: 0, Width: 20, Height: buffer.Height}, "right")

	hudHash := mustARGBHash(t, buffer)
	alternateHUD, err := NewARGBBuffer(520, 520)
	if err != nil {
		t.Fatalf("new buffer with alternate HUD: %v", err)
	}
	if err := RenderMainGridOverlay(alternateHUD, MainGridRenderOptions{
		GridSize:   26,
		Appearance: appearance,
		FontAtlas:  atlas,
		HUD:        "__",
	}); err != nil {
		t.Fatalf("render main grid with alternate HUD: %v", err)
	}
	if got := mustARGBHash(t, alternateHUD); got == hudHash {
		t.Fatalf("HUD string did not affect rendered buffer hash")
	}

	textWidth, textHeight, err := atlas.TextSize(FontRoleHUD, "M_")
	if err != nil {
		t.Fatalf("HUD text size: %v", err)
	}
	padX := atlas.HUDFontSize() / 3
	if padX < 6 {
		padX = 6
	}
	padY := atlas.HUDFontSize() / 5
	if padY < 3 {
		padY = 3
	}
	boxWidth := textWidth + padX*2
	boxHeight := textHeight + padY*2
	edgePad := edgeLabelPadding(appearance.LabelFontSize)
	box := Rect{
		X:      centeredInSpan(0, buffer.Width, boxWidth),
		Y:      buffer.Height - (appearance.LabelFontSize + edgePad) - edgePad - boxHeight,
		Width:  boxWidth,
		Height: boxHeight,
	}
	if got := argbAt(buffer, box.X+2, box.Y+2); (got>>24) < 0xb0 || (got&0x00ffffff) > 0x404040 {
		t.Fatalf("HUD background pixel = %#x, want a dark visible HUD fill", got)
	}
}

func TestRenderMainGridOverlayDimsNonSelectedColumns(t *testing.T) {
	config := DefaultConfig()
	atlas, err := NewFontAtlasFromConfig(config)
	if err != nil {
		t.Fatalf("font atlas: %v", err)
	}
	buffer, err := NewARGBBuffer(260, 260)
	if err != nil {
		t.Fatalf("new buffer: %v", err)
	}

	selected := 12
	if err := RenderMainGridOverlay(buffer, MainGridRenderOptions{
		GridSize:       config.Grid.Size,
		Appearance:     config.Appearance,
		FontAtlas:      atlas,
		HUD:            "M_",
		SelectedColumn: &selected,
	}); err != nil {
		t.Fatalf("render selected-column main grid: %v", err)
	}

	selectedX0, selectedX1, err := axisSegment(buffer.Width, config.Grid.Size, selected)
	if err != nil {
		t.Fatalf("selected segment: %v", err)
	}
	dimmedX0, dimmedX1, err := axisSegment(buffer.Width, config.Grid.Size, selected-1)
	if err != nil {
		t.Fatalf("dimmed segment: %v", err)
	}
	y0, y1, err := axisSegment(buffer.Height, config.Grid.Size, 13)
	if err != nil {
		t.Fatalf("middle row segment: %v", err)
	}

	if got, want := argbAt(buffer, centeredInSpan(selectedX0, selectedX1, 1), centeredInSpan(y0, y1, 1)), uint32(0x303a7afe); got != want {
		t.Fatalf("selected column interior pixel = %#x, want selected highlight %#x", got, want)
	}
	if got, want := argbAt(buffer, centeredInSpan(dimmedX0, dimmedX1, 1), centeredInSpan(y0, y1, 1)), uint32(0x70000000); got != want {
		t.Fatalf("dimmed column interior pixel = %#x, want dim overlay %#x", got, want)
	}
}

func TestRenderMainGridOverlayClipsLabelsToEdgeCells(t *testing.T) {
	config := DefaultConfig()
	atlas, err := NewFontAtlasFromConfig(config)
	if err != nil {
		t.Fatalf("font atlas: %v", err)
	}
	buffer, err := NewARGBBuffer(520, 520)
	if err != nil {
		t.Fatalf("new buffer: %v", err)
	}
	if err := RenderMainGridOverlay(buffer, MainGridRenderOptions{
		GridSize:   config.Grid.Size,
		Appearance: config.Appearance,
		FontAtlas:  atlas,
	}); err != nil {
		t.Fatalf("render main grid: %v", err)
	}

	topY0, topY1, err := axisSegment(buffer.Height, config.Grid.Size, 0)
	if err != nil {
		t.Fatalf("top row segment: %v", err)
	}
	bottomY0, bottomY1, err := axisSegment(buffer.Height, config.Grid.Size, config.Grid.Size-1)
	if err != nil {
		t.Fatalf("bottom row segment: %v", err)
	}
	leftX0, leftX1, err := axisSegment(buffer.Width, config.Grid.Size, 0)
	if err != nil {
		t.Fatalf("left column segment: %v", err)
	}
	rightX0, rightX1, err := axisSegment(buffer.Width, config.Grid.Size, config.Grid.Size-1)
	if err != nil {
		t.Fatalf("right column segment: %v", err)
	}

	for y := 0; y < buffer.Height; y++ {
		for x := 0; x < buffer.Width; x++ {
			if !isGridLabelForegroundPixel(argbAt(buffer, x, y)) {
				continue
			}
			onTop := y >= topY0 && y < topY1
			onBottom := y >= bottomY0 && y < bottomY1
			onLeft := x >= leftX0 && x < leftX1
			onRight := x >= rightX0 && x < rightX1
			if !onTop && !onBottom && !onLeft && !onRight {
				t.Fatalf("opaque label ink escaped edge cells at %d,%d", x, y)
			}
		}
	}
}

func TestDaemonShowRendersConfiguredMainGridToOverlay(t *testing.T) {
	ctx := context.Background()
	config := DefaultConfig()
	config.Appearance.GridOpacity = 0.6
	config.Appearance.GridLineWidth = 2
	config.Appearance.LabelFontSize = 16

	atlas, err := NewFontAtlasFromConfig(config)
	if err != nil {
		t.Fatalf("font atlas: %v", err)
	}
	focused := Monitor{Name: "eDP-1", Width: 520, Height: 520, Scale: 1, Focused: true}
	wayland := newFakeWaylandBackend(focused)
	renderer := &fakeRendererSink{}
	controller := NewDaemonController(DaemonDeps{
		MonitorLookup: &fakeFocusedMonitorLookup{monitor: focused},
		Overlay:       wayland,
		Renderer:      renderer,
		Config:        &config,
		FontAtlas:     atlas,
	})
	if err := controller.Show(ctx); err != nil {
		t.Fatalf("show overlay: %v", err)
	}

	expected, err := NewARGBBuffer(focused.Width, focused.Height)
	if err != nil {
		t.Fatalf("new expected buffer: %v", err)
	}
	if err := RenderMainGridOverlay(expected, MainGridRenderOptions{
		GridSize:   config.Grid.Size,
		Appearance: config.Appearance,
		FontAtlas:  atlas,
		HUD:        DefaultMainGridHUD,
	}); err != nil {
		t.Fatalf("render expected grid: %v", err)
	}
	expectedHash := mustARGBHash(t, expected)

	presentations := renderer.Presentations()
	if len(presentations) != 1 {
		t.Fatalf("renderer presentations = %d, want 1", len(presentations))
	}
	if presentations[0].Hash != expectedHash {
		t.Fatalf("renderer hash = %s, want configured grid hash %s", presentations[0].Hash, expectedHash)
	}
	render := requireFakeWaylandEvent(t, wayland.Events(), "render")
	if render.BufferHash != expectedHash {
		t.Fatalf("surface render hash = %s, want configured grid hash %s", render.BufferHash, expectedHash)
	}

	surface, ok := controller.surface.(*fakeOverlaySurface)
	if !ok {
		t.Fatalf("controller surface = %T, want *fakeOverlaySurface", controller.surface)
	}
	if err := surface.SimulateConfigure(ctx, 780, 520); err != nil {
		t.Fatalf("simulate configure: %v", err)
	}
	resizedExpected, err := NewARGBBuffer(780, 520)
	if err != nil {
		t.Fatalf("new resized expected buffer: %v", err)
	}
	if err := RenderMainGridOverlay(resizedExpected, MainGridRenderOptions{
		GridSize:   config.Grid.Size,
		Appearance: config.Appearance,
		FontAtlas:  atlas,
		HUD:        DefaultMainGridHUD,
	}); err != nil {
		t.Fatalf("render resized expected grid: %v", err)
	}
	lastRender := requireLastFakeWaylandEvent(t, wayland.Events(), "render")
	if got, want := lastRender.BufferHash, mustARGBHash(t, resizedExpected); got != want {
		t.Fatalf("surface configure rerender hash = %s, want resized grid hash %s", got, want)
	}
}

func assertEdgeHasLabelInk(t *testing.T, buffer ARGBBuffer, rect Rect, edge string) {
	t.Helper()
	rect, ok := clipRect(rect, buffer.Width, buffer.Height)
	if !ok {
		t.Fatalf("%s edge rect clips to empty", edge)
	}
	for y := rect.Y; y < rect.Y+rect.Height; y++ {
		for x := rect.X; x < rect.X+rect.Width; x++ {
			if isGridLabelForegroundPixel(argbAt(buffer, x, y)) {
				return
			}
		}
	}
	t.Fatalf("%s edge has no opaque label ink", edge)
}

func argbAt(buffer ARGBBuffer, x, y int) uint32 {
	return buffer.Pixels[y*buffer.Stride+x]
}

func mustARGBHash(t *testing.T, buffer ARGBBuffer) string {
	t.Helper()
	hash, err := ARGBHash(buffer)
	if err != nil {
		t.Fatalf("ARGB hash: %v", err)
	}
	return hash
}

func isGridLabelForegroundPixel(pixel uint32) bool {
	alpha := (pixel >> 24) & 0xff
	red := (pixel >> 16) & 0xff
	green := (pixel >> 8) & 0xff
	blue := pixel & 0xff
	return alpha >= 0xe0 && red <= 0xa0 && green >= 0xd0 && blue >= 0x90
}
