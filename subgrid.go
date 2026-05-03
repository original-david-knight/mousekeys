package main

import (
	"fmt"
	"math"
)

const (
	maxSubgridAxisCells = 26
	noSubgridAxis       = -1
)

type SubgridMoveDirection string

const (
	SubgridMoveLeft  SubgridMoveDirection = "left"
	SubgridMoveDown  SubgridMoveDirection = "down"
	SubgridMoveUp    SubgridMoveDirection = "up"
	SubgridMoveRight SubgridMoveDirection = "right"
)

type SubgridGeometry struct {
	MainCell Rect
	Cursor   Point
	Display  Rect
	XCount   int
	YCount   int
}

func NewSubgridGeometry(monitor Monitor, mainCell Rect, cursor Point, subgridPixelSize int) (SubgridGeometry, error) {
	if monitor.Width <= 0 || monitor.Height <= 0 {
		return SubgridGeometry{}, fmt.Errorf("monitor %q has invalid size %dx%d", monitor.Name, monitor.Width, monitor.Height)
	}
	if mainCell.Width <= 0 || mainCell.Height <= 0 {
		return SubgridGeometry{}, fmt.Errorf("main grid cell has invalid size %dx%d", mainCell.Width, mainCell.Height)
	}

	xCount, err := SubgridAxisCount(mainCell.Width, subgridPixelSize)
	if err != nil {
		return SubgridGeometry{}, err
	}
	yCount, err := SubgridAxisCount(mainCell.Height, subgridPixelSize)
	if err != nil {
		return SubgridGeometry{}, err
	}

	natural := Rect{
		X:      cursor.X - mainCell.Width/2,
		Y:      cursor.Y - mainCell.Height/2,
		Width:  mainCell.Width,
		Height: mainCell.Height,
	}
	display := ShiftOrClipRectToBounds(natural, monitor.LocalRect())
	return SubgridGeometry{
		MainCell: mainCell,
		Cursor:   cursor,
		Display:  display,
		XCount:   xCount,
		YCount:   yCount,
	}, nil
}

func SubgridAxisCount(mainCellAxisSize int, subgridPixelSize int) (int, error) {
	if mainCellAxisSize < 0 {
		return 0, fmt.Errorf("main cell axis size must be non-negative, got %d", mainCellAxisSize)
	}
	if subgridPixelSize <= 0 {
		return 0, fmt.Errorf("subgrid pixel size must be positive, got %d", subgridPixelSize)
	}

	count := int(math.Round(float64(mainCellAxisSize) / float64(subgridPixelSize)))
	if count < 1 {
		count = 1
	}
	if count > maxSubgridAxisCells {
		count = maxSubgridAxisCells
	}
	return count, nil
}

func ShiftOrClipRectToBounds(rect Rect, bounds Rect) Rect {
	if rect.Width <= 0 || rect.Height <= 0 || bounds.Width <= 0 || bounds.Height <= 0 {
		return Rect{}
	}

	out := rect
	if out.Width > bounds.Width {
		out.X = bounds.X
		out.Width = bounds.Width
	} else if out.X < bounds.X {
		out.X = bounds.X
	} else if out.X+out.Width > bounds.X+bounds.Width {
		out.X = bounds.X + bounds.Width - out.Width
	}

	if out.Height > bounds.Height {
		out.Y = bounds.Y
		out.Height = bounds.Height
	} else if out.Y < bounds.Y {
		out.Y = bounds.Y
	} else if out.Y+out.Height > bounds.Y+bounds.Height {
		out.Y = bounds.Y + bounds.Height - out.Height
	}
	return out
}

type SubgridNavigationFSM struct {
	mainCell Rect
	bounds   Rect
	xCount   int
	yCount   int
	xStep    int
	yStep    int
	col      int
	row      int
	point    Point
}

type SubgridNavigationResult struct {
	Changed   bool
	Direction SubgridMoveDirection
	Column    int
	Row       int
	Point     Point
}

func NewSubgridNavigationFSM(mainCell Rect, bounds Rect, xCount int, yCount int, start Point) *SubgridNavigationFSM {
	if bounds.Width <= 0 || bounds.Height <= 0 {
		bounds = mainCell
	}
	return &SubgridNavigationFSM{
		mainCell: mainCell,
		bounds:   bounds,
		xCount:   xCount,
		yCount:   yCount,
		xStep:    subgridNavigationStep(mainCell.Width, xCount),
		yStep:    subgridNavigationStep(mainCell.Height, yCount),
		col:      subgridAxisIndexForPoint(start.X-mainCell.X, mainCell.Width, xCount),
		row:      subgridAxisIndexForPoint(start.Y-mainCell.Y, mainCell.Height, yCount),
		point:    clampPointToRect(start, bounds),
	}
}

func (f *SubgridNavigationFSM) Reset() {
	if f == nil {
		return
	}
	f.point = Point{}
	f.col = 0
	f.row = 0
}

func (f *SubgridNavigationFSM) Point() Point {
	if f == nil {
		return Point{}
	}
	return f.point
}

