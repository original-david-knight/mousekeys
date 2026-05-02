package main

const noMainGridColumn = -1

type MainCoordinateEntryFSM struct {
	gridSize int
	monitor  Monitor
	input    []byte
}

type MainCoordinateEntryResult struct {
	Changed  bool
	Selected *MainCoordinateSelectedEvent
}

type MainCoordinateSelectedEvent struct {
	Column       int
	Row          int
	ColumnLetter byte
	RowLetter    byte
	Bounds       Rect
	Center       Point
}

func NewMainCoordinateEntryFSM(gridSize int, monitor Monitor) *MainCoordinateEntryFSM {
	fsm := &MainCoordinateEntryFSM{
		gridSize: gridSize,
		monitor:  monitor,
	}
	fsm.Reset()
	return fsm
}

func (f *MainCoordinateEntryFSM) Reset() {
	if f == nil {
		return
	}
	f.input = f.input[:0]
}

func (f *MainCoordinateEntryFSM) HUD() string {
	if f == nil {
		return DefaultMainGridHUD
	}
	switch len(f.input) {
	case 0:
		return "__"
	case 1:
		return string([]byte{f.input[0], '_'})
	default:
		return string(f.input[:2])
	}
}

func (f *MainCoordinateEntryFSM) SelectedColumn() (int, bool) {
	if f == nil || len(f.input) == 0 {
		return noMainGridColumn, false
	}
	return int(f.input[0] - 'A'), true
}

func (f *MainCoordinateEntryFSM) Input() string {
	if f == nil {
		return ""
	}
	return string(f.input)
}

func (f *MainCoordinateEntryFSM) HandleToken(token KeyboardToken) MainCoordinateEntryResult {
	if f == nil {
		return MainCoordinateEntryResult{}
	}
	if tokenHasKeyboardCommand(token, KeyboardCommandBackspace) {
		return f.backspace()
	}
	if token.Kind != KeyboardTokenLetter {
		return MainCoordinateEntryResult{}
	}
	letter, ok := normalizedGridLetter(token.Letter)
	if !ok || !f.letterInGrid(letter) || len(f.input) >= 2 {
		return MainCoordinateEntryResult{}
	}

	f.input = append(f.input, letter)
	result := MainCoordinateEntryResult{Changed: true}
	if len(f.input) == 2 {
		result.Selected = f.selection()
	}
	return result
}

func (f *MainCoordinateEntryFSM) backspace() MainCoordinateEntryResult {
	if len(f.input) == 0 {
		return MainCoordinateEntryResult{}
	}
	f.input = f.input[:len(f.input)-1]
	return MainCoordinateEntryResult{Changed: true}
}

func (f *MainCoordinateEntryFSM) selection() *MainCoordinateSelectedEvent {
	col := int(f.input[0] - 'A')
	row := int(f.input[1] - 'A')
	bounds, err := GridCellBounds(f.monitor, f.gridSize, col, row)
	if err != nil {
		return &MainCoordinateSelectedEvent{
			Column:       col,
			Row:          row,
			ColumnLetter: f.input[0],
			RowLetter:    f.input[1],
		}
	}
	return &MainCoordinateSelectedEvent{
		Column:       col,
		Row:          row,
		ColumnLetter: f.input[0],
		RowLetter:    f.input[1],
		Bounds:       bounds,
		Center:       bounds.Center(),
	}
}

func (f *MainCoordinateEntryFSM) letterInGrid(letter byte) bool {
	return f.gridSize > 0 && int(letter-'A') < f.gridSize
}

func normalizedGridLetter(letter byte) (byte, bool) {
	switch {
	case letter >= 'A' && letter <= 'Z':
		return letter, true
	case letter >= 'a' && letter <= 'z':
		return letter - 'a' + 'A', true
	default:
		return 0, false
	}
}

func tokenHasKeyboardCommand(token KeyboardToken, command KeyboardCommand) bool {
	for _, got := range token.Commands {
		if got == command {
			return true
		}
	}
	return false
}
