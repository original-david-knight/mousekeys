package main

import (
	"encoding/binary"
	"fmt"
	"testing"
)

func TestSoftwareRendererMainGridDeterministicAndStyled(t *testing.T) {
	renderer := mustSoftwareRenderer(t, RendererStyle{
		GridOpacity:   0.55,
		GridLineWidth: 2,
		LabelFontSize: 8,
		HUDFontSize:   10,
	})
	monitor := Monitor{Name: "DP-1", LogicalWidth: 260, LogicalHeight: 260, Scale: 1}

	first, err := renderer.RenderMainGrid(monitor, 26)
	if err != nil {
		t.Fatalf("RenderMainGrid returned error: %v", err)
	}
	second, err := renderer.RenderMainGrid(monitor, 26)
	if err != nil {
		t.Fatalf("RenderMainGrid returned error: %v", err)
	}
	if first.StraightHash() != second.StraightHash() {
		t.Fatalf("straight ARGB hash changed across identical renders: %s vs %s", first.StraightHash(), second.StraightHash())
	}

	core := mustPixelAt(t, first, 130, 135)
	if core.A() == 0 || core.B() <= core.R() || core.B() <= core.G() {
		t.Fatalf("grid core pixel is not bluish: %#08x", uint32(core))
	}
	halo := mustPixelAt(t, first, 128, 135)
	if halo.A() == 0 || halo.B() <= halo.G() || halo.G() < halo.R() {
		t.Fatalf("grid halo pixel is not dark blue: %#08x", uint32(halo))
	}
	empty := mustPixelAt(t, first, 135, 135)
	if empty.A() != 0 {
		t.Fatalf("empty cell pixel is not transparent: %#08x", uint32(empty))
	}

	_, _, label := findPixel(t, first, Rect{X: 0, Y: 0, Width: 260, Height: 10}, func(pixel ARGBPixel) bool {
		return pixel.A() > 0 && pixel.G() > pixel.R()+20 && pixel.G() > pixel.B()
	})
	if label.A() == 0 {
		t.Fatal("did not find green-tinted edge label foreground")
	}
}

func TestSoftwareRendererSelectedCellOutlineOnly(t *testing.T) {
	renderer := mustSoftwareRenderer(t, RendererStyle{
		GridOpacity:   0.55,
		GridLineWidth: 2,
		LabelFontSize: 8,
		HUDFontSize:   10,
	})
	monitor := Monitor{Name: "DP-1", LogicalWidth: 260, LogicalHeight: 260, Scale: 1}
	cell := Rect{X: 120, Y: 100, Width: 10, Height: 10}

	snapshot, err := renderer.RenderSelectedCellOutline(monitor, cell)
	if err != nil {
		t.Fatalf("RenderSelectedCellOutline returned error: %v", err)
	}
	if edge := mustPixelAt(t, snapshot, cell.X, cell.Y+cell.Height/2); edge.A() == 0 || edge.B() <= edge.R() {
		t.Fatalf("selected-cell outline edge is not visible/bluish: %#08x", uint32(edge))
	}
	if center := mustPixelAt(t, snapshot, cell.X+cell.Width/2, cell.Y+cell.Height/2); center.A() != 0 {
		t.Fatalf("selected-cell outline filled the cell center: %#08x", uint32(center))
	}
	if outside := mustPixelAt(t, snapshot, 30, 30); outside.A() != 0 {
		t.Fatalf("selected-cell outline rendered outside the selected cell: %#08x", uint32(outside))
	}
}

