package main

import (
	"fmt"
	"math"
	"strings"
	"unicode"
)

const (
	rendererLabelCharacters = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	rendererHUDCharacters   = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_ -:./"
	rendererFontStrategy    = "bundled pre-baked 5x7 bitmap atlas, scaled and cached in pure Go"
)

var (
	rendererGridCoreColor  = StraightARGB(190, 74, 181, 255)
	rendererGridHaloColor  = StraightARGB(140, 3, 22, 62)
	rendererLabelTextColor = StraightARGB(235, 157, 255, 190)
	rendererLabelHaloColor = StraightARGB(185, 1, 31, 40)
)

type RendererStyle struct {
	GridOpacity   float64
	GridLineWidth int
	LabelFontSize int
	HUDFontSize   int
}

func RendererStyleFromAppearance(appearance AppearanceConfig) RendererStyle {
	return RendererStyle{
		GridOpacity:   appearance.GridOpacity,
		GridLineWidth: appearance.GridLineWidth,
		LabelFontSize: appearance.LabelFontSize,
		HUDFontSize:   deriveHUDFontSize(appearance.LabelFontSize),
	}
}

func deriveHUDFontSize(labelFontSize int) int {
	if labelFontSize < 1 {
		return 1
	}
	size := int(math.Round(float64(labelFontSize) * 1.25))
	if size <= labelFontSize {
		size = labelFontSize + 1
	}
	return size
}

func (s RendererStyle) Validate() error {
	if s.GridOpacity < 0 || s.GridOpacity > 1 {
		return fmt.Errorf("renderer grid opacity must be between 0 and 1, got %g", s.GridOpacity)
	}
	if s.GridLineWidth < 1 {
		return fmt.Errorf("renderer grid line width must be positive, got %d", s.GridLineWidth)
	}
	if s.LabelFontSize < 1 {
		return fmt.Errorf("renderer label font size must be positive, got %d", s.LabelFontSize)
	}
	if s.HUDFontSize < 1 {
		return fmt.Errorf("renderer HUD font size must be positive, got %d", s.HUDFontSize)
	}
	return nil
}

type SoftwareRenderer struct {
	style      RendererStyle
	labelAtlas *bitmapGlyphAtlas
	hudAtlas   *bitmapGlyphAtlas
}

type RendererGlyphAtlasStats struct {
	Strategy         string `json:"strategy"`
	LabelSize        int    `json:"label_size"`
	HUDSize          int    `json:"hud_size"`
	LabelGlyphs      int    `json:"label_glyphs"`
	HUDGlyphs        int    `json:"hud_glyphs"`
	GlyphBuilds      int    `json:"glyph_builds"`
	RequiredGlyphSet string `json:"required_glyph_set"`
}

func NewSoftwareRenderer(appearance AppearanceConfig) (*SoftwareRenderer, error) {
	return NewSoftwareRendererWithStyle(RendererStyleFromAppearance(appearance))
}

func NewSoftwareRendererWithStyle(style RendererStyle) (*SoftwareRenderer, error) {
	if err := style.Validate(); err != nil {
		return nil, err
	}
	chars := rendererAtlasCharacters()
	labelAtlas, err := newBitmapGlyphAtlas(style.LabelFontSize, chars)
	if err != nil {
		return nil, fmt.Errorf("build label glyph atlas: %w", err)
	}
	hudAtlas, err := newBitmapGlyphAtlas(style.HUDFontSize, chars)
	if err != nil {
		return nil, fmt.Errorf("build HUD glyph atlas: %w", err)
	}
	return &SoftwareRenderer{
		style:      style,
		labelAtlas: labelAtlas,
		hudAtlas:   hudAtlas,
	}, nil
}

func RendererFontStrategy() string {
	return rendererFontStrategy
}

func rendererAtlasCharacters() string {
	return uniqueRunes(rendererLabelCharacters + rendererHUDCharacters)
}

func uniqueRunes(s string) string {
	seen := make(map[rune]bool, len(s))
	var b strings.Builder
	for _, r := range s {
		if seen[r] {
			continue
		}
		seen[r] = true
		b.WriteRune(r)
	}
	return b.String()
}

