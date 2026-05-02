package main

import "testing"

func TestSubgridAxisCountFormula(t *testing.T) {
	for _, tt := range []struct {
		name      string
		axisSize  int
		pixelSize int
		want      int
	}{
		{name: "zero axis still has one cell", axisSize: 0, pixelSize: 5, want: 1},
		{name: "below half rounds to one by lower cap", axisSize: 2, pixelSize: 5, want: 1},
		{name: "exact target", axisSize: 20, pixelSize: 5, want: 4},
		{name: "rounds down", axisSize: 21, pixelSize: 5, want: 4},
		{name: "rounds up", axisSize: 23, pixelSize: 5, want: 5},
		{name: "capped at alphabet", axisSize: 300, pixelSize: 5, want: 26},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SubgridAxisCount(tt.axisSize, tt.pixelSize)
			if err != nil {
				t.Fatalf("subgrid axis count: %v", err)
			}
			if got != tt.want {
				t.Fatalf("count = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSubgridAxisCountRejectsInvalidInputs(t *testing.T) {
	for _, tt := range []struct {
		name      string
		axisSize  int
		pixelSize int
	}{
		{name: "negative axis", axisSize: -1, pixelSize: 5},
		{name: "zero pixel size", axisSize: 10, pixelSize: 0},
		{name: "negative pixel size", axisSize: 10, pixelSize: -5},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := SubgridAxisCount(tt.axisSize, tt.pixelSize); err == nil {
				t.Fatalf("SubgridAxisCount(%d, %d) succeeded, want error", tt.axisSize, tt.pixelSize)
			}
		})
	}
}

func TestShiftOrClipRectToBounds(t *testing.T) {
	bounds := Rect{X: 0, Y: 0, Width: 100, Height: 80}
	for _, tt := range []struct {
		name string
		rect Rect
		want Rect
	}{
		{name: "already visible", rect: Rect{X: 30, Y: 20, Width: 20, Height: 10}, want: Rect{X: 30, Y: 20, Width: 20, Height: 10}},
		{name: "shift from top left", rect: Rect{X: -10, Y: -5, Width: 30, Height: 20}, want: Rect{X: 0, Y: 0, Width: 30, Height: 20}},
		{name: "shift from bottom right", rect: Rect{X: 90, Y: 70, Width: 30, Height: 20}, want: Rect{X: 70, Y: 60, Width: 30, Height: 20}},
		{name: "clip oversized", rect: Rect{X: -10, Y: -5, Width: 120, Height: 90}, want: Rect{X: 0, Y: 0, Width: 100, Height: 80}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShiftOrClipRectToBounds(tt.rect, bounds); got != tt.want {
				t.Fatalf("placement = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestNewSubgridGeometryUsesCellSizeCountsAndVisiblePlacement(t *testing.T) {
	monitor := Monitor{Name: "test", Width: 100, Height: 80}
	mainCell := Rect{X: 90, Y: 70, Width: 10, Height: 10}

	geometry, err := NewSubgridGeometry(monitor, mainCell, mainCell.Center(), 4)
	if err != nil {
		t.Fatalf("new subgrid geometry: %v", err)
	}
	if geometry.XCount != 3 || geometry.YCount != 3 {
		t.Fatalf("subgrid counts = %dx%d, want 3x3", geometry.XCount, geometry.YCount)
	}
	if geometry.Display != mainCell {
		t.Fatalf("display rect = %+v, want selected cell bounds %+v", geometry.Display, mainCell)
	}
}

func TestSubgridNavigationFSMUsesVimKeysWithoutVisibleInput(t *testing.T) {
	mainCell := Rect{X: 10, Y: 20, Width: 20, Height: 20}
	bounds := Rect{X: 0, Y: 0, Width: 80, Height: 80}
	fsm := NewSubgridNavigationFSM(mainCell, bounds, 4, 4, mainCell.Center())

	if got, want := fsm.Point(), mainCell.Center(); got != want {
		t.Fatalf("initial point = %+v, want %+v", got, want)
	}

	left := fsm.HandleToken(KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'H'})
	if !left.Changed || left.Direction != SubgridMoveLeft || left.Column != 1 || left.Row != 2 || left.Point != (Point{X: 15, Y: 30}) {
		t.Fatalf("H result = %+v, want left to column 1 with unchanged Y", left)
	}

	down := fsm.HandleToken(KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'J'})
	if !down.Changed || down.Direction != SubgridMoveDown || down.Column != 1 || down.Row != 3 || down.Point != (Point{X: 15, Y: 35}) {
		t.Fatalf("J result = %+v, want down to row 3 with unchanged X", down)
	}

	up := fsm.HandleToken(KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'K'})
	if !up.Changed || up.Direction != SubgridMoveUp || up.Column != 1 || up.Row != 2 || up.Point != (Point{X: 15, Y: 30}) {
		t.Fatalf("K result = %+v, want up to row 2 with unchanged X", up)
	}

	right := fsm.HandleToken(KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'L'})
	if !right.Changed || right.Direction != SubgridMoveRight || right.Column != 2 || right.Row != 2 || right.Point != (Point{X: 20, Y: 30}) {
		t.Fatalf("L result = %+v, want right to column 2 with unchanged Y", right)
	}
}

func TestSubgridNavigationFSMUsesArrowKeys(t *testing.T) {
	mainCell := Rect{X: 10, Y: 20, Width: 20, Height: 20}
	bounds := Rect{X: 0, Y: 0, Width: 80, Height: 80}
	fsm := NewSubgridNavigationFSM(mainCell, bounds, 4, 4, mainCell.Center())

	left := fsm.HandleToken(KeyboardToken{Kind: KeyboardTokenCommand, KeySym: "Left"})
	if !left.Changed || left.Direction != SubgridMoveLeft || left.Point != (Point{X: 15, Y: 30}) {
		t.Fatalf("Left result = %+v, want left movement", left)
	}
	down := fsm.HandleToken(KeyboardToken{Kind: KeyboardTokenCommand, KeySym: "Down"})
	if !down.Changed || down.Direction != SubgridMoveDown || down.Point != (Point{X: 15, Y: 35}) {
		t.Fatalf("Down result = %+v, want down movement", down)
	}
	up := fsm.HandleToken(KeyboardToken{Kind: KeyboardTokenCommand, KeySym: "Up"})
	if !up.Changed || up.Direction != SubgridMoveUp || up.Point != (Point{X: 15, Y: 30}) {
		t.Fatalf("Up result = %+v, want up movement", up)
	}
	right := fsm.HandleToken(KeyboardToken{Kind: KeyboardTokenCommand, KeySym: "Right"})
	if !right.Changed || right.Direction != SubgridMoveRight || right.Point != (Point{X: 20, Y: 30}) {
		t.Fatalf("Right result = %+v, want right movement", right)
	}
}

func TestSubgridNavigationFSMCanMoveBeyondSelectedCellUntilMonitorEdge(t *testing.T) {
	mainCell := Rect{X: 10, Y: 10, Width: 10, Height: 10}
	bounds := Rect{X: 0, Y: 0, Width: 40, Height: 40}
	fsm := NewSubgridNavigationFSM(mainCell, bounds, 2, 2, mainCell.Center())

	for i := 0; i < 4; i++ {
		result := fsm.HandleToken(KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'L'})
		if !result.Changed {
			t.Fatalf("right movement %d was ignored", i+1)
		}
	}
	if got, want := fsm.Point(), (Point{X: 35, Y: 15}); got != want {
		t.Fatalf("point after moving beyond selected cell = %+v, want %+v", got, want)
	}
	if got := fsm.HandleToken(KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'L'}); !got.Changed || got.Point != (Point{X: 39, Y: 15}) {
		t.Fatalf("right movement clamped to monitor edge = %+v, want changed to x=39", got)
	}
	if got := fsm.HandleToken(KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'L'}); got.Changed {
		t.Fatalf("right movement at monitor edge = %+v, want ignored", got)
	}
}