func TestSoftwareRendererScaledMonitorGridAndSelectedCellUseLogicalPixels(t *testing.T) {
	renderer := mustSoftwareRenderer(t, RendererStyle{
		GridOpacity:   0.55,
		GridLineWidth: 2,
		LabelFontSize: 8,
		HUDFontSize:   10,
	})
	monitor := Monitor{
		Name:          "eDP-1",
		OriginX:       320,
		OriginY:       -80,
		LogicalWidth:  257,
		LogicalHeight: 193,
		Scale:         1.75,
	}
	grid, err := NewGridGeometry(monitor, 26)
	if err != nil {
		t.Fatalf("NewGridGeometry returned error: %v", err)
	}
	cell, err := grid.Cell(12, 8)
	if err != nil {
		t.Fatalf("Cell returned error: %v", err)
	}
	centerX, centerY := cell.X+cell.Width/2, cell.Y+cell.Height/2

	mainGrid, err := renderer.RenderGrid(grid)
	if err != nil {
		t.Fatalf("RenderGrid returned error: %v", err)
	}
	if mainGrid.Width != monitor.LogicalWidth || mainGrid.Height != monitor.LogicalHeight {
		t.Fatalf("main grid snapshot = %dx%d, want logical %dx%d", mainGrid.Width, mainGrid.Height, monitor.LogicalWidth, monitor.LogicalHeight)
	}
	if edge := mustPixelAt(t, mainGrid, cell.X, centerY); edge.A() == 0 || edge.B() <= edge.R() {
		t.Fatalf("main grid vertical boundary at selected cell is not visible/bluish: %#08x", uint32(edge))
	}
	if center := mustPixelAt(t, mainGrid, centerX, centerY); center.A() != 0 {
		t.Fatalf("main grid rendered non-edge pixels inside selected cell: %#08x", uint32(center))
	}

	outline, err := renderer.RenderSelectedCellOutline(monitor, cell)
	if err != nil {
		t.Fatalf("RenderSelectedCellOutline returned error: %v", err)
	}
	if outline.Width != monitor.LogicalWidth || outline.Height != monitor.LogicalHeight {
		t.Fatalf("selected outline snapshot = %dx%d, want logical %dx%d", outline.Width, outline.Height, monitor.LogicalWidth, monitor.LogicalHeight)
	}
	if edge := mustPixelAt(t, outline, cell.X, centerY); edge.A() == 0 || edge.B() <= edge.R() {
		t.Fatalf("selected-cell outline edge is not aligned with grid cell boundary: %#08x", uint32(edge))
	}
	if center := mustPixelAt(t, outline, centerX, centerY); center.A() != 0 {
		t.Fatalf("selected-cell outline filled the cell center on scaled monitor: %#08x", uint32(center))
	}
	otherCell, err := grid.Cell(4, 8)
	if err != nil {
		t.Fatalf("Cell for outside assertion returned error: %v", err)
	}
	if outside := mustPixelAt(t, outline, otherCell.X+otherCell.Width/2, otherCell.Y+otherCell.Height/2); outside.A() != 0 {
		t.Fatalf("selected-cell outline leaked into another logical cell: %#08x", uint32(outside))
	}
}

func TestSoftwareRendererLabelsUseHaloWithoutBoxes(t *testing.T) {
	renderer := mustSoftwareRenderer(t, RendererStyle{
		GridOpacity:   0,
		GridLineWidth: 1,
		LabelFontSize: 8,
		HUDFontSize:   10,
	})
	snapshot, err := renderer.RenderMainGrid(Monitor{Name: "DP-1", LogicalWidth: 260, LogicalHeight: 260, Scale: 1}, 26)
	if err != nil {
		t.Fatalf("RenderMainGrid returned error: %v", err)
	}

	_, _, foreground := findPixel(t, snapshot, Rect{X: 0, Y: 0, Width: 260, Height: 10}, func(pixel ARGBPixel) bool {
		return pixel.A() > 0 && pixel.G() > pixel.R()+20 && pixel.G() > pixel.B()
	})
	if foreground.A() == 0 {
		t.Fatal("did not find label foreground")
	}
	_, _, halo := findPixel(t, snapshot, Rect{X: 0, Y: 0, Width: 260, Height: 10}, func(pixel ARGBPixel) bool {
		return pixel.A() > 0 && pixel.B() > pixel.G() && pixel.G() > pixel.R()
	})
	if halo.A() == 0 {
		t.Fatal("did not find label dark halo")
	}
	if empty := mustPixelAt(t, snapshot, 135, 135); empty.A() != 0 {
		t.Fatalf("label rendering left an opaque box/panel in an empty cell: %#08x", uint32(empty))
	}
}