func (r *SoftwareRenderer) GlyphAtlasStats() RendererGlyphAtlasStats {
	if r == nil {
		return RendererGlyphAtlasStats{}
	}
	return RendererGlyphAtlasStats{
		Strategy:         rendererFontStrategy,
		LabelSize:        r.style.LabelFontSize,
		HUDSize:          r.style.HUDFontSize,
		LabelGlyphs:      r.labelAtlas.len(),
		HUDGlyphs:        r.hudAtlas.len(),
		GlyphBuilds:      r.glyphBuildCount(),
		RequiredGlyphSet: rendererAtlasCharacters(),
	}
}

func (r *SoftwareRenderer) GlyphAtlasCovers(chars string) bool {
	if r == nil {
		return false
	}
	return r.labelAtlas.covers(chars) && r.hudAtlas.covers(chars)
}

func (r *SoftwareRenderer) glyphBuildCount() int {
	if r == nil {
		return 0
	}
	return r.labelAtlas.buildCount + r.hudAtlas.buildCount
}

func (r *SoftwareRenderer) RenderMainGrid(monitor Monitor, gridSize int) (ARGBSnapshot, error) {
	grid, err := NewGridGeometry(monitor, gridSize)
	if err != nil {
		return ARGBSnapshot{}, err
	}
	return r.RenderGrid(grid)
}

func (r *SoftwareRenderer) RenderGrid(grid GridGeometry) (ARGBSnapshot, error) {
	if r == nil {
		return ARGBSnapshot{}, fmt.Errorf("software renderer is nil")
	}
	if grid.Size < 1 || grid.Size > len(rendererLabelCharacters) {
		return ARGBSnapshot{}, fmt.Errorf("grid size must be between 1 and %d for label atlas, got %d", len(rendererLabelCharacters), grid.Size)
	}
	if len(grid.Columns) != grid.Size || len(grid.Rows) != grid.Size {
		return ARGBSnapshot{}, fmt.Errorf("grid geometry has %d columns/%d rows, want %d", len(grid.Columns), len(grid.Rows), grid.Size)
	}
	canvas, err := newARGBCanvas(grid.Monitor.LogicalWidth, grid.Monitor.LogicalHeight)
	if err != nil {
		return ARGBSnapshot{}, err
	}
	r.drawGridLines(canvas, grid)
	r.drawEdgeLabels(canvas, grid)
	return canvas.snapshot(), nil
}

func (r *SoftwareRenderer) RenderHUD(width, height int, text string) (ARGBSnapshot, error) {
	if r == nil {
		return ARGBSnapshot{}, fmt.Errorf("software renderer is nil")
	}
	canvas, err := newARGBCanvas(width, height)
	if err != nil {
		return ARGBSnapshot{}, err
	}
	text = strings.Map(func(ch rune) rune {
		return unicode.ToUpper(ch)
	}, text)
	clip := Rect{X: 0, Y: 0, Width: width, Height: height}
	textWidth, textHeight, err := r.textSize(r.hudAtlas, text)
	if err != nil {
		return ARGBSnapshot{}, err
	}
	margin := maxInt(2, r.style.HUDFontSize/2)
	x := (width - textWidth) / 2
	y := height - textHeight - margin
	if y < 0 {
		y = 0
	}
	r.drawText(canvas, r.hudAtlas, text, x, y, clip, r.hudHaloRadius())
	return canvas.snapshot(), nil
}

func (r *SoftwareRenderer) drawGridLines(canvas *argbCanvas, grid GridGeometry) {
	haloWidth := r.gridHaloWidth()
	coreWidth := r.style.GridLineWidth
	halo := scalePixelAlpha(rendererGridHaloColor, r.style.GridOpacity)
	core := scalePixelAlpha(rendererGridCoreColor, r.style.GridOpacity)

	for i := 0; i <= grid.Size; i++ {
		x := 0
		if i > 0 {
			x = grid.Columns[i-1].End
		}
		r.drawVerticalStroke(canvas, x, haloWidth, halo)
		r.drawVerticalStroke(canvas, x, coreWidth, core)
	}
	for i := 0; i <= grid.Size; i++ {
		y := 0
		if i > 0 {
			y = grid.Rows[i-1].End
		}
		r.drawHorizontalStroke(canvas, y, haloWidth, halo)
		r.drawHorizontalStroke(canvas, y, coreWidth, core)
	}
}

