package main

import (
	"context"
	"encoding/binary"
	"testing"
	"time"
)

func TestPointerCoordinateTransformsForScaledOffsetFocusedMonitor(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC))
	focused := fakeFocusedMonitorFixture()
	if focused.X == 0 && focused.Y == 0 {
		t.Fatalf("focused fixture must have non-zero virtual origin: %+v", focused)
	}
	if focused.Scale == 1.0 {
		t.Fatalf("focused fixture must have scale != 1.0: %+v", focused)
	}

	layout, err := MonitorLayoutBounds(fakeMonitorFixtures())
	if err != nil {
		t.Fatalf("monitor layout bounds: %v", err)
	}
	local := Point{X: 50, Y: 75}

	withOutput, err := pointerMotionFromLogical(clock, focused, local.X, local.Y, PointerMappingWithOutput, Rect{})
	if err != nil {
		t.Fatalf("with-output motion transform: %v", err)
	}
	if withOutput.ProtocolX != uint32(local.X) || withOutput.ProtocolY != uint32(local.Y) {
		t.Fatalf("with-output protocol coords = %d,%d, want focused-output-local %d,%d", withOutput.ProtocolX, withOutput.ProtocolY, local.X, local.Y)
	}
	if withOutput.XExtent != uint32(focused.Width) || withOutput.YExtent != uint32(focused.Height) {
		t.Fatalf("with-output extents = %dx%d, want focused output %dx%d", withOutput.XExtent, withOutput.YExtent, focused.Width, focused.Height)
	}

	fallback, err := pointerMotionFromLogical(clock, focused, local.X, local.Y, PointerMappingFallback, layout)
	if err != nil {
		t.Fatalf("fallback motion transform: %v", err)
	}
	virtual := focused.LocalToVirtual(local)
	wantFallbackX := virtual.X - layout.X
	wantFallbackY := virtual.Y - layout.Y
	if fallback.ProtocolX != uint32(wantFallbackX) || fallback.ProtocolY != uint32(wantFallbackY) {
		t.Fatalf("fallback protocol coords = %d,%d, want virtual-layout %d,%d from local %+v origin %+v layout %+v", fallback.ProtocolX, fallback.ProtocolY, wantFallbackX, wantFallbackY, local, Point{X: focused.X, Y: focused.Y}, layout)
	}
	if fallback.XExtent != uint32(layout.Width) || fallback.YExtent != uint32(layout.Height) {
		t.Fatalf("fallback extents = %dx%d, want virtual layout %dx%d", fallback.XExtent, fallback.YExtent, layout.Width, layout.Height)
	}
}

func TestLogicalARGBBufferScalingExpandsLogicalPixels(t *testing.T) {
	focused := fakeFocusedMonitorFixture()
	scale := waylandBufferScale(focused.Scale)
	if scale <= 1 {
		t.Fatalf("focused fixture scale %g mapped to buffer scale %d, want scaled buffer", focused.Scale, scale)
	}

	logical, err := NewARGBBuffer(2, 2)
	if err != nil {
		t.Fatalf("new logical buffer: %v", err)
	}
	pixels := []uint32{
		0xff102030, 0xff405060,
		0xff708090, 0xffa0b0c0,
	}
	copy(logical.Pixels, pixels)

	physicalWidth := logical.Width * scale
	physicalHeight := logical.Height * scale
	dst := make([]byte, physicalWidth*physicalHeight*4)
	copyARGBToWaylandSHM(dst, logical, scale)

	for y := 0; y < physicalHeight; y++ {
		for x := 0; x < physicalWidth; x++ {
			want := pixels[(y/scale)*logical.Stride+(x/scale)]
			if got := littleEndianARGBAt(dst, physicalWidth, x, y); got != want {
				t.Fatalf("physical pixel %d,%d = %#x, want logical pixel %d,%d %#x", x, y, got, x/scale, y/scale, want)
			}
		}
	}
}

