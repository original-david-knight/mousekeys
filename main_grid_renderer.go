package main

import (
	"fmt"
	"math"
)

const (
	mainGridLetters    = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	DefaultMainGridHUD = "__"
)

type MainGridRenderOptions struct {
	GridSize       int
	Appearance     AppearanceConfig
	FontAtlas      *FontAtlas
	HUD            string
	SelectedColumn *int
}

func RenderMainGridOverlay(buffer ARGBBuffer, options MainGridRenderOptions) error {
	if err := buffer.Validate(); err != nil {
		return err
	}

	options, err := normalizeMainGridRenderOptions(options)
	if err != nil {
		return err
	}

	clearARGBBuffer(buffer)
	drawMainGridColumnFocus(buffer, options)
	drawMainGridLines(buffer, options.GridSize, options.Appearance)
	if err := drawMainGridEdgeLabels(buffer, options); err != nil {
		return err
	}
	dimMainGridNonSelectedColumns(buffer, options)
	if err := drawMainGridHUD(buffer, options); err != nil {
		return err
	}
	return nil
}

func normalizeMainGridRenderOptions(options MainGridRenderOptions) (MainGridRenderOptions, error) {
	defaults := DefaultConfig()
	if options.GridSize == 0 {
		options.GridSize = defaults.Grid.Size
	}
	if options.Appearance == (AppearanceConfig{}) {
		options.Appearance = defaults.Appearance
		if options.FontAtlas != nil && options.FontAtlas.LabelFontSize() > 0 {
			options.Appearance.LabelFontSize = options.FontAtlas.LabelFontSize()
		}
	}

	if options.GridSize < 1 || options.GridSize > len(mainGridLetters) {
		return MainGridRenderOptions{}, fmt.Errorf("main grid size must be between 1 and %d, got %d", len(mainGridLetters), options.GridSize)
	}
	if options.Appearance.GridOpacity < 0 || options.Appearance.GridOpacity > 1 {
		return MainGridRenderOptions{}, fmt.Errorf("appearance.grid_opacity must be between 0 and 1, got %g", options.Appearance.GridOpacity)
	}
	if options.Appearance.GridLineWidth < 1 {
		return MainGridRenderOptions{}, fmt.Errorf("appearance.grid_line_width must be positive, got %d", options.Appearance.GridLineWidth)
	}
	if options.Appearance.LabelFontSize < 1 {
		return MainGridRenderOptions{}, fmt.Errorf("appearance.label_font_size must be positive, got %d", options.Appearance.LabelFontSize)
	}
	if options.SelectedColumn != nil && (*options.SelectedColumn < 0 || *options.SelectedColumn >= options.GridSize) {
		return MainGridRenderOptions{}, fmt.Errorf("selected main grid column out of range: col=%d size=%d", *options.SelectedColumn, options.GridSize)
	}

	if options.FontAtlas == nil {
		atlas, err := NewFontAtlas(FontAtlasOptions{LabelFontSize: options.Appearance.LabelFontSize})
		if err != nil {
			return MainGridRenderOptions{}, err
		}
		options.FontAtlas = atlas
	} else if options.FontAtlas.LabelFontSize() != options.Appearance.LabelFontSize {
		return MainGridRenderOptions{}, fmt.Errorf("font atlas label size %d does not match appearance.label_font_size %d", options.FontAtlas.LabelFontSize(), options.Appearance.LabelFontSize)
	}

	return options, nil
}

func drawMainGridColumnFocus(buffer ARGBBuffer, options MainGridRenderOptions) {
	if options.SelectedColumn == nil {
		return
	}

	selected := *options.SelectedColumn
	x0, x1, err := axisSegment(buffer.Width, options.GridSize, selected)
	if err != nil {
		return
	}
	blendRect(buffer, Rect{X: x0, Y: 0, Width: x1 - x0, Height: buffer.Height}, 0x303a7afe)
}