func (f *SubgridNavigationFSM) HandleToken(token KeyboardToken) SubgridNavigationResult {
	return f.HandleTokenSteps(token, 1)
}

func (f *SubgridNavigationFSM) HandleTokenSteps(token KeyboardToken, steps int) SubgridNavigationResult {
	if f == nil {
		return SubgridNavigationResult{}
	}
	direction, ok := subgridMoveDirectionFromToken(token)
	if !ok {
		return SubgridNavigationResult{}
	}
	if steps < 1 {
		steps = 1
	}

	var result SubgridNavigationResult
	for i := 0; i < steps; i++ {
		next := f.handleDirection(direction)
		if !next.Changed {
			break
		}
		result = next
	}
	return result
}

func (f *SubgridNavigationFSM) handleDirection(direction SubgridMoveDirection) SubgridNavigationResult {
	nextCol := f.col
	nextRow := f.row
	nextPoint := f.point
	switch direction {
	case SubgridMoveLeft:
		nextCol--
		nextPoint.X -= f.xStep
	case SubgridMoveRight:
		nextCol++
		nextPoint.X += f.xStep
	case SubgridMoveUp:
		nextRow--
		nextPoint.Y -= f.yStep
	case SubgridMoveDown:
		nextRow++
		nextPoint.Y += f.yStep
	default:
		return SubgridNavigationResult{}
	}

	nextPoint = clampPointToRect(nextPoint, f.bounds)
	if nextPoint == f.point {
		return SubgridNavigationResult{}
	}
	f.col = nextCol
	f.row = nextRow
	f.point = nextPoint
	return SubgridNavigationResult{
		Changed:   true,
		Direction: direction,
		Column:    f.col,
		Row:       f.row,
		Point:     f.point,
	}
}

func subgridMoveDirectionFromToken(token KeyboardToken) (SubgridMoveDirection, bool) {
	if direction, ok := arrowSubgridMoveDirection(token.KeySym); ok {
		return direction, true
	}
	if token.Kind == KeyboardTokenLetter {
		letter, ok := normalizedGridLetter(token.Letter)
		if !ok {
			return "", false
		}
		switch letter {
		case 'H':
			return SubgridMoveLeft, true
		case 'J':
			return SubgridMoveDown, true
		case 'K':
			return SubgridMoveUp, true
		case 'L':
			return SubgridMoveRight, true
		}
	}
	return "", false
}

func arrowSubgridMoveDirection(keysym KeySym) (SubgridMoveDirection, bool) {
	switch keysym {
	case "Left", "KP_Left":
		return SubgridMoveLeft, true
	case "Down", "KP_Down":
		return SubgridMoveDown, true
	case "Up", "KP_Up":
		return SubgridMoveUp, true
	case "Right", "KP_Right":
		return SubgridMoveRight, true
	default:
		return "", false
	}
}

func subgridNavigationStep(total int, count int) int {
	if total <= 0 || count <= 0 {
		return 1
	}
	step := int(math.Round(float64(total) / float64(count)))
	if step < 1 {
		return 1
	}
	return step
}

func clampPointToRect(point Point, bounds Rect) Point {
	if bounds.Width <= 0 || bounds.Height <= 0 {
		return point
	}
	return Point{
		X: clampInt(point.X, bounds.X, bounds.X+bounds.Width-1),
		Y: clampInt(point.Y, bounds.Y, bounds.Y+bounds.Height-1),
	}
}

func subgridAxisIndexForPoint(offset int, total int, count int) int {
	if count <= 1 || total <= 0 {
		return 0
	}
	if offset <= 0 {
		return 0
	}
	if offset >= total {
		return count - 1
	}
	for i := 0; i < count; i++ {
		_, end, err := axisSegment(total, count, i)
		if err != nil {
			return 0
		}
		if offset < end {
			return i
		}
	}
	return count - 1
}

func SubgridCellBounds(mainCell Rect, xCount int, yCount int, col int, row int) (Rect, error) {
	if mainCell.Width <= 0 || mainCell.Height <= 0 {
		return Rect{}, fmt.Errorf("main grid cell has invalid size %dx%d", mainCell.Width, mainCell.Height)
	}
	if xCount <= 0 || yCount <= 0 {
		return Rect{}, fmt.Errorf("subgrid cell counts must be positive, got %dx%d", xCount, yCount)
	}
	if col < 0 || col >= xCount || row < 0 || row >= yCount {
		return Rect{}, fmt.Errorf("subgrid cell out of range: col=%d row=%d size=%dx%d", col, row, xCount, yCount)
	}

	x0, x1, err := axisSegment(mainCell.Width, xCount, col)
	if err != nil {
		return Rect{}, err
	}
	y0, y1, err := axisSegment(mainCell.Height, yCount, row)
	if err != nil {
		return Rect{}, err
	}
	return Rect{
		X:      mainCell.X + x0,
		Y:      mainCell.Y + y0,
		Width:  x1 - x0,
		Height: y1 - y0,
	}, nil
}

type SubgridRefinementMode string

