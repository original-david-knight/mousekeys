package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/rajveermalviya/go-wayland/wayland/client"
)

func TestPointerRecorderActionAPIsEmitExactOrdering(t *testing.T) {
	ctx := context.Background()
	monitor := Monitor{Name: "DP-1", OriginX: 1920, OriginY: 120, LogicalWidth: 2560, LogicalHeight: 1440, Scale: 1}
	now := time.Unix(1700000000, 123000000)
	pointer := newPointerRecorder(nil)
	pointer.now = func() time.Time { return now }

	if err := pointer.MoveAbsolute(ctx, 123.5, 456.25, monitor); err != nil {
		t.Fatalf("MoveAbsolute returned error: %v", err)
	}
	if err := pointer.LeftClick(ctx); err != nil {
		t.Fatalf("LeftClick returned error: %v", err)
	}
	if err := pointer.RightClick(ctx); err != nil {
		t.Fatalf("RightClick returned error: %v", err)
	}
	if err := pointer.DoubleClick(ctx); err != nil {
		t.Fatalf("DoubleClick returned error: %v", err)
	}

	events := pointer.Events()
	gotKinds := pointerEventKinds(events)
	wantKinds := []string{
		"motion", "frame",
		"button", "button", "frame",
		"button", "button", "frame",
		"button", "button", "frame",
		"button", "button", "frame",
	}
	if !reflect.DeepEqual(gotKinds, wantKinds) {
		t.Fatalf("event kinds = %v, want %v", gotKinds, wantKinds)
	}
	position := PointerPosition{X: 123.5, Y: 456.25, OutputName: "DP-1"}
	if events[0].Motion.Position != position {
		t.Fatalf("motion position = %+v, want %+v", events[0].Motion.Position, position)
	}
	assertRecordedFrame(t, events[1], "DP-1")
	assertRecordedButton(t, events[2], PointerButtonLeft, PointerButtonDown, position, 1, 1, 1)
	assertRecordedButton(t, events[3], PointerButtonLeft, PointerButtonUp, position, 1, 1, 1)
	assertRecordedFrame(t, events[4], "DP-1")
	assertRecordedButton(t, events[5], PointerButtonRight, PointerButtonDown, position, 2, 1, 1)
	assertRecordedButton(t, events[6], PointerButtonRight, PointerButtonUp, position, 2, 1, 1)
	assertRecordedFrame(t, events[7], "DP-1")
	assertRecordedButton(t, events[8], PointerButtonLeft, PointerButtonDown, position, 3, 2, 1)
	assertRecordedButton(t, events[9], PointerButtonLeft, PointerButtonUp, position, 3, 2, 1)
	assertRecordedFrame(t, events[10], "DP-1")
	assertRecordedButton(t, events[11], PointerButtonLeft, PointerButtonDown, position, 3, 2, 2)
	assertRecordedButton(t, events[12], PointerButtonLeft, PointerButtonUp, position, 3, 2, 2)
	assertRecordedFrame(t, events[13], "DP-1")
}

func TestVirtualPointerSynthesizerWithOutputMotion(t *testing.T) {
	ctx := context.Background()
	device := &fakeVirtualPointerDevice{}
	factory := &fakeVirtualPointerFactory{
		binding: virtualPointerBinding{
			Device: device,
			Mode:   virtualPointerBindingWithOutput,
			Output: WaylandOutputInfo{Name: "DP-1", LogicalX: 1920, LogicalY: 120, LogicalWidth: 300, LogicalHeight: 200},
			Layout: virtualPointerLayout{X: -1280, Y: -360, Width: 3500, Height: 1080},
		},
	}
	synth, err := newVirtualPointerSynthesizer(factory, nil, fixedPointerNow(1700000000123))
	if err != nil {
		t.Fatalf("newVirtualPointerSynthesizer returned error: %v", err)
	}

	monitor := Monitor{Name: "DP-1", OriginX: 1920, OriginY: 120, LogicalWidth: 300, LogicalHeight: 200, Scale: 1}
	if err := synth.MoveAbsolute(ctx, 12.4, 99.6, monitor); err != nil {
		t.Fatalf("MoveAbsolute returned error: %v", err)
	}

	if len(factory.created) != 1 || factory.created[0].Name != "DP-1" {
		t.Fatalf("factory created outputs = %+v, want DP-1", factory.created)
	}
	want := []fakeVirtualPointerProtocolEvent{
		{Kind: "motion_absolute", Time: 3487918203, X: 12, Y: 100, XExtent: 300, YExtent: 200},
		{Kind: "frame"},
	}
	if !reflect.DeepEqual(device.events, want) {
		t.Fatalf("protocol events = %+v, want %+v", device.events, want)
	}
}

