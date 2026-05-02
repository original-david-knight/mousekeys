package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	wlclient "github.com/rajveermalviya/go-wayland/wayland/client"

	"mousekeys/internal/waylandprotocols/wlrvirtualpointer"
)

const (
	linuxInputButtonLeft  uint32 = 0x110
	linuxInputButtonRight uint32 = 0x111
)

type WaylandVirtualPointerSynthesizer struct {
	client *WaylandClient
	clock  Clock

	mu                sync.Mutex
	pointer           *wlrvirtualpointer.VirtualPointer
	pointerMode       PointerMappingMode
	pointerOutputName string
	current           PointerMotion
	haveCurrent       bool
}

func NewWaylandVirtualPointerSynthesizer(client *WaylandClient, clock Clock) *WaylandVirtualPointerSynthesizer {
	if clock == nil {
		clock = systemClock{}
	}
	return &WaylandVirtualPointerSynthesizer{
		client: client,
		clock:  clock,
	}
}

func (s *WaylandVirtualPointerSynthesizer) MoveAbsolute(ctx context.Context, x int, y int, output Monitor) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("Wayland virtual pointer is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	motion, outputHandle, err := s.resolveMotion(ctx, x, y, output)
	if err != nil {
		return err
	}

	s.client.StartDispatchLoop(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.client.withProtocolLock(func() error {
		pointer, err := s.ensurePointerProtocolLocked(motion.Mapping, motion.OutputName, outputHandle)
		if err != nil {
			return err
		}
		if err := pointer.MotionAbsolute(
			waylandPointerTime(motion.Time),
			motion.ProtocolX,
			motion.ProtocolY,
			motion.XExtent,
			motion.YExtent,
		); err != nil {
			return fmt.Errorf("send virtual pointer motion_absolute: %w", err)
		}
		if err := pointer.Frame(); err != nil {
			return fmt.Errorf("send virtual pointer motion frame: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}

	s.current = motion
	s.haveCurrent = true
	return nil
}

func (s *WaylandVirtualPointerSynthesizer) LeftClick(ctx context.Context) error {
	return s.click(ctx, PointerButtonLeft)
}

func (s *WaylandVirtualPointerSynthesizer) RightClick(ctx context.Context) error {
	return s.click(ctx, PointerButtonRight)
}

func (s *WaylandVirtualPointerSynthesizer) DoubleClick(ctx context.Context) error {
	if err := s.click(ctx, PointerButtonLeft); err != nil {
		return err
	}
	return s.click(ctx, PointerButtonLeft)
}

func (s *WaylandVirtualPointerSynthesizer) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pointer == nil {
		return nil
	}
	return s.client.withProtocolLock(func() error {
		if s.pointer == nil {
			return nil
		}
		err := s.pointer.Destroy()
		s.pointer = nil
		s.haveCurrent = false
		if err != nil {
			return fmt.Errorf("destroy virtual pointer: %w", err)
		}
		return nil
	})
}

func (s *WaylandVirtualPointerSynthesizer) resolveMotion(ctx context.Context, x int, y int, output Monitor) (PointerMotion, *wlclient.Output, error) {
	mode := PointerMappingFallback
	var outputHandle *wlclient.Output
	if s.client.VirtualPointerManagerVersion() >= 2 {
		if handle, ok := s.client.OutputHandle(output.Name); ok {
			mode = PointerMappingWithOutput
			outputHandle = handle
		}
	}

	layout := Rect{}
	if mode == PointerMappingFallback {
		outputs, err := s.client.Outputs(ctx)
		if err != nil {
			return PointerMotion{}, nil, fmt.Errorf("read Wayland outputs for virtual-pointer fallback layout: %w", err)
		}
		outputs = ensureMonitorInLayout(outputs, output)
		layout, err = MonitorLayoutBounds(outputs)
		if err != nil {
			return PointerMotion{}, nil, err
		}
	}

	motion, err := pointerMotionFromLogical(s.clock, output, x, y, mode, layout)
	if err != nil {
		return PointerMotion{}, nil, err
	}
	return motion, outputHandle, nil
}

func (s *WaylandVirtualPointerSynthesizer) ensurePointerProtocolLocked(mode PointerMappingMode, outputName string, outputHandle *wlclient.Output) (*wlrvirtualpointer.VirtualPointer, error) {
	if s.pointer != nil && s.pointerMode == mode && (mode == PointerMappingFallback || s.pointerOutputName == outputName) {
		return s.pointer, nil
	}

	if s.pointer != nil {
		if err := s.pointer.Destroy(); err != nil {
			return nil, fmt.Errorf("destroy replaced virtual pointer: %w", err)
		}
		s.pointer = nil
	}

	manager := s.client.VirtualPointerManager()
	if manager == nil {
		return nil, fmt.Errorf("wlr virtual-pointer manager is not bound")
	}
	seat := s.client.Seat()
	if seat == nil {
		return nil, fmt.Errorf("Wayland seat is not bound")
	}

	var pointer *wlrvirtualpointer.VirtualPointer
	var err error
	switch mode {
	case PointerMappingWithOutput:
		if outputHandle == nil {
			return nil, fmt.Errorf("Wayland output %q has no wl_output handle for virtual pointer", outputName)
		}
		pointer, err = manager.CreateVirtualPointerWithOutput(seat, outputHandle)
	case PointerMappingFallback:
		pointer, err = manager.CreateVirtualPointer(seat)
	default:
		return nil, fmt.Errorf("unsupported pointer mapping mode %q", mode)
	}
	if err != nil {
		return nil, fmt.Errorf("create virtual pointer: %w", err)
	}
	s.pointer = pointer
	s.pointerMode = mode
	s.pointerOutputName = outputName
	return pointer, nil
}

func (s *WaylandVirtualPointerSynthesizer) click(ctx context.Context, button PointerButton) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("Wayland virtual pointer is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	code, err := pointerButtonCode(button)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.haveCurrent || s.pointer == nil {
		return fmt.Errorf("cannot click before MoveAbsolute")
	}
	current := s.current
	event := PointerButtonEvent{
		OutputName: current.OutputName,
		X:          current.X,
		Y:          current.Y,
		Button:     button,
		Time:       s.clock.Now(),
	}

	return s.client.withProtocolLock(func() error {
		if s.pointer == nil {
			return fmt.Errorf("virtual pointer is not available")
		}
		timeMS := waylandPointerTime(event.Time)
		if err := s.pointer.Button(timeMS, code, uint32(wlclient.PointerButtonStatePressed)); err != nil {
			return fmt.Errorf("send virtual pointer button down: %w", err)
		}
		if err := s.pointer.Button(timeMS, code, uint32(wlclient.PointerButtonStateReleased)); err != nil {
			return fmt.Errorf("send virtual pointer button up: %w", err)
		}
		if err := s.pointer.Frame(); err != nil {
			return fmt.Errorf("send virtual pointer button frame: %w", err)
		}
		return nil
	})
}

func pointerButtonCode(button PointerButton) (uint32, error) {
	switch button {
	case PointerButtonLeft:
		return linuxInputButtonLeft, nil
	case PointerButtonRight:
		return linuxInputButtonRight, nil
	default:
		return 0, fmt.Errorf("unsupported pointer button %q", button)
	}
}

func waylandPointerTime(t time.Time) uint32 {
	if t.IsZero() {
		t = time.Now()
	}
	return uint32(t.UnixNano() / int64(time.Millisecond))
}

func ensureMonitorInLayout(monitors []Monitor, output Monitor) []Monitor {
	for _, monitor := range monitors {
		if monitor.Name == output.Name {
			return monitors
		}
	}
	return append(append([]Monitor(nil), monitors...), output)
}
