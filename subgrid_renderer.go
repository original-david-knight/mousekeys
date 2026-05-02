package main

import "fmt"

type SubgridRenderOptions struct {
	Geometry   SubgridGeometry
	Appearance AppearanceConfig
	FontAtlas  *FontAtlas
}

func RenderSubgridOverlay(buffer ARGBBuffer, options SubgridRenderOptions) error {
	if err := buffer.Validate(); err != nil {
		return err
	}
	options, err := normalizeSubgridRenderOptions(options)
	if err != nil {
		return err
	}

	clearARGBBuffer(buffer)
	drawSubgridBackground(buffer, options.Geometry.Display)
	drawSubgridLines(buffer, options)
	if err := drawSubgridEdgeLabels(buffer, options); err != nil {
		return err
	}
	return nil
}

func normalizeSubgridRenderOptions(options SubgridRenderOptions) (SubgridRenderOptions, error) {
	defaults := DefaultConfig()
	if options.Appearance == (AppearanceConfig{}) {
		options.Appearance = defaults.Appearance
		if options.FontAtlas != nil && options.FontAtlas.LabelFontSize() > 0 {
			options.Appearance.LabelFontSize = options.FontAtlas.LabelFontSize()
		}
	}
	if options.Geometry.Display.Width <= 0 || options.Geometry.Display.Height <= 0 {
		return SubgridRenderOptions{}, fmt.Errorf("subgrid display rect has invalid size %dx%d", options.Geometry.Display.Width, options.Geometry.Display.Height)
	}
	if options.Geometry.XCount < 1 || options.Geometry.XCount > maxSubgridAxisCells {
		return SubgridRenderOptions{}, fmt.Errorf("subgrid horizontal count must be between 1 and %d, got %d", maxSubgridAxisCells, options.Geometry.XCount)
	}
	if options.Geometry.YCount < 1 || options.Geometry.YCount > maxSubgridAxisCells {
		return SubgridRenderOptions{}, fmt.Errorf("subgrid vertical count must be between 1 and %d, got %d", maxSubgridAxisCells, options.Geometry.YCount)
	}
	if options.Appearance.GridOpacity < 0 || options.Appearance.GridOpacity > 1 {
		return SubgridRenderOptions{}, fmt.Errorf("appearance.grid_opacity must be between 0 and 1, got %g", options.Appearance.GridOpacity)
	}
	if options.Appearance.GridLineWidth < 1 {
		return SubgridRenderOptions{}, fmt.Errorf("appearance.grid_line_width must be positive, got %d", options.Appearance.GridLineWidth)
	}
	if options.Appearance.LabelFontSize < 1 {
		return SubgridRenderOptions{}, fmt.Errorf("appearance.label_font_size must be positive, got %d", options.Appearance.LabelFontSize)
	}

	if options.FontAtlas == nil {
		atlas, err := NewFontAtlas(FontAtlasOptions{LabelFontSize: options.Appearance.LabelFontSize})
		if err != nil {
			return SubgridRenderOptions{}, err
		}
		options.FontAtlas = atlas
	} else if options.FontAtlas.LabelFontSize() != options.Appearance.LabelFontSize {
		return SubgridRenderOptions{}, fmt.Errorf("font atlas label size %d does not match appearance.label_font_size %d", options.FontAtlas.LabelFontSize(), options.Appearance.LabelFontSize)
	}
	return options, nil
}

func drawSubgridBackground(buffer ARGBBuffer, display Rect) {
	blendRect(buffer, display, 0x90101216)
}