func TestScaledFocusedMonitorGridSnapshotUsesLogicalCoordinates(t *testing.T) {
	ctx := context.Background()
	config := DefaultConfig()
	atlas, err := NewFontAtlasFromConfig(config)
	if err != nil {
		t.Fatalf("font atlas: %v", err)
	}
	focused := fakeFocusedMonitorFixture()
	wayland := newFakeWaylandBackend(fakeMonitorFixtures()...)
	renderer := &fakeRendererSink{}
	controller := NewDaemonController(DaemonDeps{
		MonitorLookup: &fakeFocusedMonitorLookup{monitor: focused},
		Overlay:       wayland,
		Renderer:      renderer,
		Config:        &config,
		FontAtlas:     atlas,
	})
	if err := controller.Show(ctx); err != nil {
		t.Fatalf("show scaled focused monitor grid: %v", err)
	}

	presentations := renderer.Presentations()
	if len(presentations) != 1 {
		t.Fatalf("renderer presentations = %d, want 1", len(presentations))
	}
	presentation := presentations[0]
	if presentation.Width != focused.Width || presentation.Height != focused.Height {
		t.Fatalf("rendered buffer size = %dx%d, want focused logical size %dx%d", presentation.Width, presentation.Height, focused.Width, focused.Height)
	}

	buffer, err := NewARGBBuffer(focused.Width, focused.Height)
	if err != nil {
		t.Fatalf("new expected buffer: %v", err)
	}
	if err := RenderMainGridOverlay(buffer, MainGridRenderOptions{
		GridSize:   config.Grid.Size,
		Appearance: config.Appearance,
		FontAtlas:  atlas,
		HUD:        DefaultMainGridHUD,
	}); err != nil {
		t.Fatalf("render expected scaled-monitor grid: %v", err)
	}

	const wantHash = "0668f39dab34ee13dd9a74e0688391a025d16e9fce92c34ee4cbba0e98b00657"
	if got := mustARGBHash(t, buffer); got != wantHash {
		t.Fatalf("scaled focused-monitor grid snapshot hash = %s, want %s", got, wantHash)
	}
	if presentation.Hash != wantHash {
		t.Fatalf("renderer hash = %s, want scaled grid snapshot %s", presentation.Hash, wantHash)
	}

	lineColor := uint32(opacityToAlpha(config.Appearance.GridOpacity)<<24) | 0x00ffffff
	vertical := axisBoundary(focused.Width, config.Grid.Size, 13)
	rowY0, rowY1, err := axisSegment(focused.Height, config.Grid.Size, 13)
	if err != nil {
		t.Fatalf("middle row segment: %v", err)
	}
	if got := argbAt(buffer, vertical, centeredInSpan(rowY0, rowY1, 1)); got != lineColor {
		t.Fatalf("logical vertical grid line pixel = %#x, want %#x", got, lineColor)
	}
	horizontal := axisBoundary(focused.Height, config.Grid.Size, 13)
	colX0, colX1, err := axisSegment(focused.Width, config.Grid.Size, 13)
	if err != nil {
		t.Fatalf("middle column segment: %v", err)
	}
	if got := argbAt(buffer, centeredInSpan(colX0, colX1, 1), horizontal); got != lineColor {
		t.Fatalf("logical horizontal grid line pixel = %#x, want %#x", got, lineColor)
	}

	topY0, topY1, err := axisSegment(focused.Height, config.Grid.Size, 0)
	if err != nil {
		t.Fatalf("top row segment: %v", err)
	}
	bottomY0, bottomY1, err := axisSegment(focused.Height, config.Grid.Size, config.Grid.Size-1)
	if err != nil {
		t.Fatalf("bottom row segment: %v", err)
	}
	leftX0, leftX1, err := axisSegment(focused.Width, config.Grid.Size, 0)
	if err != nil {
		t.Fatalf("left column segment: %v", err)
	}
	rightX0, rightX1, err := axisSegment(focused.Width, config.Grid.Size, config.Grid.Size-1)
	if err != nil {
		t.Fatalf("right column segment: %v", err)
	}
	assertEdgeHasLabelInk(t, buffer, Rect{X: 0, Y: topY0, Width: focused.Width, Height: topY1 - topY0}, "scaled top")
	assertEdgeHasLabelInk(t, buffer, Rect{X: 0, Y: bottomY0, Width: focused.Width, Height: bottomY1 - bottomY0}, "scaled bottom")
	assertEdgeHasLabelInk(t, buffer, Rect{X: leftX0, Y: 0, Width: leftX1 - leftX0, Height: focused.Height}, "scaled left")
	assertEdgeHasLabelInk(t, buffer, Rect{X: rightX0, Y: 0, Width: rightX1 - rightX0, Height: focused.Height}, "scaled right")
}

func littleEndianARGBAt(data []byte, width int, x int, y int) uint32 {
	offset := ((y * width) + x) * 4
	return binary.LittleEndian.Uint32(data[offset : offset+4])
}
