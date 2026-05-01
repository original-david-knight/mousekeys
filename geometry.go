package main

import "fmt"

func GridCellBounds(m Monitor, gridSize int, col int, row int) (Rect, error) {
	if gridSize <= 0 {
		return Rect{}, fmt.Errorf("grid size must be positive, got %d", gridSize)
	}
	if col < 0 || col >= gridSize || row < 0 || row >= gridSize {
		return Rect{}, fmt.Errorf("grid cell out of range: col=%d row=%d size=%d", col, row, gridSize)
	}

	x0, x1, err := axisSegment(m.Width, gridSize, col)
	if err != nil {
		return Rect{}, err
	}
	y0, y1, err := axisSegment(m.Height, gridSize, row)
	if err != nil {
		return Rect{}, err
	}

	return Rect{
		X:      x0,
		Y:      y0,
		Width:  x1 - x0,
		Height: y1 - y0,
	}, nil
}

func GridCellCenter(m Monitor, gridSize int, col int, row int) (Point, error) {
	bounds, err := GridCellBounds(m, gridSize, col, row)
	if err != nil {
		return Point{}, err
	}
	return bounds.Center(), nil
}

func axisSegment(total int, count int, index int) (int, int, error) {
	if total <= 0 {
		return 0, 0, fmt.Errorf("axis length must be positive, got %d", total)
	}
	if count <= 0 {
		return 0, 0, fmt.Errorf("axis segment count must be positive, got %d", count)
	}
	if index < 0 || index >= count {
		return 0, 0, fmt.Errorf("axis segment index out of range: index=%d count=%d", index, count)
	}

	base := total / count
	remainder := total % count
	start := index*base + min(index, remainder)
	size := base
	if index < remainder {
		size++
	}

	return start, start + size, nil
}
