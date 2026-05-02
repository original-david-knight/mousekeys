package main

import "testing"

func TestRenderSelectedCellOverlayOnlyDrawsCellOutline(t *testing.T) {
	appearance := AppearanceConfig{
		GridOpacity:   0.5,
		GridLineWidth: 1,
		LabelFontSize: 14,
	}
	buffer, err := NewARGBBuffer(80, 60)
	if err != nil {
		t.Fatalf("new buffer: %v", err)
	}
	cell := Rect{X: 20, Y: 15, Width: 20, Height: 15}
	if err := RenderSelectedCellOverlay(buffer, SelectedCellRenderOptions{
		Cell:       cell,
		Appearance: appearance,
	}); err != nil {
		t.Fatalf("render selected cell outline: %v", err)
	}

	lineColor := gridLineCoreColor(appearance.GridOpacity)
	if got := argbAt(buffer, cell.X, cell.Y+5); got != lineColor {
		t.Fatalf("left outline pixel = %#x, want %#x", got, lineColor)
	}
	if got := argbAt(buffer, cell.X+cell.Width, cell.Y+5); got != lineColor {
		t.Fatalf("right outline pixel = %#x, want %#x", got, lineColor)
	}
	if got := argbAt(buffer, cell.X+5, cell.Y); got != lineColor {
		t.Fatalf("top outline pixel = %#x, want %#x", got, lineColor)
	}
	if got := argbAt(buffer, cell.X+5, cell.Y+cell.Height); got != lineColor {
		t.Fatalf("bottom outline pixel = %#x, want %#x", got, lineColor)
	}
	if got := argbAt(buffer, cell.X+cell.Width/2, cell.Y+cell.Height/2); got != 0 {
		t.Fatalf("selected cell interior pixel = %#x, want transparent", got)
	}
	if got := argbAt(buffer, 5, 5); got != 0 {
		t.Fatalf("outside selected cell pixel = %#x, want transparent", got)
	}
}