func (r *SoftwareRenderer) drawVerticalStroke(canvas *argbCanvas, center, width int, color ARGBPixel) {
	start, end := strokeRange(center, width, canvas.width)
	canvas.fillRect(start, 0, end, canvas.height, color)
}

func (r *SoftwareRenderer) drawHorizontalStroke(canvas *argbCanvas, center, width int, color ARGBPixel) {
	start, end := strokeRange(center, width, canvas.height)
	canvas.fillRect(0, start, canvas.width, end, color)
}

func (r *SoftwareRenderer) drawEdgeLabels(canvas *argbCanvas, grid GridGeometry) {
	topRow := grid.Rows[0]
	bottomRow := grid.Rows[grid.Size-1]
	leftColumn := grid.Columns[0]
	rightColumn := grid.Columns[grid.Size-1]
	radius := r.labelHaloRadius()

	for i := 0; i < grid.Size; i++ {
		ch := rune(rendererLabelCharacters[i])
		column := grid.Columns[i]
		row := grid.Rows[i]

		r.drawGlyphCentered(canvas, r.labelAtlas, ch, Rect{
			X: column.Start, Y: topRow.Start, Width: column.Size(), Height: topRow.Size(),
		}, radius)
		r.drawGlyphCentered(canvas, r.labelAtlas, ch, Rect{
			X: column.Start, Y: bottomRow.Start, Width: column.Size(), Height: bottomRow.Size(),
		}, radius)
		r.drawGlyphCentered(canvas, r.labelAtlas, ch, Rect{
			X: leftColumn.Start, Y: row.Start, Width: leftColumn.Size(), Height: row.Size(),
		}, radius)
		r.drawGlyphCentered(canvas, r.labelAtlas, ch, Rect{
			X: rightColumn.Start, Y: row.Start, Width: rightColumn.Size(), Height: row.Size(),
		}, radius)
	}
}

func (r *SoftwareRenderer) drawGlyphCentered(canvas *argbCanvas, atlas *bitmapGlyphAtlas, ch rune, clip Rect, haloRadius int) {
	glyph, ok := atlas.glyph(ch)
	if !ok || clip.Width <= 0 || clip.Height <= 0 {
		return
	}
	x := clip.X + (clip.Width-glyph.width)/2
	y := clip.Y + (clip.Height-glyph.height)/2
	r.drawGlyph(canvas, glyph, x, y, clip, haloRadius)
}

func (r *SoftwareRenderer) drawText(canvas *argbCanvas, atlas *bitmapGlyphAtlas, text string, x, y int, clip Rect, haloRadius int) {
	cursor := x
	for _, ch := range text {
		glyph, ok := atlas.glyph(ch)
		if !ok {
			continue
		}
		r.drawGlyph(canvas, glyph, cursor, y, clip, haloRadius)
		cursor += glyph.width + atlas.spacing()
	}
}

func (r *SoftwareRenderer) drawGlyph(canvas *argbCanvas, glyph bitmapGlyph, x, y int, clip Rect, haloRadius int) {
	for dy := -haloRadius; dy <= haloRadius; dy++ {
		for dx := -haloRadius; dx <= haloRadius; dx++ {
			if dx == 0 && dy == 0 {
				continue
			}
			canvas.drawGlyphMask(glyph, x+dx, y+dy, clip, rendererLabelHaloColor)
		}
	}
	canvas.drawGlyphMask(glyph, x, y, clip, rendererLabelTextColor)
}

func (r *SoftwareRenderer) textSize(atlas *bitmapGlyphAtlas, text string) (int, int, error) {
	if text == "" {
		return 0, 0, nil
	}
	width := 0
	height := 0
	for _, ch := range text {
		glyph, ok := atlas.glyph(ch)
		if !ok {
			return 0, 0, fmt.Errorf("glyph atlas does not cover %q", ch)
		}
		if width > 0 {
			width += atlas.spacing()
		}
		width += glyph.width
		height = maxInt(height, glyph.height)
	}
	return width, height, nil
}