func drawSubgridLines(buffer ARGBBuffer, options SubgridRenderOptions) {
	display := options.Geometry.Display
	lineColor := gridLineCoreColor(options.Appearance.GridOpacity)
	if lineColor == 0 {
		return
	}
	haloColor := gridLineHaloColor(options.Appearance.GridOpacity)
	lineWidth := options.Appearance.GridLineWidth
	haloWidth := gridLineHaloWidth(lineWidth)

	if haloColor != 0 && haloWidth > lineWidth {
		for col := 0; col <= options.Geometry.XCount; col++ {
			boundary := axisBoundary(display.Width, options.Geometry.XCount, col)
			start, end := lineSpanForBoundary(boundary, display.Width, haloWidth)
			blendRect(buffer, Rect{X: display.X + start, Y: display.Y, Width: end - start, Height: display.Height}, haloColor)
		}
		for row := 0; row <= options.Geometry.YCount; row++ {
			boundary := axisBoundary(display.Height, options.Geometry.YCount, row)
			start, end := lineSpanForBoundary(boundary, display.Height, haloWidth)
			blendRect(buffer, Rect{X: display.X, Y: display.Y + start, Width: display.Width, Height: end - start}, haloColor)
		}
	}

	for col := 0; col <= options.Geometry.XCount; col++ {
		boundary := axisBoundary(display.Width, options.Geometry.XCount, col)
		start, end := lineSpanForBoundary(boundary, display.Width, lineWidth)
		overwriteRect(buffer, Rect{X: display.X + start, Y: display.Y, Width: end - start, Height: display.Height}, lineColor)
	}
	for row := 0; row <= options.Geometry.YCount; row++ {
		boundary := axisBoundary(display.Height, options.Geometry.YCount, row)
		start, end := lineSpanForBoundary(boundary, display.Height, lineWidth)
		overwriteRect(buffer, Rect{X: display.X, Y: display.Y + start, Width: display.Width, Height: end - start}, lineColor)
	}
}

func drawSubgridEdgeLabels(buffer ARGBBuffer, options SubgridRenderOptions) error {
	display := options.Geometry.Display
	atlas := options.FontAtlas
	pad := edgeLabelPadding(options.Appearance.LabelFontSize)

	topY0, topY1, err := axisSegment(display.Height, options.Geometry.YCount, 0)
	if err != nil {
		return err
	}
	leftX0, leftX1, err := axisSegment(display.Width, options.Geometry.XCount, 0)
	if err != nil {
		return err
	}

	for col := 0; col < options.Geometry.XCount; col++ {
		text := string(mainGridLetters[col])
		textWidth, textHeight, err := atlas.TextSize(FontRoleLabel, text)
		if err != nil {
			return err
		}
		x0, x1, err := axisSegment(display.Width, options.Geometry.XCount, col)
		if err != nil {
			return err
		}
		cell := Rect{
			X:      display.X + x0,
			Y:      display.Y + topY0,
			Width:  x1 - x0,
			Height: topY1 - topY0,
		}
		textX := centeredInSpan(cell.X, cell.X+cell.Width, textWidth)
		textY := edgeAlignedInSpan(cell.Y, cell.Y+cell.Height, textHeight, pad, false)
		if err := compositeGridLabel(buffer, atlas, FontRoleLabel, text, textX, textY, cell); err != nil {
			return err
		}
	}

	for row := 0; row < options.Geometry.YCount; row++ {
		text := string(mainGridLetters[row])
		textWidth, textHeight, err := atlas.TextSize(FontRoleLabel, text)
		if err != nil {
			return err
		}
		y0, y1, err := axisSegment(display.Height, options.Geometry.YCount, row)
		if err != nil {
			return err
		}
		cell := Rect{
			X:      display.X + leftX0,
			Y:      display.Y + y0,
			Width:  leftX1 - leftX0,
			Height: y1 - y0,
		}
		textX := edgeAlignedInSpan(cell.X, cell.X+cell.Width, textWidth, pad, false)
		textY := centeredInSpan(cell.Y, cell.Y+cell.Height, textHeight)
		if err := compositeGridLabel(buffer, atlas, FontRoleLabel, text, textX, textY, cell); err != nil {
			return err
		}
	}
	return nil
}