func TestSoftwareRendererOpacityAndLineWidthControlGridStrength(t *testing.T) {
	monitor := Monitor{Name: "DP-1", LogicalWidth: 260, LogicalHeight: 260, Scale: 1}
	lowOpacity := mustRenderMainGrid(t, RendererStyle{
		GridOpacity:   0.2,
		GridLineWidth: 1,
		LabelFontSize: 8,
		HUDFontSize:   10,
	}, monitor)
	highOpacity := mustRenderMainGrid(t, RendererStyle{
		GridOpacity:   0.8,
		GridLineWidth: 1,
		LabelFontSize: 8,
		HUDFontSize:   10,
	}, monitor)
	if mustPixelAt(t, highOpacity, 130, 135).A() <= mustPixelAt(t, lowOpacity, 130, 135).A() {
		t.Fatalf("higher grid_opacity did not increase visible line alpha: low=%#08x high=%#08x",
			uint32(mustPixelAt(t, lowOpacity, 130, 135)), uint32(mustPixelAt(t, highOpacity, 130, 135)))
	}

	thin := mustRenderMainGrid(t, RendererStyle{
		GridOpacity:   0.5,
		GridLineWidth: 1,
		LabelFontSize: 8,
		HUDFontSize:   10,
	}, monitor)
	thick := mustRenderMainGrid(t, RendererStyle{
		GridOpacity:   0.5,
		GridLineWidth: 3,
		LabelFontSize: 8,
		HUDFontSize:   10,
	}, monitor)
	thinVisible := countNonTransparent(thin, Rect{X: 120, Y: 135, Width: 21, Height: 1})
	thickVisible := countNonTransparent(thick, Rect{X: 120, Y: 135, Width: 21, Height: 1})
	if thickVisible <= thinVisible {
		t.Fatalf("derived halo/core width did not grow with grid_line_width: thin=%d thick=%d", thinVisible, thickVisible)
	}
}

func TestSoftwareRendererGlyphAtlasCoverageAndCache(t *testing.T) {
	renderer := mustSoftwareRenderer(t, RendererStyle{
		GridOpacity:   0.4,
		GridLineWidth: 1,
		LabelFontSize: 14,
		HUDFontSize:   18,
	})
	required := rendererAtlasCharacters()
	if !renderer.GlyphAtlasCovers(required) {
		t.Fatalf("glyph atlas does not cover required label/HUD characters %q", required)
	}
	stats := renderer.GlyphAtlasStats()
	wantBuilds := len([]rune(required)) * 2
	if stats.Strategy != RendererFontStrategy() || stats.LabelSize != 14 || stats.HUDSize != 18 || stats.GlyphBuilds != wantBuilds {
		t.Fatalf("unexpected atlas stats: %+v, want %d builds", stats, wantBuilds)
	}

	before := renderer.glyphBuildCount()
	monitor := Monitor{Name: "DP-1", LogicalWidth: 260, LogicalHeight: 260, Scale: 1}
	for i := 0; i < 3; i++ {
		if _, err := renderer.RenderMainGrid(monitor, 26); err != nil {
			t.Fatalf("RenderMainGrid returned error: %v", err)
		}
		if _, err := renderer.RenderHUD(180, 60, "M_ 12:/.-"); err != nil {
			t.Fatalf("RenderHUD returned error: %v", err)
		}
	}
	if after := renderer.glyphBuildCount(); after != before {
		t.Fatalf("glyph atlas was rebuilt on the hot render path: before=%d after=%d", before, after)
	}
}

func TestWaylandPremultipliedUploadKeepsSemitransparentStrokeBluish(t *testing.T) {
	snapshot := mustRenderMainGrid(t, RendererStyle{
		GridOpacity:   0.55,
		GridLineWidth: 2,
		LabelFontSize: 8,
		HUDFontSize:   10,
	}, Monitor{Name: "DP-1", LogicalWidth: 260, LogicalHeight: 260, Scale: 1})

	x, y, straight := findPixel(t, snapshot, Rect{X: 120, Y: 120, Width: 30, Height: 30}, func(pixel ARGBPixel) bool {
		return pixel.A() > 0 && pixel.A() < 255 && !IsPremultipliedARGB(pixel) && pixel.B() > pixel.R()
	})
	if straight.A() == 0 {
		t.Fatal("did not find a semi-transparent straight-ARGB stroke pixel")
	}
	index := y*snapshot.Width + x
	upload := snapshot.PremultipliedForWayland()
	premultiplied := upload[index]
	if !IsPremultipliedARGB(premultiplied) {
		t.Fatalf("upload pixel is not premultiplied: straight=%#08x upload=%#08x", uint32(straight), uint32(premultiplied))
	}
	if premultiplied.A() != straight.A() {
		t.Fatalf("premultiply changed alpha: straight=%d upload=%d", straight.A(), premultiplied.A())
	}
	if premultiplied.R() == 255 && premultiplied.G() == 255 && premultiplied.B() == 255 {
		t.Fatalf("semi-transparent stroke became solid white on upload: %#08x", uint32(premultiplied))
	}
	if premultiplied.B() <= premultiplied.R() {
		t.Fatalf("premultiplied stroke lost bluish color: straight=%#08x upload=%#08x", uint32(straight), uint32(premultiplied))
	}

	bytes := snapshot.PremultipliedForWaylandBytes()
	got := ARGBPixel(binary.LittleEndian.Uint32(bytes[index*4:]))
	if got != premultiplied {
		t.Fatalf("wl_shm byte path differs from premultiplied pixels: bytes=%#08x pixels=%#08x", uint32(got), uint32(premultiplied))
	}
}

