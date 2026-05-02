package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
)

const fontAtlasCharset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 _-.:,/\\+()[]!?\"'"

type FontRole int

const (
	FontRoleLabel FontRole = iota
	FontRoleHUD
)

type FontAtlasOptions struct {
	LabelFontSize int
	HUDFontSize   int
}

type FontAtlas struct {
	labelSize      int
	hudSize        int
	labelGlyphs    map[rune]GlyphBitmap
	hudGlyphs      map[rune]GlyphBitmap
	rasterizations int
}

type GlyphBitmap struct {
	Rune     rune
	Width    int
	Height   int
	Advance  int
	Baseline int
	Pixels   []uint32
}

func NewFontAtlasFromConfig(config Config) (*FontAtlas, error) {
	return NewFontAtlas(FontAtlasOptions{LabelFontSize: config.Appearance.LabelFontSize})
}

func NewFontAtlas(options FontAtlasOptions) (*FontAtlas, error) {
	if options.LabelFontSize <= 0 {
		return nil, fmt.Errorf("label font size must be positive, got %d", options.LabelFontSize)
	}
	if options.HUDFontSize <= 0 {
		options.HUDFontSize = DefaultHUDFontSize(options.LabelFontSize)
	}
	if options.HUDFontSize <= 0 {
		return nil, fmt.Errorf("HUD font size must be positive, got %d", options.HUDFontSize)
	}

	atlas := &FontAtlas{
		labelSize:   options.LabelFontSize,
		hudSize:     options.HUDFontSize,
		labelGlyphs: make(map[rune]GlyphBitmap, len(fontAtlasCharset)),
		hudGlyphs:   make(map[rune]GlyphBitmap, len(fontAtlasCharset)),
	}

	var err error
	atlas.labelGlyphs, err = atlas.rasterizeGlyphSet(options.LabelFontSize)
	if err != nil {
		return nil, err
	}
	atlas.hudGlyphs, err = atlas.rasterizeGlyphSet(options.HUDFontSize)
	if err != nil {
		return nil, err
	}
	return atlas, nil
}

func DefaultHUDFontSize(labelFontSize int) int {
	if labelFontSize <= 0 {
		return 0
	}
	size := labelFontSize + labelFontSize/3
	if size <= labelFontSize {
		size = labelFontSize + 1
	}
	return size
}

func (a *FontAtlas) LabelFontSize() int {
	if a == nil {
		return 0
	}
	return a.labelSize
}

func (a *FontAtlas) HUDFontSize() int {
	if a == nil {
		return 0
	}
	return a.hudSize
}

func (a *FontAtlas) GlyphCount(role FontRole) int {
	if a == nil {
		return 0
	}
	switch role {
	case FontRoleLabel:
		return len(a.labelGlyphs)
	case FontRoleHUD:
		return len(a.hudGlyphs)
	default:
		return 0
	}
}

func (a *FontAtlas) RasterizationCount() int {
	if a == nil {
		return 0
	}
	return a.rasterizations
}

func (a *FontAtlas) LabelGlyph(r rune) (GlyphBitmap, bool) {
	return a.Glyph(FontRoleLabel, r)
}

func (a *FontAtlas) HUDGlyph(r rune) (GlyphBitmap, bool) {
	return a.Glyph(FontRoleHUD, r)
}

func (a *FontAtlas) Glyph(role FontRole, r rune) (GlyphBitmap, bool) {
	if a == nil {
		return GlyphBitmap{}, false
	}
	r = normalizeAtlasRune(r)

	var glyphs map[rune]GlyphBitmap
	switch role {
	case FontRoleLabel:
		glyphs = a.labelGlyphs
	case FontRoleHUD:
		glyphs = a.hudGlyphs
	default:
		return GlyphBitmap{}, false
	}

	glyph, ok := glyphs[r]
	if ok {
		return glyph, true
	}
	return GlyphBitmap{}, false
}

