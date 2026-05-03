package main

import (
	"context"
	"fmt"
	"io"
	"time"
)

type Monitor struct {
	Name             string  `json:"name"`
	OriginX          int     `json:"origin_x"`
	OriginY          int     `json:"origin_y"`
	LogicalWidth     int     `json:"logical_width"`
	LogicalHeight    int     `json:"logical_height"`
	PhysicalWidthMM  int     `json:"physical_width_mm,omitempty"`
	PhysicalHeightMM int     `json:"physical_height_mm,omitempty"`
	Scale            float64 `json:"scale"`
}

func (m Monitor) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("monitor name is required")
	}
	if m.LogicalWidth <= 0 || m.LogicalHeight <= 0 {
		return fmt.Errorf("monitor %q has invalid logical size %dx%d", m.Name, m.LogicalWidth, m.LogicalHeight)
	}
	if m.Scale <= 0 {
		return fmt.Errorf("monitor %q has invalid scale %v", m.Name, m.Scale)
	}
	return nil
}

type FocusedMonitorLookup interface {
	FocusedMonitor(ctx context.Context) (Monitor, error)
}

type WaylandOverlayBackend interface {
	CreateSurface(ctx context.Context, monitor Monitor) (OverlaySurface, error)
	OutputChanged(ctx context.Context, monitor Monitor) error
	Close(ctx context.Context) error
}

type OverlaySurface interface {
	Configure(ctx context.Context, width, height int, scale float64) error
	Render(ctx context.Context, buffer ARGBSnapshot) error
	GrabKeyboard(ctx context.Context) (KeyboardEventSource, error)
	Unmap(ctx context.Context) error
	Destroy(ctx context.Context) error
}

type OverlayLifecycleEventKind string

const (
	OverlayLifecycleConfigure       OverlayLifecycleEventKind = "configure"
	OverlayLifecycleRelease         OverlayLifecycleEventKind = "release"
	OverlayLifecycleOutputChange    OverlayLifecycleEventKind = "output_change"
	OverlayLifecycleCompositorClose OverlayLifecycleEventKind = "compositor_close"
	OverlayLifecycleDestroy         OverlayLifecycleEventKind = "destroy"
	OverlayLifecycleError           OverlayLifecycleEventKind = "error"
)

type OverlayLifecycleEvent struct {
	Kind    OverlayLifecycleEventKind
	Width   int
	Height  int
	Scale   float64
	Monitor Monitor
	Err     error
}

type OverlayLifecycleEventSource interface {
	NextOverlayEvent(ctx context.Context) (OverlayLifecycleEvent, error)
	Close() error
}

type OverlayLifecycleEventProvider interface {
	LifecycleEvents() OverlayLifecycleEventSource
}

type RendererBufferSink interface {
	UploadARGB(ctx context.Context, target string, buffer ARGBSnapshot) error
}

type KeyboardEventSource interface {
	NextKeyboardEvent(ctx context.Context) (KeyboardEvent, error)
	Close() error
}

type KeyboardEventKind string

const (
	KeyboardEventKeymap    KeyboardEventKind = "keymap"
	KeyboardEventEnter     KeyboardEventKind = "enter"
	KeyboardEventLeave     KeyboardEventKind = "leave"
	KeyboardEventDestroy   KeyboardEventKind = "destroy"
	KeyboardEventKey       KeyboardEventKind = "key"
	KeyboardEventModifiers KeyboardEventKind = "modifiers"
	KeyboardEventRepeat    KeyboardEventKind = "repeat"
)

type KeyState string

const (
	KeyPressed  KeyState = "pressed"
	KeyReleased KeyState = "released"
)

type ModifierState struct {
	Shift bool `json:"shift,omitempty"`
	Ctrl  bool `json:"ctrl,omitempty"`
	Alt   bool `json:"alt,omitempty"`
	Super bool `json:"super,omitempty"`
}

func (m ModifierState) Empty() bool {
	return !m.Shift && !m.Ctrl && !m.Alt && !m.Super
}

type KeyboardEvent struct {
	Kind        KeyboardEventKind `json:"kind"`
	Key         string            `json:"key,omitempty"`
	RawKey      uint32            `json:"raw_key,omitempty"`
	State       KeyState          `json:"state,omitempty"`
	Modifiers   ModifierState     `json:"modifiers,omitempty"`
	Repeated    bool              `json:"repeated,omitempty"`
	Keymap      *KeyboardKeymapFD `json:"-"`
	RepeatRate  int               `json:"repeat_rate,omitempty"`
	RepeatDelay time.Duration     `json:"repeat_delay,omitempty"`
	At          time.Time         `json:"at,omitempty"`
}

type KeyboardKeymapFD struct {
	Data   []byte
	Offset int64
	Size   int64
}

func (k KeyboardKeymapFD) Bytes() ([]byte, error) {
	if k.Offset < 0 {
		return nil, fmt.Errorf("keymap offset %d is negative", k.Offset)
	}
	if k.Size < 0 {
		return nil, fmt.Errorf("keymap size %d is negative", k.Size)
	}
	if k.Offset > int64(len(k.Data)) {
		return nil, io.EOF
	}
	size := k.Size
	if size == 0 {
		size = int64(len(k.Data)) - k.Offset
	}
	end := k.Offset + size
	if end > int64(len(k.Data)) {
		return nil, io.ErrUnexpectedEOF
	}
	out := make([]byte, size)
	copy(out, k.Data[k.Offset:end])
	return out, nil
}

type PointerPosition struct {
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	OutputName string  `json:"output_name"`
}

type PointerMotion struct {
	Position PointerPosition `json:"position"`
	At       time.Time       `json:"at"`
}

type PointerButton string

const (
	PointerButtonLeft   PointerButton = "left"
	PointerButtonMiddle PointerButton = "middle"
	PointerButtonRight  PointerButton = "right"
)

type PointerButtonState string

const (
	PointerButtonDown PointerButtonState = "down"
	PointerButtonUp   PointerButtonState = "up"
)

type PointerButtonEvent struct {
	Button     PointerButton      `json:"button"`
	State      PointerButtonState `json:"state"`
	Position   PointerPosition    `json:"position"`
	ClickGroup int                `json:"click_group,omitempty"`
	ClickCount int                `json:"click_count,omitempty"`
	Sequence   int                `json:"sequence,omitempty"`
	At         time.Time          `json:"at"`
}

type PointerFrame struct {
	OutputName string    `json:"output_name"`
	At         time.Time `json:"at"`
}

type PointerSynthesizer interface {
	MovePointer(ctx context.Context, motion PointerMotion) error
	Button(ctx context.Context, button PointerButtonEvent) error
	Frame(ctx context.Context, frame PointerFrame) error
}

type InstalledServiceStatus struct {
	UnitName   string            `json:"unit_name"`
	Active     bool              `json:"active"`
	PID        int               `json:"pid,omitempty"`
	Executable string            `json:"executable,omitempty"`
	Build      buildInfo         `json:"build"`
	Details    map[string]string `json:"details,omitempty"`
}

type InstalledServiceStatusProvider interface {
	InstalledServiceStatus(ctx context.Context) (InstalledServiceStatus, error)
}

type ClockTimerSource interface {
	Now() time.Time
	NewTimer(d time.Duration) TimerHandle
}

type TimerHandle interface {
	C() <-chan time.Time
	Stop() bool
	Reset(d time.Duration) bool
}