func dimMainGridNonSelectedColumns(buffer ARGBBuffer, options MainGridRenderOptions) {
	if options.SelectedColumn == nil {
		return
	}

	selected := *options.SelectedColumn
	for col := 0; col < options.GridSize; col++ {
		if col == selected {
			continue
		}
		x0, x1, err := axisSegment(buffer.Width, options.GridSize, col)
		if err != nil {
			return
		}
		blendRect(buffer, Rect{X: x0, Y: 0, Width: x1 - x0, Height: buffer.Height}, 0x70000000)
	}
}

func clearARGBBuffer(buffer ARGBBuffer) {
	for y := 0; y < buffer.Height; y++ {
		row := buffer.Pixels[y*buffer.Stride : y*buffer.Stride+buffer.Width]
		for x := range row {
			row[x] = 0
		}
	}
}

func drawMainGridLines(buffer ARGBBuffer, gridSize int, appearance AppearanceConfig) {
	alpha := opacityToAlpha(appearance.GridOpacity)
	if alpha == 0 {
		return
	}
	lineColor := uint32(alpha<<24) | 0x00ffffff
	lineWidth := appearance.GridLineWidth

	for col := 0; col <= gridSize; col++ {
		boundary := axisBoundary(buffer.Width, gridSize, col)
		start, end := lineSpanForBoundary(boundary, buffer.Width, lineWidth)
		overwriteRect(buffer, Rect{X: start, Y: 0, Width: end - start, Height: buffer.Height}, lineColor)
	}
	for row := 0; row <= gridSize; row++ {
		boundary := axisBoundary(buffer.Height, gridSize, row)
		start, end := lineSpanForBoundary(boundary, buffer.Height, lineWidth)
		overwriteRect(buffer, Rect{X: 0, Y: start, Width: buffer.Width, Height: end - start}, lineColor)
	}
}

func drawMainGridEdgeLabels(buffer ARGBBuffer, options MainGridRenderOptions) error {
	atlas := options.FontAtlas
	gridSize := options.GridSize
	pad := edgeLabelPadding(options.Appearance.LabelFontSize)
	labelColor := uint32(0xffffffff)

	topY0, topY1, err := axisSegment(buffer.Height, gridSize, 0)
	if err != nil {
		return err
	}
	bottomY0, bottomY1, err := axisSegment(buffer.Height, gridSize, gridSize-1)
	if err != nil {
		return err
	}
	leftX0, leftX1, err := axisSegment(buffer.Width, gridSize, 0)
	if err != nil {
		return err
	}
	rightX0, rightX1, err := axisSegment(buffer.Width, gridSize, gridSize-1)
	if err != nil {
		return err
	}

	for i := 0; i < gridSize; i++ {
		text := string(mainGridLetters[i])
		textWidth, textHeight, err := atlas.TextSize(FontRoleLabel, text)
		if err != nil {
			return err
		}

		colX0, colX1, err := axisSegment(buffer.Width, gridSize, i)
		if err != nil {
			return err
		}
		topCell := Rect{X: colX0, Y: topY0, Width: colX1 - colX0, Height: topY1 - topY0}
		bottomCell := Rect{X: colX0, Y: bottomY0, Width: colX1 - colX0, Height: bottomY1 - bottomY0}

		topX := centeredInSpan(colX0, colX1, textWidth)
		topY := edgeAlignedInSpan(topY0, topY1, textHeight, pad, false)
		if err := compositeTextClipped(buffer, atlas, FontRoleLabel, text, topX, topY, labelColor, topCell); err != nil {
			return err
		}

		bottomX := centeredInSpan(colX0, colX1, textWidth)
		bottomY := edgeAlignedInSpan(bottomY0, bottomY1, textHeight, pad, true)
		if err := compositeTextClipped(buffer, atlas, FontRoleLabel, text, bottomX, bottomY, labelColor, bottomCell); err != nil {
			return err
		}

		rowY0, rowY1, err := axisSegment(buffer.Height, gridSize, i)
		if err != nil {
			return err
		}
		leftCell := Rect{X: leftX0, Y: rowY0, Width: leftX1 - leftX0, Height: rowY1 - rowY0}
		rightCell := Rect{X: rightX0, Y: rowY0, Width: rightX1 - rightX0, Height: rowY1 - rowY0}

		leftX := edgeAlignedInSpan(leftX0, leftX1, textWidth, pad, false)
		leftY := centeredInSpan(rowY0, rowY1, textHeight)
		if err := compositeTextClipped(buffer, atlas, FontRoleLabel, text, leftX, leftY, labelColor, leftCell); err != nil {
			return err
		}

		rightX := edgeAlignedInSpan(rightX0, rightX1, textWidth, pad, true)
		rightY := centeredInSpan(rowY0, rowY1, textHeight)
		if err := compositeTextClipped(buffer, atlas, FontRoleLabel, text, rightX, rightY, labelColor, rightCell); err != nil {
			return err
		}
	}
	return nil
}