func TestVirtualPointerSynthesizerFallbackMotionAppliesLayoutOrigin(t *testing.T) {
	ctx := context.Background()
	layout := virtualPointerLayout{X: -1280, Y: -360, Width: 5760, Height: 1920}
	tests := []struct {
		name   string
		output WaylandOutputInfo
		wantX  uint32
		wantY  uint32
	}{
		{
			name:   "positive origin",
			output: WaylandOutputInfo{Name: "DP-1", LogicalX: 1920, LogicalY: 120, LogicalWidth: 2560, LogicalHeight: 1440},
			wantX:  3210,
			wantY:  501,
		},
		{
			name:   "negative origin",
			output: WaylandOutputInfo{Name: "HDMI-A-1", LogicalX: -1280, LogicalY: -360, LogicalWidth: 1280, LogicalHeight: 720},
			wantX:  10,
			wantY:  21,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			device := &fakeVirtualPointerDevice{}
			factory := &fakeVirtualPointerFactory{
				binding: virtualPointerBinding{
					Device: device,
					Mode:   virtualPointerBindingFallback,
					Output: tt.output,
					Layout: layout,
				},
			}
			synth, err := newVirtualPointerSynthesizer(factory, nil, fixedPointerNow(1700000000123))
			if err != nil {
				t.Fatalf("newVirtualPointerSynthesizer returned error: %v", err)
			}
			monitor := Monitor{
				Name:          tt.output.Name,
				OriginX:       tt.output.LogicalX,
				OriginY:       tt.output.LogicalY,
				LogicalWidth:  tt.output.LogicalWidth,
				LogicalHeight: tt.output.LogicalHeight,
				Scale:         1,
			}
			if err := synth.MoveAbsolute(ctx, 10.2, 20.6, monitor); err != nil {
				t.Fatalf("MoveAbsolute returned error: %v", err)
			}
			want := []fakeVirtualPointerProtocolEvent{
				{Kind: "motion_absolute", Time: 3487918203, X: tt.wantX, Y: tt.wantY, XExtent: 5760, YExtent: 1920},
				{Kind: "frame"},
			}
			if !reflect.DeepEqual(device.events, want) {
				t.Fatalf("protocol events = %+v, want %+v", device.events, want)
			}
		})
	}
}

func TestVirtualPointerSynthesizerClickProtocolOrdering(t *testing.T) {
	ctx := context.Background()
	device := &fakeVirtualPointerDevice{}
	factory := &fakeVirtualPointerFactory{
		binding: virtualPointerBinding{
			Device: device,
			Mode:   virtualPointerBindingWithOutput,
			Output: WaylandOutputInfo{Name: "DP-1", LogicalWidth: 300, LogicalHeight: 200},
			Layout: virtualPointerLayout{Width: 300, Height: 200},
		},
	}
	synth, err := newVirtualPointerSynthesizer(factory, nil, fixedPointerNow(1700000000123))
	if err != nil {
		t.Fatalf("newVirtualPointerSynthesizer returned error: %v", err)
	}
	monitor := Monitor{Name: "DP-1", LogicalWidth: 300, LogicalHeight: 200, Scale: 1}
	if err := synth.MoveAbsolute(ctx, 10, 20, monitor); err != nil {
		t.Fatalf("MoveAbsolute returned error: %v", err)
	}
	if err := synth.DoubleClick(ctx); err != nil {
		t.Fatalf("DoubleClick returned error: %v", err)
	}

	want := []fakeVirtualPointerProtocolEvent{
		{Kind: "motion_absolute", Time: 3487918203, X: 10, Y: 20, XExtent: 300, YExtent: 200},
		{Kind: "frame"},
		{Kind: "button", Time: 3487918203, Button: linuxInputButtonLeft, State: uint32(client.PointerButtonStatePressed)},
		{Kind: "button", Time: 3487918203, Button: linuxInputButtonLeft, State: uint32(client.PointerButtonStateReleased)},
		{Kind: "frame"},
		{Kind: "button", Time: 3487918203, Button: linuxInputButtonLeft, State: uint32(client.PointerButtonStatePressed)},
		{Kind: "button", Time: 3487918203, Button: linuxInputButtonLeft, State: uint32(client.PointerButtonStateReleased)},
		{Kind: "frame"},
	}
	if !reflect.DeepEqual(device.events, want) {
		t.Fatalf("protocol events = %+v, want %+v", device.events, want)
	}
}