func (r *SoftwareRenderer) gridHaloWidth() int {
	return r.style.GridLineWidth + 2*maxInt(1, r.style.GridLineWidth)
}

func (r *SoftwareRenderer) labelHaloRadius() int {
	return maxInt(1, r.style.LabelFontSize/7)
}

func (r *SoftwareRenderer) hudHaloRadius() int {
	return maxInt(1, r.style.HUDFontSize/7)
}

func strokeRange(center, width, limit int) (int, int) {
	if limit <= 0 {
		return 0, 0
	}
	width = maxInt(1, minInt(width, limit))
	start := center - width/2
	if center <= 0 {
		start = 0
	}
	if center >= limit {
		start = limit - width
	}
	end := start + width
	if start < 0 {
		start = 0
	}
	if end > limit {
		end = limit
	}
	return start, end
}

func scalePixelAlpha(pixel ARGBPixel, scale float64) ARGBPixel {
	if scale <= 0 || pixel.A() == 0 {
		return StraightARGB(0, pixel.R(), pixel.G(), pixel.B())
	}
	if scale > 1 {
		scale = 1
	}
	alpha := uint8(math.Round(float64(pixel.A()) * scale))
	return StraightARGB(alpha, pixel.R(), pixel.G(), pixel.B())
}

type argbCanvas struct {
	width  int
	height int
	pixels []ARGBPixel
}

func newARGBCanvas(width, height int) (*argbCanvas, error) {
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("renderer canvas dimensions must be positive, got %dx%d", width, height)
	}
	return &argbCanvas{
		width:  width,
		height: height,
		pixels: make([]ARGBPixel, width*height),
	}, nil
}

func (c *argbCanvas) fillRect(x0, y0, x1, y1 int, color ARGBPixel) {
	if color.A() == 0 {
		return
	}
	x0 = maxInt(0, minInt(x0, c.width))
	x1 = maxInt(0, minInt(x1, c.width))
	y0 = maxInt(0, minInt(y0, c.height))
	y1 = maxInt(0, minInt(y1, c.height))
	if x0 >= x1 || y0 >= y1 {
		return
	}
	for y := y0; y < y1; y++ {
		row := y * c.width
		for x := x0; x < x1; x++ {
			i := row + x
			c.pixels[i] = AlphaOverStraightARGB(color, c.pixels[i])
		}
	}
}

func (c *argbCanvas) drawGlyphMask(glyph bitmapGlyph, x, y int, clip Rect, color ARGBPixel) {
	if color.A() == 0 {
		return
	}
	clipX0 := maxInt(0, clip.X)
	clipY0 := maxInt(0, clip.Y)
	clipX1 := minInt(c.width, clip.X+clip.Width)
	clipY1 := minInt(c.height, clip.Y+clip.Height)
	if clipX0 >= clipX1 || clipY0 >= clipY1 {
		return
	}
	for gy := 0; gy < glyph.height; gy++ {
		py := y + gy
		if py < clipY0 || py >= clipY1 {
			continue
		}
		for gx := 0; gx < glyph.width; gx++ {
			if glyph.mask[gy*glyph.width+gx] == 0 {
				continue
			}
			px := x + gx
			if px < clipX0 || px >= clipX1 {
				continue
			}
			i := py*c.width + px
			c.pixels[i] = AlphaOverStraightARGB(color, c.pixels[i])
		}
	}
}

func (c *argbCanvas) snapshot() ARGBSnapshot {
	pixels := make([]ARGBPixel, len(c.pixels))
	copy(pixels, c.pixels)
	return ARGBSnapshot{Width: c.width, Height: c.height, Pixels: pixels}
}

type bitmapGlyphAtlas struct {
	size       int
	glyphs     map[rune]bitmapGlyph
	buildCount int
}

type bitmapGlyph struct {
	ch     rune
	width  int
	height int
	mask   []uint8
}

