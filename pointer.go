package main

import (
	"context"
	"fmt"
)

func EmitPointerClick(ctx context.Context, pointer PointerSynthesizer, output Monitor, p Point, button PointerButton) error {
	if pointer == nil {
		return fmt.Errorf("pointer synthesizer is required")
	}
	if err := pointer.MoveAbsolute(ctx, p.X, p.Y, output); err != nil {
		return err
	}
	switch button {
	case PointerButtonLeft:
		return pointer.LeftClick(ctx)
	case PointerButtonRight:
		return pointer.RightClick(ctx)
	default:
		return fmt.Errorf("unsupported pointer button %q", button)
	}
}

func EmitPointerDoubleClick(ctx context.Context, pointer PointerSynthesizer, output Monitor, p Point, button PointerButton) error {
	if pointer == nil {
		return fmt.Errorf("pointer synthesizer is required")
	}
	if button != PointerButtonLeft {
		return fmt.Errorf("double-click is only supported for the left button, got %q", button)
	}
	if err := pointer.MoveAbsolute(ctx, p.X, p.Y, output); err != nil {
		return err
	}
	return pointer.DoubleClick(ctx)
}

func pointerMotionFromLogical(clock Clock, output Monitor, x int, y int, mode PointerMappingMode, layout Rect) (PointerMotion, error) {
	if clock == nil {
		clock = systemClock{}
	}
	if output.Name == "" {
		return PointerMotion{}, fmt.Errorf("pointer output name is required")
	}
	if output.Width <= 0 || output.Height <= 0 {
		return PointerMotion{}, fmt.Errorf("pointer output %q has invalid size %dx%d", output.Name, output.Width, output.Height)
	}
	if !output.ContainsLocal(Point{X: x, Y: y}) {
		return PointerMotion{}, fmt.Errorf("pointer target outside output %q: %d,%d not in %dx%d", output.Name, x, y, output.Width, output.Height)
	}

	protocolX, protocolY, xExtent, yExtent, err := mapPointerCoordinates(output, x, y, mode, layout)
	if err != nil {
		return PointerMotion{}, err
	}
	return PointerMotion{
		OutputName: output.Name,
		X:          x,
		Y:          y,
		ProtocolX:  protocolX,
		ProtocolY:  protocolY,
		XExtent:    xExtent,
		YExtent:    yExtent,
		Mapping:    mode,
		Time:       clock.Now(),
	}, nil
}

func mapPointerCoordinates(output Monitor, x int, y int, mode PointerMappingMode, layout Rect) (uint32, uint32, uint32, uint32, error) {
	switch mode {
	case PointerMappingWithOutput:
		return uint32(x), uint32(y), uint32(output.Width), uint32(output.Height), nil
	case PointerMappingFallback:
		if layout.Width <= 0 || layout.Height <= 0 {
			layout = Rect{X: output.X, Y: output.Y, Width: output.Width, Height: output.Height}
		}
		virtualX := output.X + x
		virtualY := output.Y + y
		protocolX := virtualX - layout.X
		protocolY := virtualY - layout.Y
		if protocolX < 0 || protocolY < 0 {
			return 0, 0, 0, 0, fmt.Errorf("pointer fallback target %d,%d falls outside virtual layout %+v", virtualX, virtualY, layout)
		}
		if protocolX > layout.Width || protocolY > layout.Height {
			return 0, 0, 0, 0, fmt.Errorf("pointer fallback target %d,%d exceeds virtual layout %+v", virtualX, virtualY, layout)
		}
		return uint32(protocolX), uint32(protocolY), uint32(layout.Width), uint32(layout.Height), nil
	default:
		return 0, 0, 0, 0, fmt.Errorf("unsupported pointer mapping mode %q", mode)
	}
}

func MonitorLayoutBounds(monitors []Monitor) (Rect, error) {
	if len(monitors) == 0 {
		return Rect{}, fmt.Errorf("cannot compute monitor layout bounds without outputs")
	}

	maxInt := int(^uint(0) >> 1)
	minInt := -maxInt - 1
	minX := maxInt
	minY := maxInt
	maxX := minInt
	maxY := minInt
	for _, monitor := range monitors {
		if monitor.Width <= 0 || monitor.Height <= 0 {
			return Rect{}, fmt.Errorf("monitor %q has invalid size %dx%d", monitor.Name, monitor.Width, monitor.Height)
		}
		minX = min(minX, monitor.X)
		minY = min(minY, monitor.Y)
		maxX = max(maxX, monitor.X+monitor.Width)
		maxY = max(maxY, monitor.Y+monitor.Height)
	}
	return Rect{X: minX, Y: minY, Width: maxX - minX, Height: maxY - minY}, nil
}