func TestCreateVirtualPointerForManagerVersion(t *testing.T) {
	t.Run("manager v1 fallback", func(t *testing.T) {
		manager := &fakeVirtualPointerManagerClient{device: &fakeVirtualPointerDevice{}}
		_, mode, err := createVirtualPointerForManagerVersion(manager, 1, &client.Seat{}, &client.Output{})
		if err != nil {
			t.Fatalf("createVirtualPointerForManagerVersion returned error: %v", err)
		}
		if mode != virtualPointerBindingFallback || !reflect.DeepEqual(manager.calls, []string{"create"}) {
			t.Fatalf("mode/calls = %s/%v, want fallback/[create]", mode, manager.calls)
		}
	})

	t.Run("manager v2 with output", func(t *testing.T) {
		manager := &fakeVirtualPointerManagerClient{device: &fakeVirtualPointerDevice{}}
		_, mode, err := createVirtualPointerForManagerVersion(manager, 2, &client.Seat{}, &client.Output{})
		if err != nil {
			t.Fatalf("createVirtualPointerForManagerVersion returned error: %v", err)
		}
		if mode != virtualPointerBindingWithOutput || !reflect.DeepEqual(manager.calls, []string{"create_with_output"}) {
			t.Fatalf("mode/calls = %s/%v, want with-output/[create_with_output]", mode, manager.calls)
		}
	})

	t.Run("manager v2 missing output is error", func(t *testing.T) {
		manager := &fakeVirtualPointerManagerClient{device: &fakeVirtualPointerDevice{}}
		_, _, err := createVirtualPointerForManagerVersion(manager, 2, &client.Seat{}, nil)
		if err == nil {
			t.Fatal("createVirtualPointerForManagerVersion returned nil error with nil output")
		}
		if len(manager.calls) != 0 {
			t.Fatalf("manager calls = %v, want none after nil output", manager.calls)
		}
	})
}

func TestWaylandClientBaseAcceptsVirtualPointerManagerV1(t *testing.T) {
	_, env, _ := waylandSocketEnvForTest(t, "wayland-vpointer-v1")
	driver := newFakeWaylandBaseDriver()
	driver.globals = []fakeWaylandGlobal{
		{name: 1, iface: waylandGlobalCompositor, version: 6},
		{name: 2, iface: waylandGlobalShm, version: 1},
		{name: 3, iface: waylandGlobalSeat, version: 7},
		{name: 4, iface: waylandGlobalOutput, version: 4},
		{name: 5, iface: waylandGlobalXDGOutputManager, version: 3},
		{name: 6, iface: waylandGlobalLayerShell, version: 5},
		{name: 7, iface: waylandGlobalVirtualPointerManager, version: 1},
	}
	wc, err := openWaylandClientBase(context.Background(), waylandClientBaseOptions{
		Getenv: env,
		Driver: driver,
	})
	if err != nil {
		t.Fatalf("openWaylandClientBase returned error: %v", err)
	}
	t.Cleanup(func() {
		if err := wc.Close(context.Background()); err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	})

	gotBindings := driver.boundInterfaces()
	if got := gotBindings[len(gotBindings)-1]; got != "zwlr_virtual_pointer_manager_v1@1" {
		t.Fatalf("last binding = %q, want virtual pointer manager v1; all bindings: %v", got, gotBindings)
	}
	wc.mu.RLock()
	gotVersion := wc.virtualPointerVersion
	wc.mu.RUnlock()
	if gotVersion != 1 {
		t.Fatalf("virtualPointerVersion = %d, want 1", gotVersion)
	}
}

func TestVirtualPointerLayoutFromOutputs(t *testing.T) {
	outputs := []WaylandOutputInfo{
		{Name: "HDMI-A-1", LogicalX: -1280, LogicalY: -360, LogicalWidth: 1280, LogicalHeight: 720},
		{Name: "DP-1", LogicalX: 1920, LogicalY: 120, LogicalWidth: 2560, LogicalHeight: 1440},
	}
	layout, err := virtualPointerLayoutFromOutputs(outputs)
	if err != nil {
		t.Fatalf("virtualPointerLayoutFromOutputs returned error: %v", err)
	}
	want := virtualPointerLayout{X: -1280, Y: -360, Width: 5760, Height: 1920}
	if layout != want {
		t.Fatalf("layout = %+v, want %+v", layout, want)
	}
}