func newBitmapGlyphAtlas(size int, chars string) (*bitmapGlyphAtlas, error) {
	if size < 1 {
		return nil, fmt.Errorf("glyph atlas size must be positive, got %d", size)
	}
	atlas := &bitmapGlyphAtlas{
		size:   size,
		glyphs: make(map[rune]bitmapGlyph),
	}
	for _, ch := range chars {
		if _, exists := atlas.glyphs[ch]; exists {
			continue
		}
		pattern, ok := bakedGlyphPatterns[ch]
		if !ok {
			return nil, fmt.Errorf("missing baked bitmap glyph for %q", ch)
		}
		glyph, err := scaleBakedGlyph(ch, pattern, size)
		if err != nil {
			return nil, err
		}
		atlas.glyphs[ch] = glyph
		atlas.buildCount++
	}
	return atlas, nil
}

func (a *bitmapGlyphAtlas) glyph(ch rune) (bitmapGlyph, bool) {
	if a == nil {
		return bitmapGlyph{}, false
	}
	glyph, ok := a.glyphs[unicode.ToUpper(ch)]
	return glyph, ok
}

func (a *bitmapGlyphAtlas) covers(chars string) bool {
	if a == nil {
		return false
	}
	for _, ch := range chars {
		if _, ok := a.glyph(ch); !ok {
			return false
		}
	}
	return true
}

func (a *bitmapGlyphAtlas) spacing() int {
	if a == nil {
		return 0
	}
	return maxInt(1, a.size/5)
}

func (a *bitmapGlyphAtlas) len() int {
	if a == nil {
		return 0
	}
	return len(a.glyphs)
}

