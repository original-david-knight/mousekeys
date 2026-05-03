package main

import "testing"

func TestGridGeometryUnevenDivisionCoversMonitorExactly(t *testing.T) {
	monitor := Monitor{
		Name:          "DP-1",
		OriginX:       -120,
		OriginY:       80,
		LogicalWidth:  257,
		LogicalHeight: 193,
		Scale:         1.25,
	}
	grid, err := NewGridGeometry(monitor, 26)
	if err != nil {
		t.Fatalf("NewGridGeometry returned error: %v", err)
	}
	assertPartitionCoversExactly(t, "columns", grid.Columns, monitor.LogicalWidth)
	assertPartitionCoversExactly(t, "rows", grid.Rows, monitor.LogicalHeight)

	area := 0
	for column := 0; column < grid.Size; column++ {
		for row := 0; row < grid.Size; row++ {
			cell, err := grid.Cell(column, row)
			if err != nil {
				t.Fatalf("Cell(%d,%d) returned error: %v", column, row, err)
			}
			area += cell.Width * cell.Height
		}
	}
	if area != monitor.LogicalWidth*monitor.LogicalHeight {
		t.Fatalf("grid cell union area = %d, want %d", area, monitor.LogicalWidth*monitor.LogicalHeight)
	}
}

func TestGridCoordinateTransformsUseLogicalPixelsAndVirtualOrigins(t *testing.T) {
	tests := []struct {
		name    string
		monitor Monitor
		column  int
		row     int
	}{
		{
			name: "scaled non-zero origin",
			monitor: Monitor{
				Name:          "eDP-1",
				OriginX:       320,
				OriginY:       45,
				LogicalWidth:  257,
				LogicalHeight: 193,
				Scale:         1.5,
			},
			column: 12,
			row:    8,
		},
		{
			name: "scaled negative origin",
			monitor: Monitor{
				Name:          "HDMI-A-1",
				OriginX:       -1280,
				OriginY:       -360,
				LogicalWidth:  1280,
				LogicalHeight: 720,
				Scale:         2,
			},
			column: 25,
			row:    0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			grid, err := NewGridGeometry(tt.monitor, 26)
			if err != nil {
				t.Fatalf("NewGridGeometry returned error: %v", err)
			}
			cell, err := grid.Cell(tt.column, tt.row)
			if err != nil {
				t.Fatalf("Cell returned error: %v", err)
			}
			wantLocalX, wantLocalY := cell.Center()

			localX, localY, err := grid.CellCenterLocal(tt.column, tt.row)
			if err != nil {
				t.Fatalf("CellCenterLocal returned error: %v", err)
			}
			if localX != wantLocalX || localY != wantLocalY {
				t.Fatalf("local center = %.3f,%.3f, want %.3f,%.3f", localX, localY, wantLocalX, wantLocalY)
			}
			if localX < 0 || localX > float64(tt.monitor.LogicalWidth) || localY < 0 || localY > float64(tt.monitor.LogicalHeight) {
				t.Fatalf("local center %.3f,%.3f escaped logical monitor bounds %+v", localX, localY, tt.monitor)
			}

			virtualX, virtualY, err := grid.CellCenterVirtual(tt.column, tt.row)
			if err != nil {
				t.Fatalf("CellCenterVirtual returned error: %v", err)
			}
			wantVirtualX := wantLocalX + float64(tt.monitor.OriginX)
			wantVirtualY := wantLocalY + float64(tt.monitor.OriginY)
			if virtualX != wantVirtualX || virtualY != wantVirtualY {
				t.Fatalf("virtual center = %.3f,%.3f, want %.3f,%.3f", virtualX, virtualY, wantVirtualX, wantVirtualY)
			}

			state := coordinateEntryState{input: string([]byte{
				byte('A' + tt.column),
				byte('A' + tt.row),
			})}
			selected, err := state.SelectedCell(grid)
			if err != nil {
				t.Fatalf("SelectedCell returned error: %v", err)
			}
			if selected.CenterLocalX != wantLocalX || selected.CenterLocalY != wantLocalY ||
				selected.CenterVirtualX != wantVirtualX || selected.CenterVirtualY != wantVirtualY {
				t.Fatalf("selected centers = local %.3f,%.3f virtual %.3f,%.3f; want local %.3f,%.3f virtual %.3f,%.3f",
					selected.CenterLocalX, selected.CenterLocalY, selected.CenterVirtualX, selected.CenterVirtualY,
					wantLocalX, wantLocalY, wantVirtualX, wantVirtualY)
			}
		})
	}
}

func TestHiddenSubcellGeometryUsesExactCountFormulaPerAxis(t *testing.T) {
	tests := []struct {
		name       string
		cell       Rect
		pixelSize  int
		wantCountX int
		wantCountY int
	}{
		{
			name:       "minimum one",
			cell:       Rect{Width: 1, Height: 2},
			pixelSize:  5,
			wantCountX: 1,
			wantCountY: 1,
		},
		{
			name:       "round down and round up independently",
			cell:       Rect{Width: 12, Height: 13},
			pixelSize:  5,
			wantCountX: 2,
			wantCountY: 3,
		},
		{
			name:       "cap at alphabet size",
			cell:       Rect{Width: 500, Height: 250},
			pixelSize:  5,
			wantCountX: 26,
			wantCountY: 26,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			grid, err := NewHiddenSubcellGeometry(tt.cell, tt.pixelSize)
			if err != nil {
				t.Fatalf("NewHiddenSubcellGeometry returned error: %v", err)
			}
			if grid.CountX != tt.wantCountX || grid.CountY != tt.wantCountY {
				t.Fatalf("hidden counts = %dx%d, want %dx%d", grid.CountX, grid.CountY, tt.wantCountX, tt.wantCountY)
			}
			if grid.StepX != float64(tt.cell.Width)/float64(tt.wantCountX) || grid.StepY != float64(tt.cell.Height)/float64(tt.wantCountY) {
				t.Fatalf("hidden steps = %.3fx%.3f do not match cell/count formula", grid.StepX, grid.StepY)
			}
		})
	}
}

func assertPartitionCoversExactly(t *testing.T, name string, spans []Span, total int) {
	t.Helper()
	if len(spans) != 26 {
		t.Fatalf("%s span count = %d, want 26", name, len(spans))
	}
	if spans[0].Start != 0 {
		t.Fatalf("%s first span starts at %d, want 0", name, spans[0].Start)
	}
	if spans[len(spans)-1].End != total {
		t.Fatalf("%s last span ends at %d, want %d", name, spans[len(spans)-1].End, total)
	}
	minSize, maxSize := spans[0].Size(), spans[0].Size()
	for i, span := range spans {
		if span.Start != i*total/len(spans) || span.End != (i+1)*total/len(spans) {
			t.Fatalf("%s span[%d] = %+v, want deterministic integer partition", name, i, span)
		}
		if i > 0 && span.Start != spans[i-1].End {
			t.Fatalf("%s span[%d] starts at %d, previous ended at %d", name, i, span.Start, spans[i-1].End)
		}
		if span.Size() < minSize {
			minSize = span.Size()
		}
		if span.Size() > maxSize {
			maxSize = span.Size()
		}
	}
	if maxSize-minSize > 1 {
		t.Fatalf("%s uneven sizes differ by %d, want at most 1", name, maxSize-minSize)
	}
}