func (a *FontAtlas) TextSize(role FontRole, text string) (int, int, error) {
	if a == nil {
		return 0, 0, fmt.Errorf("font atlas is nil")
	}
	width := 0
	height := 0
	for _, r := range text {
		glyph, ok := a.Glyph(role, r)
		if !ok {
			return 0, 0, fmt.Errorf("glyph %q is not available", r)
		}
		width += glyph.Advance
		if glyph.Height > height {
			height = glyph.Height
		}
	}
	return width, height, nil
}

func CompositeText(dst ARGBBuffer, atlas *FontAtlas, role FontRole, text string, x, y int, color uint32) (Rect, error) {
	if atlas == nil {
		return Rect{}, fmt.Errorf("font atlas is nil")
	}
	if err := dst.Validate(); err != nil {
		return Rect{}, err
	}

	cursorX := x
	bounds := Rect{X: x, Y: y}
	for _, r := range text {
		glyph, ok := atlas.Glyph(role, r)
		if !ok {
			return Rect{}, fmt.Errorf("glyph %q is not available", r)
		}
		if err := CompositeGlyph(dst, glyph, cursorX, y, color); err != nil {
			return Rect{}, err
		}
		cursorX += glyph.Advance
		if glyph.Height > bounds.Height {
			bounds.Height = glyph.Height
		}
	}
	bounds.Width = cursorX - x
	return bounds, nil
}

func CompositeGlyph(dst ARGBBuffer, glyph GlyphBitmap, x, y int, color uint32) error {
	if err := dst.Validate(); err != nil {
		return err
	}
	if err := glyph.Validate(); err != nil {
		return err
	}

	colorAlpha := int((color >> 24) & 0xff)
	if colorAlpha == 0 {
		return nil
	}

	colorRGB := color & 0x00ffffff
	for gy := 0; gy < glyph.Height; gy++ {
		dy := y + gy
		if dy < 0 || dy >= dst.Height {
			continue
		}
		for gx := 0; gx < glyph.Width; gx++ {
			dx := x + gx
			if dx < 0 || dx >= dst.Width {
				continue
			}

			coverage := int((glyph.Pixels[gy*glyph.Width+gx] >> 24) & 0xff)
			if coverage == 0 {
				continue
			}
			sourceAlpha := div255(colorAlpha * coverage)
			if sourceAlpha == 0 {
				continue
			}

			source := uint32(sourceAlpha<<24) | colorRGB
			offset := dy*dst.Stride + dx
			dst.Pixels[offset] = BlendARGB(dst.Pixels[offset], source)
		}
	}
	return nil
}

func BlendARGB(dst, src uint32) uint32 {
	sa := int((src >> 24) & 0xff)
	if sa == 0 {
		return dst
	}
	if sa == 255 {
		return src
	}

	da := int((dst >> 24) & 0xff)
	sr := int((src >> 16) & 0xff)
	sg := int((src >> 8) & 0xff)
	sb := int(src & 0xff)
	dr := int((dst >> 16) & 0xff)
	dg := int((dst >> 8) & 0xff)
	db := int(dst & 0xff)

	outA := sa + div255(da*(255-sa))
	if outA == 0 {
		return 0
	}

	outR := blendStraightChannel(sr, dr, sa, da, outA)
	outG := blendStraightChannel(sg, dg, sa, da, outA)
	outB := blendStraightChannel(sb, db, sa, da, outA)
	return uint32(outA<<24 | outR<<16 | outG<<8 | outB)
}

func (g GlyphBitmap) Validate() error {
	if g.Width <= 0 || g.Height <= 0 || g.Advance <= 0 {
		return fmt.Errorf("invalid glyph %q geometry %dx%d advance %d", g.Rune, g.Width, g.Height, g.Advance)
	}
	if len(g.Pixels) < g.Width*g.Height {
		return fmt.Errorf("glyph %q pixel slice is shorter than width*height", g.Rune)
	}
	return nil
}

