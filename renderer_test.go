package main

import (
	"encoding/binary"
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
