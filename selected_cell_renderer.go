package main

import "fmt"

type SelectedCellRenderOptions struct {
	Cell       Rect
	Appearance AppearanceConfig
}

func RenderSelectedCellOverlay(buffer ARGBBuffer, options SelectedCellRenderOptions) error {
	if err := buffer.Validate(); err != nil {
		return err
	}
	options, err := normalizeSelectedCellRenderOptions(options)
	if err != nil {
		return err
	}

	clearARGBBuffer(buffer)
	drawSelectedCellOutline(buffer, options)
	return nil
}

func normalizeSelectedCellRenderOptions(options SelectedCellRenderOptions) (SelectedCellRenderOptions, error) {
	defaults := DefaultConfig()
	if options.Appearance == (AppearanceConfig{}) {
		options.Appearance = defaults.Appearance
	}
	if options.Cell.Width <= 0 || options.Cell.Height <= 0 {
		return SelectedCellRenderOptions{}, fmt.Errorf("selected cell has invalid size %dx%d", options.Cell.Width, options.Cell.Height)
	}
	if options.Appearance.GridOpacity < 0 || options.Appearance.GridOpacity > 1 {
		return SelectedCellRenderOptions{}, fmt.Errorf("appearance.grid_opacity must be between 0 and 1, got %g", options.Appearance.GridOpacity)
	}
	if options.Appearance.GridLineWidth < 1 {
		return SelectedCellRenderOptions{}, fmt.Errorf("appearance.grid_line_width must be positive, got %d", options.Appearance.GridLineWidth)
	}
	return options, nil
}

func drawSelectedCellOutline(buffer ARGBBuffer, options SelectedCellRenderOptions) {
	lineColor := gridLineCoreColor(options.Appearance.GridOpacity)
	if lineColor == 0 {
		return
	}
	lineWidth := options.Appearance.GridLineWidth
	haloColor := gridLineHaloColor(options.Appearance.GridOpacity)
	haloWidth := gridLineHaloWidth(lineWidth)
	if haloColor != 0 && haloWidth > lineWidth {
		drawRectOutline(buffer, options.Cell, haloWidth, haloColor, true)
	}
	drawRectOutline(buffer, options.Cell, lineWidth, lineColor, false)
}

func drawRectOutline(buffer ARGBBuffer, rect Rect, width int, color uint32, blend bool) {
	if width <= 0 {
		return
	}

	leftStart, leftEnd := lineSpanForBoundary(rect.X, buffer.Width, width)
	rightStart, rightEnd := lineSpanForBoundary(rect.X+rect.Width, buffer.Width, width)
	topStart, topEnd := lineSpanForBoundary(rect.Y, buffer.Height, width)
	bottomStart, bottomEnd := lineSpanForBoundary(rect.Y+rect.Height, buffer.Height, width)

	draw := overwriteRect
	if blend {
		draw = blendRect
	}
	draw(buffer, Rect{X: leftStart, Y: topStart, Width: rightEnd - leftStart, Height: topEnd - topStart}, color)
	draw(buffer, Rect{X: leftStart, Y: bottomStart, Width: rightEnd - leftStart, Height: bottomEnd - bottomStart}, color)
	draw(buffer, Rect{X: leftStart, Y: topStart, Width: leftEnd - leftStart, Height: bottomEnd - topStart}, color)
	draw(buffer, Rect{X: rightStart, Y: topStart, Width: rightEnd - rightStart, Height: bottomEnd - topStart}, color)
}