func GlyphSnapshot(g GlyphBitmap) ([]byte, error) {
	if err := g.Validate(); err != nil {
		return nil, err
	}
	pixelCount := g.Width * g.Height
	out := make([]byte, 24+(pixelCount*4))
	binary.BigEndian.PutUint32(out[0:4], uint32(g.Rune))
	binary.BigEndian.PutUint32(out[4:8], uint32(g.Width))
	binary.BigEndian.PutUint32(out[8:12], uint32(g.Height))
	binary.BigEndian.PutUint32(out[12:16], uint32(g.Advance))
	binary.BigEndian.PutUint32(out[16:20], uint32(g.Baseline))
	binary.BigEndian.PutUint32(out[20:24], uint32(pixelCount))

	offset := 24
	for _, pixel := range g.Pixels[:pixelCount] {
		binary.BigEndian.PutUint32(out[offset:offset+4], pixel)
		offset += 4
	}
	return out, nil
}

func GlyphHash(g GlyphBitmap) (string, error) {
	snapshot, err := GlyphSnapshot(g)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(snapshot)
	return hex.EncodeToString(sum[:]), nil
}

func (a *FontAtlas) rasterizeGlyphSet(fontSize int) (map[rune]GlyphBitmap, error) {
	glyphs := make(map[rune]GlyphBitmap, len(fontAtlasCharset))
	for _, r := range fontAtlasCharset {
		pattern, ok := builtinBitmapFont[r]
		if !ok {
			return nil, fmt.Errorf("pre-baked font is missing glyph %q", r)
		}
		glyph, err := rasterizePattern(r, pattern, fontSize)
		if err != nil {
			return nil, err
		}
		glyphs[r] = glyph
		a.rasterizations++
	}
	return glyphs, nil
}

func rasterizePattern(r rune, pattern []string, fontSize int) (GlyphBitmap, error) {
	if fontSize <= 0 {
		return GlyphBitmap{}, fmt.Errorf("font size must be positive, got %d", fontSize)
	}
	if len(pattern) == 0 {
		return GlyphBitmap{}, fmt.Errorf("glyph %q has no pattern rows", r)
	}
	sourceHeight := len(pattern)
	sourceWidth := len(pattern[0])
	if sourceWidth == 0 {
		return GlyphBitmap{}, fmt.Errorf("glyph %q has empty pattern width", r)
	}
	for _, row := range pattern {
		if len(row) != sourceWidth {
			return GlyphBitmap{}, fmt.Errorf("glyph %q has inconsistent pattern widths", r)
		}
	}

	width := ceilDiv(sourceWidth*fontSize, sourceHeight)
	if width < 1 {
		width = 1
	}
	gap := fontSize / sourceHeight
	if gap < 1 {
		gap = 1
	}

	pixels := make([]uint32, width*fontSize)
	for y := 0; y < fontSize; y++ {
		sourceY := y * sourceHeight / fontSize
		for x := 0; x < width; x++ {
			sourceX := x * sourceWidth / width
			if pattern[sourceY][sourceX] != '.' && pattern[sourceY][sourceX] != ' ' {
				pixels[y*width+x] = 0xffffffff
			}
		}
	}

	return GlyphBitmap{
		Rune:     r,
		Width:    width,
		Height:   fontSize,
		Advance:  width + gap,
		Baseline: fontSize,
		Pixels:   pixels,
	}, nil
}

func normalizeAtlasRune(r rune) rune {
	if r >= 'a' && r <= 'z' {
		return r - ('a' - 'A')
	}
	return r
}

func blendStraightChannel(src, dst, srcA, dstA, outA int) int {
	dstFactor := div255(dstA * (255 - srcA))
	return (src*srcA + dst*dstFactor + outA/2) / outA
}

func div255(value int) int {
	return (value + 127) / 255
}