func TestWaylandUploadScalesLogicalSnapshotAndAvoidsOverbrightAlpha(t *testing.T) {
	straightTranslucentWhite := StraightARGB(128, 255, 255, 255)
	premultipliedWhite := PremultiplyARGBPixel(straightTranslucentWhite)
	if premultipliedWhite != StraightARGB(128, 128, 128, 128) {
		t.Fatalf("translucent white premultiplied to %#08x, want alpha-limited channels", uint32(premultipliedWhite))
	}

	straightBlue := StraightARGB(128, 40, 80, 255)
	snapshot, err := NewARGBSnapshot(2, 2, []ARGBPixel{
		straightBlue, StraightARGB(255, 1, 2, 3),
		StraightARGB(64, 200, 120, 40), StraightARGB(0, 250, 250, 250),
	})
	if err != nil {
		t.Fatalf("NewARGBSnapshot returned error: %v", err)
	}
	scale := waylandIntegerBufferScale(1.5)
	if scale != 2 {
		t.Fatalf("waylandIntegerBufferScale(1.5) = %d, want 2", scale)
	}
	scaled := scaleARGBSnapshotNearest(snapshot, scale)
	if scaled.Width != 4 || scaled.Height != 4 {
		t.Fatalf("scaled snapshot size = %dx%d, want 4x4", scaled.Width, scaled.Height)
	}
	for y := 0; y < scaled.Height; y++ {
		for x := 0; x < scaled.Width; x++ {
			want := snapshot.Pixels[(y/scale)*snapshot.Width+x/scale]
			if got := mustPixelAt(t, scaled, x, y); got != want {
				t.Fatalf("scaled pixel %d,%d = %#08x, want nearest source %#08x", x, y, uint32(got), uint32(want))
			}
		}
	}
	if got := mustPixelAt(t, snapshot, 0, 0); got != straightBlue {
		t.Fatalf("straight snapshot was mutated by scaling: %#08x", uint32(got))
	}

	upload := scaled.PremultipliedForWayland()
	first := upload[0]
	if first == straightBlue {
		t.Fatalf("Wayland upload pixel equals straight source after premultiplication: %#08x", uint32(first))
	}
	if !IsPremultipliedARGB(first) || first.R() > first.A() || first.G() > first.A() || first.B() > first.A() {
		t.Fatalf("Wayland upload pixel over-brightened translucent channels: straight=%#08x upload=%#08x", uint32(straightBlue), uint32(first))
	}
	if len(scaled.PremultipliedForWaylandBytes()) != scaled.Width*scaled.Height*4 {
		t.Fatalf("wl_shm byte length does not match scaled ARGB buffer dimensions")
	}
}

func TestRendererTransparentCellsCompositeExactlyOverLightAndDark(t *testing.T) {
	snapshot := mustRenderMainGrid(t, RendererStyle{
		GridOpacity:   0.55,
		GridLineWidth: 2,
		LabelFontSize: 8,
		HUDFontSize:   10,
	}, Monitor{Name: "DP-1", LogicalWidth: 260, LogicalHeight: 260, Scale: 1})

	emptyX, emptyY := 135, 135
	if empty := mustPixelAt(t, snapshot, emptyX, emptyY); empty.A() != 0 {
		t.Fatalf("expected transparent empty cell before compositing, got %#08x", uint32(empty))
	}

	light := StraightARGB(255, 248, 249, 250)
	dark := StraightARGB(255, 4, 6, 12)
	lightComposite := snapshot.CompositeOver(light)
	darkComposite := snapshot.CompositeOver(dark)
	if got := mustPixelAt(t, lightComposite, emptyX, emptyY); got != light {
		t.Fatalf("transparent empty cell changed light background: got %#08x want %#08x", uint32(got), uint32(light))
	}
	if got := mustPixelAt(t, darkComposite, emptyX, emptyY); got != dark {
		t.Fatalf("transparent empty cell changed dark background: got %#08x want %#08x", uint32(got), uint32(dark))
	}

	lineX, lineY := 130, 135
	if got := mustPixelAt(t, lightComposite, lineX, lineY); got == light {
		t.Fatalf("grid stroke disappeared on light composite at %d,%d", lineX, lineY)
	}
	if got := mustPixelAt(t, darkComposite, lineX, lineY); got == dark {
		t.Fatalf("grid stroke disappeared on dark composite at %d,%d", lineX, lineY)
	}
}

