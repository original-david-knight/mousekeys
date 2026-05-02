package main

import "testing"

func TestFontAtlasBuildsConfiguredLabelAndHUDGlyphs(t *testing.T) {
	config := DefaultConfig()
	config.Appearance.LabelFontSize = 14
	atlas, err := NewFontAtlasFromConfig(config)
	if err != nil {
		t.Fatalf("new font atlas: %v", err)
	}
	if got, want := atlas.LabelFontSize(), 14; got != want {
		t.Fatalf("label font size = %d, want %d", got, want)
	}
	if got, want := atlas.HUDFontSize(), 18; got != want {
		t.Fatalf("HUD font size = %d, want %d", got, want)
	}

	expectedGlyphs := len([]rune(fontAtlasCharset))
	if got := atlas.GlyphCount(FontRoleLabel); got != expectedGlyphs {
		t.Fatalf("label glyph count = %d, want %d", got, expectedGlyphs)
	}
	if got := atlas.GlyphCount(FontRoleHUD); got != expectedGlyphs {
		t.Fatalf("HUD glyph count = %d, want %d", got, expectedGlyphs)
	}

	for r := 'A'; r <= 'Z'; r++ {
		label := requireGlyph(t, atlas, FontRoleLabel, r)
		if label.Height != 14 {
			t.Fatalf("label glyph %q height = %d, want 14", r, label.Height)
		}
		if !glyphHasInk(label) {
			t.Fatalf("label glyph %q has no opaque pixels", r)
		}

		hud := requireGlyph(t, atlas, FontRoleHUD, r)
		if hud.Height != 18 {
			t.Fatalf("HUD glyph %q height = %d, want 18", r, hud.Height)
		}
		if !glyphHasInk(hud) {
			t.Fatalf("HUD glyph %q has no opaque pixels", r)
		}
	}

	for _, r := range "0123456789 _-.:,/\\+()[]!?\"'" {
		label := requireGlyph(t, atlas, FontRoleLabel, r)
		if label.Height != 14 {
			t.Fatalf("label glyph %q height = %d, want 14", r, label.Height)
		}
		hud := requireGlyph(t, atlas, FontRoleHUD, r)
		if hud.Height != 18 {
			t.Fatalf("HUD glyph %q height = %d, want 18", r, hud.Height)
		}
	}

	lowercase, ok := atlas.LabelGlyph('m')
	if !ok || lowercase.Rune != 'M' {
		t.Fatalf("lowercase lookup = %+v ok=%v, want normalized M glyph", lowercase, ok)
	}
	if _, ok := atlas.LabelGlyph('@'); ok {
		t.Fatalf("unsupported glyph @ returned ok")
	}
}

func TestFontAtlasGlyphHashesStable(t *testing.T) {
	atlas, err := NewFontAtlas(FontAtlasOptions{LabelFontSize: 14, HUDFontSize: 20})
	if err != nil {
		t.Fatalf("new font atlas: %v", err)
	}

	tests := []struct {
		name string
		role FontRole
		r    rune
		want string
	}{
		{name: "label A", role: FontRoleLabel, r: 'A', want: "5c38992ed3f882d9d52acbb08fec0e8c25eb2b53bc3bec4bfd65e654a63cc6b2"},
		{name: "label M", role: FontRoleLabel, r: 'M', want: "c40b727f60a9d6880adfab3f888735696142b670dc7b14b11030c1bf47209034"},
		{name: "label underscore", role: FontRoleLabel, r: '_', want: "bd233d7ca9ef4ca560d08377cad69c4a2ffe80d548beaf8d9807bcf6092cf33f"},
		{name: "label 7", role: FontRoleLabel, r: '7', want: "b527f4a23ba299ee3ad6e9e3fb3db97992fb36956fff0a3cb2f405fe955a9d00"},
		{name: "HUD A", role: FontRoleHUD, r: 'A', want: "f2d5d724d2d088af0af8fecb826d249d15b344df55fccb488e8db0a2b579bf46"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash, err := GlyphHash(requireGlyph(t, atlas, tt.role, tt.r))
			if err != nil {
				t.Fatalf("glyph hash: %v", err)
			}
			if hash != tt.want {
				t.Fatalf("glyph hash = %s, want %s", hash, tt.want)
			}
		})
	}
}

func TestCompositeGlyphSemiTransparentBlend(t *testing.T) {
	buffer, err := NewARGBBuffer(4, 4)
	if err != nil {
		t.Fatalf("new ARGB buffer: %v", err)
	}
	for i := range buffer.Pixels {
		buffer.Pixels[i] = 0xff000000
	}

	glyph := GlyphBitmap{
		Rune:     '*',
		Width:    2,
		Height:   2,
		Advance:  2,
		Baseline: 2,
		Pixels: []uint32{
			0xffffffff, 0x80ffffff,
			0x00ffffff, 0xffffffff,
		},
	}

	if err := CompositeGlyph(buffer, glyph, 1, 1, 0x80ff0000); err != nil {
		t.Fatalf("composite glyph: %v", err)
	}

	assertPixel(t, buffer, 1, 1, 0xff800000)
	assertPixel(t, buffer, 2, 1, 0xff400000)
	assertPixel(t, buffer, 1, 2, 0xff000000)
	assertPixel(t, buffer, 2, 2, 0xff800000)
}

func TestCompositeTextUsesCachedGlyphs(t *testing.T) {
	atlas, err := NewFontAtlas(FontAtlasOptions{LabelFontSize: 12})
	if err != nil {
		t.Fatalf("new font atlas: %v", err)
	}
	initialRasterizations := atlas.RasterizationCount()

	buffer, err := NewARGBBuffer(64, 24)
	if err != nil {
		t.Fatalf("new ARGB buffer: %v", err)
	}
	bounds, err := CompositeText(buffer, atlas, FontRoleLabel, "M_", 2, 3, 0xccffffff)
	if err != nil {
		t.Fatalf("composite text: %v", err)
	}
	if bounds.Width <= 0 || bounds.Height != 12 {
		t.Fatalf("text bounds = %+v, want positive width and 12px height", bounds)
	}

	for i := 0; i < 20; i++ {
		requireGlyph(t, atlas, FontRoleLabel, 'M')
		requireGlyph(t, atlas, FontRoleHUD, '_')
	}
	if got := atlas.RasterizationCount(); got != initialRasterizations {
		t.Fatalf("rasterizations after repeated lookup = %d, want cached count %d", got, initialRasterizations)
	}
}

func requireGlyph(t *testing.T, atlas *FontAtlas, role FontRole, r rune) GlyphBitmap {
	t.Helper()
	glyph, ok := atlas.Glyph(role, r)
	if !ok {
		t.Fatalf("glyph %q for role %d missing", r, role)
	}
	if err := glyph.Validate(); err != nil {
		t.Fatalf("glyph %q for role %d invalid: %v", r, role, err)
	}
	return glyph
}

func glyphHasInk(glyph GlyphBitmap) bool {
	for _, pixel := range glyph.Pixels[:glyph.Width*glyph.Height] {
		if pixel>>24 != 0 {
			return true
		}
	}
	return false
}

func assertPixel(t *testing.T, buffer ARGBBuffer, x, y int, want uint32) {
	t.Helper()
	got := buffer.Pixels[y*buffer.Stride+x]
	if got != want {
		t.Fatalf("pixel %d,%d = %#08x, want %#08x", x, y, got, want)
	}
}