func ceilDiv(value, divisor int) int {
	return (value + divisor - 1) / divisor
}

var builtinBitmapFont = map[rune][]string{
	'A': {
		".###.",
		"#...#",
		"#...#",
		"#####",
		"#...#",
		"#...#",
		"#...#",
	},
	'B': {
		"####.",
		"#...#",
		"#...#",
		"####.",
		"#...#",
		"#...#",
		"####.",
	},
	'C': {
		".####",
		"#....",
		"#....",
		"#....",
		"#....",
		"#....",
		".####",
	},
	'D': {
		"####.",
		"#...#",
		"#...#",
		"#...#",
		"#...#",
		"#...#",
		"####.",
	},
	'E': {
		"#####",
		"#....",
		"#....",
		"####.",
		"#....",
		"#....",
		"#####",
	},
	'F': {
		"#####",
		"#....",
		"#....",
		"####.",
		"#....",
		"#....",
		"#....",
	},
	'G': {
		".####",
		"#....",
		"#....",
		"#.###",
		"#...#",
		"#...#",
		".####",
	},
	'H': {
		"#...#",
		"#...#",
		"#...#",
		"#####",
		"#...#",
		"#...#",
		"#...#",
	},
	'I': {
		"#####",
		"..#..",
		"..#..",
		"..#..",
		"..#..",
		"..#..",
		"#####",
	},
	'J': {
		"..###",
		"...#.",
		"...#.",
		"...#.",
		"...#.",
		"#..#.",
		".##..",
	},
	'K': {
		"#...#",
		"#..#.",
		"#.#..",
		"##...",
		"#.#..",
		"#..#.",
		"#...#",
	},
	'L': {
		"#....",
		"#....",
		"#....",
		"#....",
		"#....",
		"#....",
		"#####",
	},
	'M': {
		"#...#",
		"##.##",
		"#.#.#",
		"#.#.#",
		"#...#",
		"#...#",
		"#...#",
	},
	'N': {
		"#...#",
		"##..#",
		"##..#",
		"#.#.#",
		"#..##",
		"#..##",
		"#...#",
	},
	'O': {
		".###.",
		"#...#",
		"#...#",
		"#...#",
		"#...#",
		"#...#",
		".###.",
	},
	'P': {
		"####.",
		"#...#",
		"#...#",
		"####.",
		"#....",
		"#....",
		"#....",
	},
	'Q': {
		".###.",
		"#...#",
		"#...#",
		"#...#",
		"#.#.#",
		"#..#.",
		".##.#",
	},
	'R': {
		"####.",
		"#...#",
		"#...#",
		"####.",
		"#.#..",
		"#..#.",
		"#...#",
	},
	'S': {
		".####",
		"#....",
		"#....",
		".###.",
		"....#",
		"....#",
		"####.",
	},
	'T': {
		"#####",
		"..#..",
		"..#..",
		"..#..",
		"..#..",
		"..#..",
		"..#..",
	},
	'U': {
		"#...#",
		"#...#",
		"#...#",
		"#...#",
		"#...#",
		"#...#",
		".###.",
	},
	'V': {
		"#...#",
		"#...#",
		"#...#",
		"#...#",
		"#...#",
		".#.#.",
		"..#..",
	},
	'W': {
		"#...#",
		"#...#",
		"#...#",
		"#.#.#",
		"#.#.#",
		"##.##",
		"#...#",
	},
	'X': {
		"#...#",
		"#...#",
		".#.#.",
		"..#..",
		".#.#.",
		"#...#",
		"#...#",
	},
	'Y': {
		"#...#",
		"#...#",
		".#.#.",
		"..#..",
		"..#..",
		"..#..",
		"..#..",
	},
	'Z': {
		"#####",
		"....#",
		"...#.",
		"..#..",
		".#...",
		"#....",
		"#####",
	},
	'0': {
		".###.",
		"#...#",
		"#..##",
		"#.#.#",
		"##..#",
		"#...#",
		".###.",
	},
	'1': {
		"..#..",
		".##..",
		"..#..",
		"..#..",
		"..#..",
		"..#..",
		".###.",
	},
	'2': {
		".###.",
		"#...#",
		"....#",
		"...#.",
		"..#..",
		".#...",
		"#####",
	},
	'3': {
		"####.",
		"....#",
		"....#",
		".###.",
		"....#",
		"....#",
		"####.",
	},
	'4': {
		"...#.",
		"..##.",
		".#.#.",
		"#..#.",
		"#####",
		"...#.",
		"...#.",
	},
	'5': {
		"#####",
		"#....",
		"#....",
		"####.",
		"....#",
		"....#",
		"####.",
	},
	'6': {
		".###.",
		"#....",
		"#....",
		"####.",
		"#...#",
		"#...#",
		".###.",
	},
	'7': {
		"#####",
		"....#",
		"...#.",
		"..#..",
		".#...",
		".#...",
		".#...",
	},
	'8': {
		".###.",
		"#...#",
		"#...#",
		".###.",
		"#...#",
		"#...#",
		".###.",
	},
	'9': {
		".###.",
		"#...#",
		"#...#",
		".####",
		"....#",
		"....#",
		".###.",
	},
	' ': {
		"...",
		"...",
		"...",
		"...",
		"...",
		"...",
		"...",
	},
	'_': {
		".....",
		".....",
		".....",
		".....",
		".....",
		".....",
		"#####",
	},
	'-': {
		".....",
		".....",
		".....",
		"#####",
		".....",
		".....",
		".....",
	},
	'.': {
		"...",
		"...",
		"...",
		"...",
		"...",
		"...",
		".#.",
	},
	':': {
		"...",
		".#.",
		".#.",
		"...",
		".#.",
		".#.",
		"...",
	},
	',': {
		"...",
		"...",
		"...",
		"...",
		"...",
		".#.",
		"#..",
	},
	'/': {
		"....#",
		"...#.",
		"...#.",
		"..#..",
		".#...",
		".#...",
		"#....",
	},
	'\\': {
		"#....",
		".#...",
		".#...",
		"..#..",
		"...#.",
		"...#.",
		"....#",
	},
	'+': {
		".....",
		"..#..",
		"..#..",
		"#####",
		"..#..",
		"..#..",
		".....",
	},
	'(': {
		".##",
		"#..",
		"#..",
		"#..",
		"#..",
		"#..",
		".##",
	},
	')': {
		"##.",
		"..#",
		"..#",
		"..#",
		"..#",
		"..#",
		"##.",
	},
	'[': {
		"###",
		"#..",
		"#..",
		"#..",
		"#..",
		"#..",
		"###",
	},
	']': {
		"###",
		"..#",
		"..#",
		"..#",
		"..#",
		"..#",
		"###",
	},
	'!': {
		"..#..",
		"..#..",
		"..#..",
		"..#..",
		"..#..",
		".....",
		"..#..",
	},
	'?': {
		"####.",
		"....#",
		"....#",
		"...#.",
		"..#..",
		".....",
		"..#..",
	},
	'"': {
		"#.#",
		"#.#",
		"...",
		"...",
		"...",
		"...",
		"...",
	},
	'\'': {
		".#.",
		".#.",
		"#..",
		"...",
		"...",
		"...",
		"...",
	},
}

func init() {
	for _, r := range fontAtlasCharset {
		if _, ok := builtinBitmapFont[r]; !ok {
			panic(fmt.Sprintf("font atlas charset references missing glyph %q", r))
		}
	}
	for r := range builtinBitmapFont {
		if strings.ContainsRune(fontAtlasCharset, r) {
			continue
		}
		panic(fmt.Sprintf("font atlas glyph %q is not in charset", r))
	}
}