func TestSubgridNavigationFSMIgnoresNonMovementAndMonitorEdgeKeys(t *testing.T) {
	mainCell := Rect{X: 0, Y: 0, Width: 9, Height: 9}
	bounds := Rect{X: 0, Y: 0, Width: 20, Height: 20}
	fsm := NewSubgridNavigationFSM(mainCell, bounds, 3, 3, Point{X: 0, Y: 0})

	if result := fsm.HandleToken(KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'A'}); result.Changed {
		t.Fatalf("non-HJKL result = %+v, want ignored", result)
	}
	if result := fsm.HandleToken(KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'H'}); result.Changed {
		t.Fatalf("left at first column result = %+v, want ignored", result)
	}
	if result := fsm.HandleToken(KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'K'}); result.Changed {
		t.Fatalf("up at first row result = %+v, want ignored", result)
	}
}

func TestSubgridRefinementFSMFullXYCommit(t *testing.T) {
	mainCell := Rect{X: 10, Y: 20, Width: 20, Height: 30}
	fsm := NewSubgridRefinementFSM(mainCell, 4, 6)

	first := fsm.HandleToken(KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'B'})
	if !first.Changed || first.Committed != nil {
		t.Fatalf("first subgrid letter result = %+v, want changed without commit", first)
	}
	if got := fsm.Input(); got != "B" {
		t.Fatalf("input = %q, want B", got)
	}

	second := fsm.HandleToken(KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'C'})
	if !second.Changed || second.Committed == nil {
		t.Fatalf("second subgrid letter result = %+v, want XY commit", second)
	}
	wantBounds := Rect{X: 15, Y: 30, Width: 5, Height: 5}
	want := SubgridRefinementCommit{
		Mode:         SubgridRefinementXY,
		Column:       1,
		Row:          2,
		ColumnLetter: 'B',
		RowLetter:    'C',
		Bounds:       wantBounds,
		Point:        wantBounds.Center(),
	}
	if *second.Committed != want {
		t.Fatalf("commit = %+v, want %+v", *second.Committed, want)
	}
}