const (
	SubgridRefinementXY    SubgridRefinementMode = "xy"
	SubgridRefinementXOnly SubgridRefinementMode = "x_only"
)

type SubgridRefinementFSM struct {
	mainCell  Rect
	xCount    int
	yCount    int
	input     []byte
	committed bool
}

type SubgridRefinementResult struct {
	Changed   bool
	Committed *SubgridRefinementCommit
}

type SubgridRefinementCommit struct {
	Mode         SubgridRefinementMode
	Column       int
	Row          int
	ColumnLetter byte
	RowLetter    byte
	Bounds       Rect
	Point        Point
}

func NewSubgridRefinementFSM(mainCell Rect, xCount int, yCount int) *SubgridRefinementFSM {
	fsm := &SubgridRefinementFSM{
		mainCell: mainCell,
		xCount:   xCount,
		yCount:   yCount,
	}
	fsm.Reset()
	return fsm
}

func (f *SubgridRefinementFSM) Reset() {
	if f == nil {
		return
	}
	f.input = f.input[:0]
	f.committed = false
}

func (f *SubgridRefinementFSM) Input() string {
	if f == nil {
		return ""
	}
	return string(f.input)
}

func (f *SubgridRefinementFSM) SelectedColumn() (int, bool) {
	if f == nil || len(f.input) == 0 {
		return noSubgridAxis, false
	}
	return int(f.input[0] - 'A'), true
}

func (f *SubgridRefinementFSM) HandleToken(token KeyboardToken) SubgridRefinementResult {
	if f == nil {
		return SubgridRefinementResult{}
	}
	if f.committed {
		return SubgridRefinementResult{}
	}
	if tokenHasKeyboardCommand(token, KeyboardCommandBackspace) {
		return f.backspace()
	}
	if tokenHasKeyboardCommand(token, KeyboardCommandCommitPartial) {
		return f.commitXOnly()
	}
	if token.Kind != KeyboardTokenLetter {
		return SubgridRefinementResult{}
	}
	letter, ok := normalizedGridLetter(token.Letter)
	if !ok || len(f.input) >= 2 {
		return SubgridRefinementResult{}
	}

	if len(f.input) == 0 {
		if !f.letterInXRange(letter) {
			return SubgridRefinementResult{}
		}
		f.input = append(f.input, letter)
		return SubgridRefinementResult{Changed: true}
	}

	if !f.letterInYRange(letter) {
		return SubgridRefinementResult{}
	}
	f.input = append(f.input, letter)
	commit, err := f.xyCommit()
	if err != nil {
		return SubgridRefinementResult{Changed: true}
	}
	f.committed = true
	return SubgridRefinementResult{Changed: true, Committed: commit}
}

func (f *SubgridRefinementFSM) backspace() SubgridRefinementResult {
	if len(f.input) == 0 {
		return SubgridRefinementResult{}
	}
	f.input = f.input[:len(f.input)-1]
	return SubgridRefinementResult{Changed: true}
}

func (f *SubgridRefinementFSM) commitXOnly() SubgridRefinementResult {
	if len(f.input) != 1 {
		return SubgridRefinementResult{}
	}
	col := int(f.input[0] - 'A')
	if col < 0 || col >= f.xCount {
		return SubgridRefinementResult{}
	}

	x0, x1, err := axisSegment(f.mainCell.Width, f.xCount, col)
	if err != nil {
		return SubgridRefinementResult{}
	}
	bounds := Rect{
		X:      f.mainCell.X + x0,
		Y:      f.mainCell.Y,
		Width:  x1 - x0,
		Height: f.mainCell.Height,
	}
	f.committed = true
	return SubgridRefinementResult{
		Committed: &SubgridRefinementCommit{
			Mode:         SubgridRefinementXOnly,
			Column:       col,
			Row:          noSubgridAxis,
			ColumnLetter: f.input[0],
			Bounds:       bounds,
			Point: Point{
				X: bounds.Center().X,
				Y: f.mainCell.Center().Y,
			},
		},
	}
}

func (f *SubgridRefinementFSM) xyCommit() (*SubgridRefinementCommit, error) {
	if len(f.input) != 2 {
		return nil, fmt.Errorf("subgrid XY commit requires two letters, got %d", len(f.input))
	}
	col := int(f.input[0] - 'A')
	row := int(f.input[1] - 'A')
	bounds, err := SubgridCellBounds(f.mainCell, f.xCount, f.yCount, col, row)
	if err != nil {
		return nil, err
	}
	return &SubgridRefinementCommit{
		Mode:         SubgridRefinementXY,
		Column:       col,
		Row:          row,
		ColumnLetter: f.input[0],
		RowLetter:    f.input[1],
		Bounds:       bounds,
		Point:        bounds.Center(),
	}, nil
}

func (f *SubgridRefinementFSM) letterInXRange(letter byte) bool {
	index := int(letter) - int('A')
	return f.xCount > 0 && index >= 0 && index < f.xCount
}

func (f *SubgridRefinementFSM) letterInYRange(letter byte) bool {
	index := int(letter) - int('A')
	return f.yCount > 0 && index >= 0 && index < f.yCount
}
