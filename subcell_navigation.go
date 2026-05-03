package main

import (
	"fmt"
	"time"
)

type hiddenDirection string

const (
	hiddenDirectionLeft  hiddenDirection = "left"
	hiddenDirectionDown  hiddenDirection = "down"
	hiddenDirectionUp    hiddenDirection = "up"
	hiddenDirectionRight hiddenDirection = "right"
)

type hiddenSubcellNavigator struct {
	monitor Monitor
	grid    HiddenSubcellGeometry
	x       float64
	y       float64
}

func newHiddenSubcellNavigator(monitor Monitor, cell selectedCell, subgridPixelSize int) (hiddenSubcellNavigator, error) {
	if err := monitor.Validate(); err != nil {
		return hiddenSubcellNavigator{}, err
	}
	grid, err := NewHiddenSubcellGeometry(cell.Bounds, subgridPixelSize)
	if err != nil {
		return hiddenSubcellNavigator{}, err
	}
	return hiddenSubcellNavigator{
		monitor: monitor,
		grid:    grid,
		x:       clampFloat64(cell.CenterLocalX, 0, float64(monitor.LogicalWidth)),
		y:       clampFloat64(cell.CenterLocalY, 0, float64(monitor.LogicalHeight)),
	}, nil
}

func (n *hiddenSubcellNavigator) Move(direction hiddenDirection, subcells int) (float64, float64, error) {
	if n == nil {
		return 0, 0, fmt.Errorf("hidden subcell navigator is nil")
	}
	if subcells < 1 {
		return n.x, n.y, fmt.Errorf("hidden subcell movement must be positive, got %d", subcells)
	}
	switch direction {
	case hiddenDirectionLeft:
		n.x -= n.grid.StepX * float64(subcells)
	case hiddenDirectionRight:
		n.x += n.grid.StepX * float64(subcells)
	case hiddenDirectionUp:
		n.y -= n.grid.StepY * float64(subcells)
	case hiddenDirectionDown:
		n.y += n.grid.StepY * float64(subcells)
	default:
		return n.x, n.y, fmt.Errorf("unknown hidden subcell direction %q", direction)
	}
	n.x = clampFloat64(n.x, 0, float64(n.monitor.LogicalWidth))
	n.y = clampFloat64(n.y, 0, float64(n.monitor.LogicalHeight))
	return n.x, n.y, nil
}

func directionFromKeyEvent(event KeyboardEvent) (hiddenDirection, bool) {
	if event.Kind != KeyboardEventKey || event.Key == "" {
		return "", false
	}
	if event.Modifiers.Ctrl || event.Modifiers.Alt || event.Modifiers.Super {
		return "", false
	}
	switch event.Key {
	case "h", "H", "Left", "KP_Left":
		return hiddenDirectionLeft, true
	case "j", "J", "Down", "KP_Down":
		return hiddenDirectionDown, true
	case "k", "K", "Up", "KP_Up":
		return hiddenDirectionUp, true
	case "l", "L", "Right", "KP_Right":
		return hiddenDirectionRight, true
	default:
		return "", false
	}
}

type heldDirectionRepeatStage struct {
	Subcells int
	Interval time.Duration
}

const heldDirectionInitialDelay = 350 * time.Millisecond

func heldDirectionRepeatStageForElapsed(elapsed time.Duration) heldDirectionRepeatStage {
	switch {
	case elapsed >= 1850*time.Millisecond:
		return heldDirectionRepeatStage{Subcells: 4, Interval: 16 * time.Millisecond}
	case elapsed >= 1350*time.Millisecond:
		return heldDirectionRepeatStage{Subcells: 3, Interval: 25 * time.Millisecond}
	case elapsed >= 850*time.Millisecond:
		return heldDirectionRepeatStage{Subcells: 2, Interval: 35 * time.Millisecond}
	default:
		return heldDirectionRepeatStage{Subcells: 1, Interval: 50 * time.Millisecond}
	}
}

func clampFloat64(value, minValue, maxValue float64) float64 {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}