type fakeVirtualPointerFactory struct {
	binding virtualPointerBinding
	err     error
	created []Monitor
}

func (f *fakeVirtualPointerFactory) CreateVirtualPointer(ctx context.Context, output Monitor) (virtualPointerBinding, error) {
	if err := ctx.Err(); err != nil {
		return virtualPointerBinding{}, err
	}
	f.created = append(f.created, output)
	if f.err != nil {
		return virtualPointerBinding{}, f.err
	}
	return f.binding, nil
}

type fakeVirtualPointerProtocolEvent struct {
	Kind    string
	Time    uint32
	X       uint32
	Y       uint32
	XExtent uint32
	YExtent uint32
	Button  uint32
	State   uint32
}

type fakeVirtualPointerDevice struct {
	events    []fakeVirtualPointerProtocolEvent
	destroyed bool
	err       error
}

func (d *fakeVirtualPointerDevice) MotionAbsolute(time, x, y, xExtent, yExtent uint32) error {
	if d.err != nil {
		return d.err
	}
	d.events = append(d.events, fakeVirtualPointerProtocolEvent{Kind: "motion_absolute", Time: time, X: x, Y: y, XExtent: xExtent, YExtent: yExtent})
	return nil
}

func (d *fakeVirtualPointerDevice) Button(time, button, state uint32) error {
	if d.err != nil {
		return d.err
	}
	d.events = append(d.events, fakeVirtualPointerProtocolEvent{Kind: "button", Time: time, Button: button, State: state})
	return nil
}

func (d *fakeVirtualPointerDevice) Frame() error {
	if d.err != nil {
		return d.err
	}
	d.events = append(d.events, fakeVirtualPointerProtocolEvent{Kind: "frame"})
	return nil
}

func (d *fakeVirtualPointerDevice) Destroy() error {
	if d.err != nil {
		return d.err
	}
	d.destroyed = true
	d.events = append(d.events, fakeVirtualPointerProtocolEvent{Kind: "destroy"})
	return nil
}

type fakeVirtualPointerManagerClient struct {
	device virtualPointerDevice
	err    error
	calls  []string
}

func (m *fakeVirtualPointerManagerClient) CreateVirtualPointer(seat *client.Seat) (virtualPointerDevice, error) {
	m.calls = append(m.calls, "create")
	if m.err != nil {
		return nil, m.err
	}
	if m.device == nil {
		return nil, errors.New("fake manager has no device")
	}
	return m.device, nil
}

func (m *fakeVirtualPointerManagerClient) CreateVirtualPointerWithOutput(seat *client.Seat, output *client.Output) (virtualPointerDevice, error) {
	m.calls = append(m.calls, "create_with_output")
	if m.err != nil {
		return nil, m.err
	}
	if m.device == nil {
		return nil, errors.New("fake manager has no device")
	}
	return m.device, nil
}

func pointerEventKinds(events []recordedPointerEvent) []string {
	out := make([]string, len(events))
	for i, event := range events {
		out[i] = event.Kind
	}
	return out
}

func assertRecordedButton(t *testing.T, event recordedPointerEvent, button PointerButton, state PointerButtonState, position PointerPosition, clickGroup, clickCount, sequence int) {
	t.Helper()
	if event.Kind != "button" {
		t.Fatalf("event kind = %q, want button", event.Kind)
	}
	got := event.Button
	if got.Button != button || got.State != state || got.Position != position || got.ClickGroup != clickGroup || got.ClickCount != clickCount || got.Sequence != sequence {
		t.Fatalf("button event = %+v, want button=%s state=%s position=%+v group=%d count=%d sequence=%d", got, button, state, position, clickGroup, clickCount, sequence)
	}
}

func assertRecordedFrame(t *testing.T, event recordedPointerEvent, outputName string) {
	t.Helper()
	if event.Kind != "frame" {
		t.Fatalf("event kind = %q, want frame", event.Kind)
	}
	if event.Frame.OutputName != outputName {
		t.Fatalf("frame output = %q, want %q", event.Frame.OutputName, outputName)
	}
}

func fixedPointerNow(unixMillis int64) func() time.Time {
	return func() time.Time {
		return time.Unix(0, unixMillis*int64(time.Millisecond))
	}
}