func TestSubgridRefinementFSMXOnlyCommitWithTab(t *testing.T) {
	mainCell := Rect{X: 10, Y: 20, Width: 20, Height: 30}
	fsm := NewSubgridRefinementFSM(mainCell, 4, 6)
	fsm.HandleToken(KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'D'})

	result := fsm.HandleToken(KeyboardToken{Kind: KeyboardTokenCommand, Commands: []KeyboardCommand{KeyboardCommandCommitPartial}})
	if result.Committed == nil {
		t.Fatalf("Tab result = %+v, want X-only commit", result)
	}
	want := SubgridRefinementCommit{
		Mode:         SubgridRefinementXOnly,
		Column:       3,
		Row:          noSubgridAxis,
		ColumnLetter: 'D',
		Bounds:       Rect{X: 25, Y: 20, Width: 5, Height: 30},
		Point:        Point{X: 27, Y: mainCell.Center().Y},
	}
	if *result.Committed != want {
		t.Fatalf("commit = %+v, want %+v", *result.Committed, want)
	}
}

func TestSubgridRefinementFSMIgnoresOutOfRangeLetters(t *testing.T) {
	fsm := NewSubgridRefinementFSM(Rect{X: 0, Y: 0, Width: 9, Height: 6}, 3, 2)

	if result := fsm.HandleToken(KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'D'}); result.Changed || result.Committed != nil {
		t.Fatalf("out-of-range X result = %+v, want ignored", result)
	}
	fsm.HandleToken(KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'C'})
	if result := fsm.HandleToken(KeyboardToken{Kind: KeyboardTokenLetter, Letter: 'C'}); result.Changed || result.Committed != nil {
		t.Fatalf("out-of-range Y result = %+v, want ignored", result)
	}
	if got := fsm.Input(); got != "C" {
		t.Fatalf("input after ignored Y = %q, want C", got)
	}
}

func TestRenderSubgridOverlayLabelsOnlyTopAndLeftEdges(t *testing.T) {
	config := DefaultConfig()
	atlas, err := NewFontAtlasFromConfig(config)
	if err != nil {
		t.Fatalf("font atlas: %v", err)
	}
	buffer, err := NewARGBBuffer(80, 60)
	if err != nil {
		t.Fatalf("new buffer: %v", err)
	}
	geometry := SubgridGeometry{
		MainCell: Rect{X: 30, Y: 20, Width: 20, Height: 20},
		Cursor:   Point{X: 40, Y: 30},
		Display:  Rect{X: 30, Y: 20, Width: 20, Height: 20},
		XCount:   4,
		YCount:   4,
	}
	if err := RenderSubgridOverlay(buffer, SubgridRenderOptions{
		Geometry:   geometry,
		Appearance: config.Appearance,
		FontAtlas:  atlas,
	}); err != nil {
		t.Fatalf("render subgrid: %v", err)
	}

	if got := argbAt(buffer, 5, 5); got != 0 {
		t.Fatalf("pixel outside subgrid = %#x, want transparent", got)
	}
	if got := argbAt(buffer, 35, 25); got == 0 {
		t.Fatalf("pixel inside subgrid is transparent, want visible grid/background")
	}

	topY0, topY1, err := axisSegment(geometry.Display.Height, geometry.YCount, 0)
	if err != nil {
		t.Fatalf("top segment: %v", err)
	}
	leftX0, leftX1, err := axisSegment(geometry.Display.Width, geometry.XCount, 0)
	if err != nil {
		t.Fatalf("left segment: %v", err)
	}
	topBand := Rect{X: geometry.Display.X, Y: geometry.Display.Y + topY0, Width: geometry.Display.Width, Height: topY1 - topY0}
	leftBand := Rect{X: geometry.Display.X + leftX0, Y: geometry.Display.Y, Width: leftX1 - leftX0, Height: geometry.Display.Height}
	assertEdgeHasLabelInk(t, buffer, topBand, "subgrid top")
	assertEdgeHasLabelInk(t, buffer, leftBand, "subgrid left")

	for y := geometry.Display.Y; y < geometry.Display.Y+geometry.Display.Height; y++ {
		for x := geometry.Display.X; x < geometry.Display.X+geometry.Display.Width; x++ {
			if !isGridLabelForegroundPixel(argbAt(buffer, x, y)) {
				continue
			}
			onTop := y >= topBand.Y && y < topBand.Y+topBand.Height
			onLeft := x >= leftBand.X && x < leftBand.X+leftBand.Width
			if !onTop && !onLeft {
				t.Fatalf("opaque label ink escaped top/left subgrid edges at %d,%d", x, y)
			}
		}
	}
}