func TestSoftwareRendererLabelsAndHalosCompositeReadableOnLightAndDark(t *testing.T) {
	snapshot := mustRenderMainGrid(t, RendererStyle{
		GridOpacity:   0.55,
		GridLineWidth: 2,
		LabelFontSize: 8,
		HUDFontSize:   10,
	}, Monitor{Name: "DP-1", LogicalWidth: 260, LogicalHeight: 260, Scale: 1})

	labelX, labelY, label := findPixel(t, snapshot, Rect{X: 0, Y: 0, Width: 260, Height: 10}, func(pixel ARGBPixel) bool {
		return pixel.A() > 0 && pixel.G() > pixel.R()+20 && pixel.G() > pixel.B()
	})
	if label.A() == 0 {
		t.Fatal("did not find label foreground")
	}
	haloRect := Rect{
		X:      maxInt(0, labelX-2),
		Y:      maxInt(0, labelY-2),
		Width:  minInt(snapshot.Width, labelX+3) - maxInt(0, labelX-2),
		Height: minInt(snapshot.Height, labelY+3) - maxInt(0, labelY-2),
	}
	haloX, haloY, halo := findPixel(t, snapshot, haloRect, func(pixel ARGBPixel) bool {
		return pixel.A() > 0 && pixel.B() > pixel.G() && pixel.G() > pixel.R()
	})
	if halo.A() == 0 {
		t.Fatal("did not find label halo near foreground")
	}

	backgrounds := map[string]ARGBPixel{
		"light": StraightARGB(255, 248, 249, 250),
		"dark":  StraightARGB(255, 4, 6, 12),
	}
	for name, background := range backgrounds {
		t.Run(name, func(t *testing.T) {
			composite := snapshot.CompositeOver(background)
			compositedLabel := mustPixelAt(t, composite, labelX, labelY)
			compositedHalo := mustPixelAt(t, composite, haloX, haloY)
			if colorDistanceRGB(compositedLabel, background) < 80 {
				t.Fatalf("label foreground is too close to %s background: label=%#08x background=%#08x", name, uint32(compositedLabel), uint32(background))
			}
			if colorDistanceRGB(compositedLabel, compositedHalo) < 60 {
				t.Fatalf("label foreground lacks halo contrast on %s background: label=%#08x halo=%#08x", name, uint32(compositedLabel), uint32(compositedHalo))
			}
			if name == "light" && colorDistanceRGB(compositedHalo, background) < 80 {
				t.Fatalf("label halo is too close to light background: halo=%#08x background=%#08x", uint32(compositedHalo), uint32(background))
			}
		})
	}
}