func drawMainGridHUD(buffer ARGBBuffer, options MainGridRenderOptions) error {
	if options.HUD == "" {
		return nil
	}

	atlas := options.FontAtlas
	textWidth, textHeight, err := atlas.TextSize(FontRoleHUD, options.HUD)
	if err != nil {
		return err
	}

	padX := atlas.HUDFontSize() / 3
	if padX < 6 {
		padX = 6
	}
	padY := atlas.HUDFontSize() / 5
	if padY < 3 {
		padY = 3
	}
	edgePad := edgeLabelPadding(options.Appearance.LabelFontSize)
	boxWidth := textWidth + padX*2
	boxHeight := textHeight + padY*2
	if boxWidth > buffer.Width {
		boxWidth = buffer.Width
	}
	if boxHeight > buffer.Height {
		boxHeight = buffer.Height
	}

	x := centeredInSpan(0, buffer.Width, boxWidth)
	bottomReserved := options.Appearance.LabelFontSize + edgePad
	y := buffer.Height - bottomReserved - edgePad - boxHeight
	if y < edgePad {
		y = centeredInSpan(0, buffer.Height, boxHeight)
	}
	box := Rect{X: x, Y: y, Width: boxWidth, Height: boxHeight}

	blendRect(buffer, box, 0xb0101216)
	borderColor := uint32(0xd0ffffff)
	blendRect(buffer, Rect{X: box.X, Y: box.Y, Width: box.Width, Height: 1}, borderColor)
	blendRect(buffer, Rect{X: box.X, Y: box.Y + box.Height - 1, Width: box.Width, Height: 1}, borderColor)
	blendRect(buffer, Rect{X: box.X, Y: box.Y, Width: 1, Height: box.Height}, borderColor)
	blendRect(buffer, Rect{X: box.X + box.Width - 1, Y: box.Y, Width: 1, Height: box.Height}, borderColor)

	textX := clampInt(box.X+padX, box.X, box.X+box.Width-textWidth)
	textY := clampInt(box.Y+padY, box.Y, box.Y+box.Height-textHeight)
	return compositeTextClipped(buffer, atlas, FontRoleHUD, options.HUD, textX, textY, 0xffffffff, box)
}

func axisBoundary(total int, count int, boundaryIndex int) int {
	if boundaryIndex <= 0 {
		return 0
	}
	if boundaryIndex >= count {
		return total
	}
	start, _, err := axisSegment(total, count, boundaryIndex)
	if err != nil {
		return 0
	}
	return start
}

func lineSpanForBoundary(boundary int, total int, lineWidth int) (int, int) {
	if total <= 0 || lineWidth <= 0 {
		return 0, 0
	}
	if lineWidth >= total {
		return 0, total
	}
	if boundary <= 0 {
		return 0, lineWidth
	}
	if boundary >= total {
		return total - lineWidth, total
	}

	start := boundary - lineWidth/2
	if start < 0 {
		start = 0
	}
	end := start + lineWidth
	if end > total {
		end = total
		start = end - lineWidth
		if start < 0 {
			start = 0
		}
	}
	return start, end
}

