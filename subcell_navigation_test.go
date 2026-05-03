package main

import (
	"testing"
	"time"
)

func TestHiddenSubcellNavigatorLeavesCellAndClampsAtMonitorEdges(t *testing.T) {
	monitor := Monitor{Name: "DP-1", LogicalWidth: 260, LogicalHeight: 260, Scale: 1}
	cell := selectedCell{
		Bounds:       Rect{X: 120, Y: 100, Width: 10, Height: 10},
		CenterLocalX: 125,
		CenterLocalY: 105,
	}
	navigator, err := newHiddenSubcellNavigator(monitor, cell, 5)
	if err != nil {
		t.Fatalf("newHiddenSubcellNavigator returned error: %v", err)
	}
	if navigator.grid.CountX != 2 || navigator.grid.CountY != 2 || navigator.grid.StepX != 5 || navigator.grid.StepY != 5 {
		t.Fatalf("navigator grid = %+v, want 2x2 with 5px steps", navigator.grid)
	}

	x, y, err := navigator.Move(hiddenDirectionRight, 2)
	if err != nil {
		t.Fatalf("Move right returned error: %v", err)
	}
	if x != 135 || y != 105 || x <= float64(cell.Bounds.X+cell.Bounds.Width) {
		t.Fatalf("right movement = %.1f,%.1f, want outside selected cell", x, y)
	}
	x, y, err = navigator.Move(hiddenDirectionLeft, 100)
	if err != nil {
		t.Fatalf("Move left returned error: %v", err)
	}
	if x != 0 || y != 105 {
		t.Fatalf("left clamp = %.1f,%.1f, want monitor left edge", x, y)
	}
	x, y, err = navigator.Move(hiddenDirectionDown, 100)
	if err != nil {
		t.Fatalf("Move down returned error: %v", err)
	}
	if x != 0 || y != 260 {
		t.Fatalf("down clamp = %.1f,%.1f, want monitor bottom edge", x, y)
	}
}

func TestHeldDirectionRepeatStagesAccelerateMonotonically(t *testing.T) {
	stages := []heldDirectionRepeatStage{
		heldDirectionRepeatStageForElapsed(350 * time.Millisecond),
		heldDirectionRepeatStageForElapsed(900 * time.Millisecond),
		heldDirectionRepeatStageForElapsed(1400 * time.Millisecond),
		heldDirectionRepeatStageForElapsed(1900 * time.Millisecond),
	}
	for i, stage := range stages {
		wantSubcells := i + 1
		if stage.Subcells != wantSubcells {
			t.Fatalf("stage[%d].Subcells = %d, want %d", i, stage.Subcells, wantSubcells)
		}
		if i > 0 && stage.Interval >= stages[i-1].Interval {
			t.Fatalf("stage[%d].Interval = %s did not decrease from %s", i, stage.Interval, stages[i-1].Interval)
		}
	}
}