func scaleBakedGlyph(ch rune, pattern []string, size int) (bitmapGlyph, error) {
	if len(pattern) == 0 {
		return bitmapGlyph{}, fmt.Errorf("empty baked bitmap glyph for %q", ch)
	}
	sourceHeight := len(pattern)
	sourceWidth := len(pattern[0])
	if sourceWidth == 0 {
		return bitmapGlyph{}, fmt.Errorf("empty baked bitmap glyph row for %q", ch)
	}
	for _, row := range pattern {
		if len(row) != sourceWidth {
			return bitmapGlyph{}, fmt.Errorf("non-rectangular baked bitmap glyph for %q", ch)
		}
	}

	width := maxInt(1, int(math.Round(float64(sourceWidth)*float64(size)/float64(sourceHeight))))
	mask := make([]uint8, width*size)
	for y := 0; y < size; y++ {
		srcY := y * sourceHeight / size
		for x := 0; x < width; x++ {
			srcX := x * sourceWidth / width
			switch pattern[srcY][srcX] {
			case '1', '#', 'X':
				mask[y*width+x] = 255
			}
		}
	}
	return bitmapGlyph{ch: ch, width: width, height: size, mask: mask}, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

var bakedGlyphPatterns = map[rune][]string{
	' ': {
		"000",
		"000",
		"000",
		"000",
		"000",
		"000",
		"000",
	},
	'-': {
		"00000",
		"00000",
		"00000",
		"11110",
		"00000",
		"00000",
		"00000",
	},
	'.': {
		"000",
		"000",
		"000",
		"000",
		"000",
		"110",
		"110",
	},
	'/': {
		"00001",
		"00010",
		"00010",
		"00100",
		"01000",
		"01000",
		"10000",
	},
	':': {
		"000",
		"110",
		"110",
		"000",
		"110",
		"110",
		"000",
	},
	'_': {
		"00000",
		"00000",
		"00000",
		"00000",
		"00000",
		"00000",
		"11111",
	},
	'0': {
		"01110",
		"10001",
		"10011",
		"10101",
		"11001",
		"10001",
		"01110",
	},
	'1': {
		"00100",
		"01100",
		"00100",
		"00100",
		"00100",
		"00100",
		"01110",
	},
	'2': {
		"01110",
		"10001",
		"00001",
		"00010",
		"00100",
		"01000",
		"11111",
	},
	'3': {
		"11110",
		"00001",
		"00001",
		"01110",
		"00001",
		"00001",
		"11110",
	},
	'4': {
		"00010",
		"00110",
		"01010",
		"10010",
		"11111",
		"00010",
		"00010",
	},
	'5': {
		"11111",
		"10000",
		"10000",
		"11110",
		"00001",
		"00001",
		"11110",
	},
	'6': {
		"00110",
		"01000",
		"10000",
		"11110",
		"10001",
		"10001",
		"01110",
	},
	'7': {
		"11111",
		"00001",
		"00010",
		"00100",
		"01000",
		"01000",
		"01000",
	},
	'8': {
		"01110",
		"10001",
		"10001",
		"01110",
		"10001",
		"10001",
		"01110",
	},
	'9': {
		"01110",
		"10001",
		"10001",
		"01111",
		"00001",
		"00010",
		"11100",
	},
	'A': {
		"01110",
		"10001",
		"10001",
		"11111",
		"10001",
		"10001",
		"10001",
	},
	'B': {
		"11110",
		"10001",
		"10001",
		"11110",
		"10001",
		"10001",
		"11110",
	},
	'C': {
		"01111",
		"10000",
		"10000",
		"10000",
		"10000",
		"10000",
		"01111",
	},
	'D': {
		"11110",
		"10001",
		"10001",
		"10001",
		"10001",
		"10001",
		"11110",
	},
	'E': {
		"11111",
		"10000",
		"10000",
		"11110",
		"10000",
		"10000",
		"11111",
	},
	'F': {
		"11111",
		"10000",
		"10000",
		"11110",
		"10000",
		"10000",
		"10000",
	},
	'G': {
		"01111",
		"10000",
		"10000",
		"10011",
		"10001",
		"10001",
		"01111",
	},
	'H': {
		"10001",
		"10001",
		"10001",
		"11111",
		"10001",
		"10001",
		"10001",
	},
	'I': {
		"11111",
		"00100",
		"00100",
		"00100",
		"00100",
		"00100",
		"11111",
	},
	'J': {
		"00111",
		"00010",
		"00010",
		"00010",
		"00010",
		"10010",
		"01100",
	},
	'K': {
		"10001",
		"10010",
		"10100",
		"11000",
		"10100",
		"10010",
		"10001",
	},
	'L': {
		"10000",
		"10000",
		"10000",
		"10000",
		"10000",
		"10000",
		"11111",
	},
	'M': {
		"10001",
		"11011",
		"10101",
		"10101",
		"10001",
		"10001",
		"10001",
	},
	'N': {
		"10001",
		"11001",
		"10101",
		"10011",
		"10001",
		"10001",
		"10001",
	},
	'O': {
		"01110",
		"10001",
		"10001",
		"10001",
		"10001",
		"10001",
		"01110",
	},
	'P': {
		"11110",
		"10001",
		"10001",
		"11110",
		"10000",
		"10000",
		"10000",
	},
	'Q': {
		"01110",
		"10001",
		"10001",
		"10001",
		"10101",
		"10010",
		"01101",
	},
	'R': {
		"11110",
		"10001",
		"10001",
		"11110",
		"10100",
		"10010",
		"10001",
	},
	'S': {
		"01111",
		"10000",
		"10000",
		"01110",
		"00001",
		"00001",
		"11110",
	},
	'T': {
		"11111",
		"00100",
		"00100",
		"00100",
		"00100",
		"00100",
		"00100",
	},
	'U': {
		"10001",
		"10001",
		"10001",
		"10001",
		"10001",
		"10001",
		"01110",
	},
	'V': {
		"10001",
		"10001",
		"10001",
		"10001",
		"10001",
		"01010",
		"00100",
	},
	'W': {
		"10001",
		"10001",
		"10001",
		"10101",
		"10101",
		"10101",
		"01010",
	},
	'X': {
		"10001",
		"10001",
		"01010",
		"00100",
		"01010",
		"10001",
		"10001",
	},
	'Y': {
		"10001",
		"10001",
		"01010",
		"00100",
		"00100",
		"00100",
		"00100",
	},
	'Z': {
		"11111",
		"00001",
		"00010",
		"00100",
		"01000",
		"10000",
		"11111",
	},
}
