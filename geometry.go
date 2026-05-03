package main

import "fmt"

type Span struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

func (s Span) Size() int {
	return s.End - s.Start
}

type Rect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

func (r Rect) Center() (float64, float64) {
	return float64(r.X) + float64(r.Width)/2, float64(r.Y) + float64(r.Height)/2
}

type GridGeometry struct {
	Monitor Monitor `json:"monitor"`
	Size    int     `json:"size"`
	Columns []Span  `json:"columns"`
	Rows    []Span  `json:"rows"`
}

func NewGridGeometry(m Monitor, size int) (GridGeometry, error) {
	if err := m.Validate(); err != nil {
		return GridGeometry{}, err
	}
	if size <= 0 {
		return GridGeometry{}, fmt.Errorf("grid size must be positive, got %d", size)
	}
	return GridGeometry{
		Monitor: m,
		Size:    size,
		Columns: partitionSpans(m.LogicalWidth, size),
		Rows:    partitionSpans(m.LogicalHeight, size),
	}, nil
}

func partitionSpans(total, parts int) []Span {
	spans := make([]Span, parts)
	for i := range spans {
		spans[i] = Span{
			Start: i * total / parts,
			End:   (i + 1) * total / parts,
		}
	}
	return spans
}

func (g GridGeometry) Cell(column, row int) (Rect, error) {
	if column < 0 || column >= g.Size {
		return Rect{}, fmt.Errorf("column %d outside grid size %d", column, g.Size)
	}
	if row < 0 || row >= g.Size {
		return Rect{}, fmt.Errorf("row %d outside grid size %d", row, g.Size)
	}
	x := g.Columns[column]
	y := g.Rows[row]
	return Rect{
		X:      x.Start,
		Y:      y.Start,
		Width:  x.Size(),
		Height: y.Size(),
	}, nil
}

func (g GridGeometry) CellCenterLocal(column, row int) (float64, float64, error) {
	cell, err := g.Cell(column, row)
	if err != nil {
		return 0, 0, err
	}
	x, y := cell.Center()
	return x, y, nil
}

func (g GridGeometry) CellCenterVirtual(column, row int) (float64, float64, error) {
	x, y, err := g.CellCenterLocal(column, row)
	if err != nil {
		return 0, 0, err
	}
	return x + float64(g.Monitor.OriginX), y + float64(g.Monitor.OriginY), nil
}