func opacityToAlpha(opacity float64) int {
	if opacity <= 0 {
		return 0
	}
	if opacity >= 1 {
		return 255
	}
	return int(math.Round(opacity * 255))
}

func edgeLabelPadding(fontSize int) int {
	pad := fontSize / 3
	if pad < 2 {
		pad = 2
	}
	return pad
}

func centeredInSpan(start int, end int, size int) int {
	if end <= start {
		return start
	}
	return start + ((end - start - size) / 2)
}

func edgeAlignedInSpan(start int, end int, size int, pad int, trailing bool) int {
	if end <= start {
		return start
	}
	if end-start >= size+pad*2 {
		if trailing {
			return end - pad - size
		}
		return start + pad
	}
	return centeredInSpan(start, end, size)
}

func clampInt(value int, low int, high int) int {
	if high < low {
		return low
	}
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func overwriteRect(buffer ARGBBuffer, rect Rect, color uint32) {
	rect, ok := clipRect(rect, buffer.Width, buffer.Height)
	if !ok {
		return
	}
	for y := rect.Y; y < rect.Y+rect.Height; y++ {
		row := buffer.Pixels[y*buffer.Stride : y*buffer.Stride+buffer.Width]
		for x := rect.X; x < rect.X+rect.Width; x++ {
			row[x] = color
		}
	}
}

func blendRect(buffer ARGBBuffer, rect Rect, color uint32) {
	rect, ok := clipRect(rect, buffer.Width, buffer.Height)
	if !ok {
		return
	}
	for y := rect.Y; y < rect.Y+rect.Height; y++ {
		row := buffer.Pixels[y*buffer.Stride : y*buffer.Stride+buffer.Width]
		for x := rect.X; x < rect.X+rect.Width; x++ {
			row[x] = BlendARGB(row[x], color)
		}
	}
}

func clipRect(rect Rect, width int, height int) (Rect, bool) {
	if rect.Width <= 0 || rect.Height <= 0 || width <= 0 || height <= 0 {
		return Rect{}, false
	}
	x0 := rect.X
	y0 := rect.Y
	x1 := rect.X + rect.Width
	y1 := rect.Y + rect.Height
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > width {
		x1 = width
	}
	if y1 > height {
		y1 = height
	}
	if x1 <= x0 || y1 <= y0 {
		return Rect{}, false
	}
	return Rect{X: x0, Y: y0, Width: x1 - x0, Height: y1 - y0}, true
}

func compositeTextClipped(dst ARGBBuffer, atlas *FontAtlas, role FontRole, text string, x, y int, color uint32, clip Rect) error {
	if atlas == nil {
		return fmt.Errorf("font atlas is nil")
	}
	if err := dst.Validate(); err != nil {
		return err
	}
	clip, ok := clipRect(clip, dst.Width, dst.Height)
	if !ok {
		return nil
	}

	cursorX := x
	for _, r := range text {
		glyph, ok := atlas.Glyph(role, r)
		if !ok {
			return fmt.Errorf("glyph %q is not available", r)
		}
		if err := compositeGlyphClipped(dst, glyph, cursorX, y, color, clip); err != nil {
			return err
		}
		cursorX += glyph.Advance
	}
	return nil
}

func compositeGlyphClipped(dst ARGBBuffer, glyph GlyphBitmap, x, y int, color uint32, clip Rect) error {
	if err := glyph.Validate(); err != nil {
		return err
	}

	colorAlpha := int((color >> 24) & 0xff)
	if colorAlpha == 0 {
		return nil
	}
	colorRGB := color & 0x00ffffff

	clipX1 := clip.X + clip.Width
	clipY1 := clip.Y + clip.Height
	for gy := 0; gy < glyph.Height; gy++ {
		dy := y + gy
		if dy < clip.Y || dy >= clipY1 {
			continue
		}
		for gx := 0; gx < glyph.Width; gx++ {
			dx := x + gx
			if dx < clip.X || dx >= clipX1 {
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
