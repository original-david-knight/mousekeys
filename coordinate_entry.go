package main

import "fmt"

type coordinateEntryState struct {
	input string
}

func (s coordinateEntryState) Input() string {
	return s.input
}

func (s coordinateEntryState) RenderState(gridSize int) CoordinateRenderState {
	state := CoordinateRenderState{Input: s.input}
	if len(s.input) >= 1 {
		if column, ok := gridLetterIndex(s.input[:1], gridSize); ok {
			state.SelectedColumn = column
			state.HasSelectedColumn = true
		}
	}
	return state
}

func (s *coordinateEntryState) AddLetter(letter string, gridSize int) (changed bool, selected bool) {
	if s == nil || len(s.input) >= 2 {
		return false, false
	}
	index, ok := gridLetterIndex(letter, gridSize)
	if !ok {
		return false, false
	}
	s.input += string(rendererLabelCharacters[index])
	return true, len(s.input) == 2
}

func (s *coordinateEntryState) Backspace() bool {
	if s == nil || len(s.input) == 0 {
		return false
	}
	s.input = s.input[:len(s.input)-1]
	return true
}

func (s coordinateEntryState) SelectedCell(grid GridGeometry) (selectedCell, error) {
	if len(s.input) != 2 {
		return selectedCell{}, fmt.Errorf("coordinate input %q is not complete", s.input)
	}
	column, ok := gridLetterIndex(s.input[:1], grid.Size)
	if !ok {
		return selectedCell{}, fmt.Errorf("coordinate column %q outside grid size %d", s.input[:1], grid.Size)
	}
	row, ok := gridLetterIndex(s.input[1:2], grid.Size)
	if !ok {
		return selectedCell{}, fmt.Errorf("coordinate row %q outside grid size %d", s.input[1:2], grid.Size)
	}
	bounds, err := grid.Cell(column, row)
	if err != nil {
		return selectedCell{}, err
	}
	centerLocalX, centerLocalY := bounds.Center()
	centerVirtualX := centerLocalX + float64(grid.Monitor.OriginX)
	centerVirtualY := centerLocalY + float64(grid.Monitor.OriginY)
	return selectedCell{
		Coordinate:     s.input,
		ColumnLetter:   s.input[:1],
		RowLetter:      s.input[1:2],
		Column:         column,
		Row:            row,
		Bounds:         bounds,
		CenterLocalX:   centerLocalX,
		CenterLocalY:   centerLocalY,
		CenterVirtualX: centerVirtualX,
		CenterVirtualY: centerVirtualY,
	}, nil
}

type selectedCell struct {
	Coordinate     string  `json:"coordinate"`
	ColumnLetter   string  `json:"column_letter"`
	RowLetter      string  `json:"row_letter"`
	Column         int     `json:"column"`
	Row            int     `json:"row"`
	Bounds         Rect    `json:"bounds"`
	CenterLocalX   float64 `json:"center_local_x"`
	CenterLocalY   float64 `json:"center_local_y"`
	CenterVirtualX float64 `json:"center_virtual_x"`
	CenterVirtualY float64 `json:"center_virtual_y"`
}

func gridLetterIndex(letter string, gridSize int) (int, bool) {
	if len(letter) != 1 || gridSize <= 0 {
		return 0, false
	}
	ch := letter[0]
	if ch >= 'a' && ch <= 'z' {
		ch -= 'a' - 'A'
	}
	if ch < 'A' || ch > 'Z' {
		return 0, false
	}
	index := int(ch - 'A')
	if index < 0 || index >= gridSize || index >= len(rendererLabelCharacters) {
		return 0, false
	}
	return index, true
}