func TestSoftwareRendererCoordinateGridDimsColumnsAndDrawsHUDOnSquareAndNonSquare(t *testing.T) {
	renderer := mustSoftwareRenderer(t, RendererStyle{
		GridOpacity:   0.55,
		GridLineWidth: 2,
		LabelFontSize: 8,
		HUDFontSize:   12,
	})

	for _, monitor := range []Monitor{
		{Name: "DP-1", LogicalWidth: 260, LogicalHeight: 260, Scale: 1},
		{Name: "DP-1", LogicalWidth: 257, LogicalHeight: 193, Scale: 1},
	} {
		t.Run(fmt.Sprintf("%dx%d", monitor.LogicalWidth, monitor.LogicalHeight), func(t *testing.T) {
			grid, err := NewGridGeometry(monitor, 26)
			if err != nil {
				t.Fatalf("NewGridGeometry returned error: %v", err)
			}

			mainGrid, err := renderer.RenderMainGrid(monitor, 26)
			if err != nil {
				t.Fatalf("RenderMainGrid returned error: %v", err)
			}
			coordinateGrid, err := renderer.RenderCoordinateGrid(monitor, 26, CoordinateRenderState{
				Input:             "M",
				SelectedColumn:    12,
				HasSelectedColumn: true,
			})
			if err != nil {
				t.Fatalf("RenderCoordinateGrid returned error: %v", err)
			}
			if coordinateGrid.Width != monitor.LogicalWidth || coordinateGrid.Height != monitor.LogicalHeight {
				t.Fatalf("coordinate snapshot size = %dx%d, want %dx%d", coordinateGrid.Width, coordinateGrid.Height, monitor.LogicalWidth, monitor.LogicalHeight)
			}
			if coordinateGrid.StraightHash() == mainGrid.StraightHash() {
				t.Fatal("coordinate render state did not change the snapshot")
			}

			selectedX := (grid.Columns[12].Start + grid.Columns[12].End) / 2
			dimmedX := (grid.Columns[4].Start + grid.Columns[4].End) / 2
			lineY := grid.Rows[10].Start
			selectedLine := mustPixelAt(t, coordinateGrid, selectedX, lineY)
			dimmedLine := mustPixelAt(t, coordinateGrid, dimmedX, lineY)
			if selectedLine.A() <= dimmedLine.A() {
				t.Fatalf("selected column line alpha = %d, dimmed column line alpha = %d; want selected brighter", selectedLine.A(), dimmedLine.A())
			}

			bottomReserved := grid.Rows[len(grid.Rows)-1].Size()
			hudRect := Rect{
				X:      monitor.LogicalWidth / 3,
				Y:      monitor.LogicalHeight - bottomReserved - 36,
				Width:  monitor.LogicalWidth / 3,
				Height: 36,
			}
			_, _, hud := findPixel(t, coordinateGrid, hudRect, func(pixel ARGBPixel) bool {
				return pixel.A() > 0 && pixel.G() > pixel.R()+20 && pixel.G() > pixel.B()
			})
			if hud.A() == 0 {
				t.Fatal("did not find HUD foreground for M_")
			}

			light := coordinateGrid.CompositeOver(StraightARGB(255, 248, 249, 250))
			dark := coordinateGrid.CompositeOver(StraightARGB(255, 4, 6, 12))
			if got := mustPixelAt(t, light, selectedX, lineY); got == StraightARGB(255, 248, 249, 250) {
				t.Fatalf("selected line disappeared on light composite: %#08x", uint32(got))
			}
			if got := mustPixelAt(t, dark, selectedX, lineY); got == StraightARGB(255, 4, 6, 12) {
				t.Fatalf("selected line disappeared on dark composite: %#08x", uint32(got))
			}
		})
	}
}

func mustSoftwareRenderer(t *testing.T, style RendererStyle) *SoftwareRenderer {
	t.Helper()
	renderer, err := NewSoftwareRendererWithStyle(style)
	if err != nil {
		t.Fatalf("NewSoftwareRendererWithStyle returned error: %v", err)
	}
	return renderer
}

func mustRenderMainGrid(t *testing.T, style RendererStyle, monitor Monitor) ARGBSnapshot {
	t.Helper()
	renderer := mustSoftwareRenderer(t, style)
	snapshot, err := renderer.RenderMainGrid(monitor, 26)
	if err != nil {
		t.Fatalf("RenderMainGrid returned error: %v", err)
	}
	return snapshot
}

func mustPixelAt(t *testing.T, snapshot ARGBSnapshot, x, y int) ARGBPixel {
	t.Helper()
	pixel, ok := snapshot.PixelAt(x, y)
	if !ok {
		t.Fatalf("PixelAt(%d,%d) outside %dx%d snapshot", x, y, snapshot.Width, snapshot.Height)
	}
	return pixel
}

func findPixel(t *testing.T, snapshot ARGBSnapshot, rect Rect, predicate func(ARGBPixel) bool) (int, int, ARGBPixel) {
	t.Helper()
	for y := rect.Y; y < rect.Y+rect.Height; y++ {
		for x := rect.X; x < rect.X+rect.Width; x++ {
			pixel, ok := snapshot.PixelAt(x, y)
			if !ok {
				continue
			}
			if predicate(pixel) {
				return x, y, pixel
			}
		}
	}
	t.Fatalf("no matching pixel found in rect %+v", rect)
	return 0, 0, 0
}

func countNonTransparent(snapshot ARGBSnapshot, rect Rect) int {
	count := 0
	for y := rect.Y; y < rect.Y+rect.Height; y++ {
		for x := rect.X; x < rect.X+rect.Width; x++ {
			pixel, ok := snapshot.PixelAt(x, y)
			if ok && pixel.A() > 0 {
				count++
			}
		}
	}
	return count
}

func colorDistanceRGB(a, b ARGBPixel) int {
	return absInt(int(a.R())-int(b.R())) + absInt(int(a.G())-int(b.G())) + absInt(int(a.B())-int(b.B()))
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
