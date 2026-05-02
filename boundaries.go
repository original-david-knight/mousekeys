package main

import (
	"context"
	"time"
)

type Monitor struct {
	Name    string
	X       int
	Y       int
	Width   int
	Height  int
	Scale   float64
	Focused bool
}

func (m Monitor) LocalRect() Rect {
	return Rect{Width: m.Width, Height: m.Height}
}

func (m Monitor) LocalToVirtual(p Point) Point {
	return Point{X: m.X + p.X, Y: m.Y + p.Y}
}

func (m Monitor) ContainsLocal(p Point) bool {
	return p.X >= 0 && p.Y >= 0 && p.X < m.Width && p.Y < m.Height
}

type Point struct {
	X int
	Y int
}

type Rect struct {
	X      int
	Y      int
	Width  int
	Height int
}

func (r Rect) Center() Point {
	return Point{
		X: r.X + r.Width/2,
		Y: r.Y + r.Height/2,
	}
}

type FocusedMonitorLookup interface {
	FocusedMonitor(context.Context) (Monitor, error)
}

type WaylandOverlayBackend interface {
	Outputs(context.Context) ([]Monitor, error)
	CreateSurface(context.Context, Monitor) (OverlaySurface, error)
}

type OverlaySurface interface {
	ID() string
	Configure(context.Context, SurfaceConfig) error
	GrabKeyboard(context.Context) error
	Render(context.Context, ARGBBuffer) error
	Destroy(context.Context) error
	Closed() <-chan struct{}
}

type SurfaceConfig struct {
	OutputName string
	Width      int
	Height     int
	Scale      float64
}

type KeyboardEventSource interface {
	Events(context.Context) (<-chan KeyboardEvent, error)
}

type KeyboardEventKind string

const (
	KeyboardEventKey     KeyboardEventKind = "key"
	KeyboardEventEnter   KeyboardEventKind = "enter"
	KeyboardEventLeave   KeyboardEventKind = "leave"
	KeyboardEventDestroy KeyboardEventKind = "destroy"
	KeyboardEventError   KeyboardEventKind = "error"
)

type KeyboardEvent struct {
	Kind    KeyboardEventKind
	Key     string
	Keycode uint32
	Pressed bool
	Repeat  bool
	Time    time.Time
	Err     error
}

type PointerSynthesizer interface {
	MoveAbsolute(ctx context.Context, x int, y int, output Monitor) error
	LeftClick(context.Context) error
	RightClick(context.Context) error
	DoubleClick(context.Context) error
}

type PointerButton string

const (
	PointerButtonLeft  PointerButton = "left"
	PointerButtonRight PointerButton = "right"
)

type ButtonState string

const (
	ButtonDown ButtonState = "down"
	ButtonUp   ButtonState = "up"
)

type PointerMappingMode string

const (
	PointerMappingWithOutput PointerMappingMode = "with_output"
	PointerMappingFallback   PointerMappingMode = "fallback"
)

type PointerMotion struct {
	OutputName string
	X          int
	Y          int
	ProtocolX  uint32
	ProtocolY  uint32
	XExtent    uint32
	YExtent    uint32
	Mapping    PointerMappingMode
	Time       time.Time
	GroupID    string
}

type PointerButtonEvent struct {
	OutputName string
	X          int
	Y          int
	Button     PointerButton
	State      ButtonState
	Time       time.Time
	GroupID    string
}

type PointerFrame struct {
	OutputName string
	X          int
	Y          int
	Time       time.Time
	GroupID    string
}

type RendererBufferSink interface {
	Present(context.Context, string, ARGBBuffer) error
}

type Clock interface {
	Now() time.Time
	After(time.Duration) Timer
}

type Timer interface {
	C() <-chan time.Time
	Stop() bool
	Reset(time.Duration) bool
}

type systemClock struct{}

func (systemClock) Now() time.Time {
	return time.Now()
}

func (systemClock) After(d time.Duration) Timer {
	return &systemTimer{timer: time.NewTimer(d)}
}

type systemTimer struct {
	timer *time.Timer
}

func (t *systemTimer) C() <-chan time.Time {
	return t.timer.C
}

func (t *systemTimer) Stop() bool {
	return t.timer.Stop()
}

func (t *systemTimer) Reset(d time.Duration) bool {
	return t.timer.Reset(d)
}
