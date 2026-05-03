package main

import (
	"bytes"
	"context"
	"slices"
	"sync"
	"testing"
	"time"
)

type fixedOverlayRenderer struct{}

func (fixedOverlayRenderer) RenderMainGrid(monitor Monitor, gridSize int) (ARGBSnapshot, error) {
	pixels := make([]ARGBPixel, monitor.LogicalWidth*monitor.LogicalHeight)
	for i := range pixels {
		pixels[i] = StraightARGB(96, 74, 181, 255)
	}
	return NewARGBSnapshot(monitor.LogicalWidth, monitor.LogicalHeight, pixels)
}

func TestLayerShellOverlayDriverShowHideToggleAndReuseWithFakes(t *testing.T) {
	driver, wayland, _ := newTestLayerShellOverlayDriver(t, Monitor{
		Name:          "DP-1",
		LogicalWidth:  12,
		LogicalHeight: 8,
		Scale:         1,
	})
	controller := newDaemonController(driver, statusOutput{})

	response := controller.Dispatch(context.Background(), ipcRequest{Command: "show"})
	if !response.OK || !response.Active || response.Action != "shown" {
		t.Fatalf("show response = %+v, want shown active", response)
	}
	assertFakeWaylandEventKinds(t, wayland.Events(), "surface_create", "configure", "render", "keyboard_grab")

	response = controller.Dispatch(context.Background(), ipcRequest{Command: "show"})
	if !response.OK || response.Active || response.Action != "hidden" {
		t.Fatalf("show-toggle response = %+v, want hidden inactive", response)
	}
	assertFakeWaylandEventKinds(t, wayland.Events(), "unmap", "destroy")

	response = controller.Dispatch(context.Background(), ipcRequest{Command: "show"})
	if !response.OK || !response.Active || response.Action != "shown" {
		t.Fatalf("second show response = %+v, want shown active", response)
	}
	if got := wayland.LastSurface().id; got != 2 {
		t.Fatalf("second show surface id = %d, want 2", got)
	}

	response = controller.Dispatch(context.Background(), ipcRequest{Command: "hide"})
	if !response.OK || response.Active || response.Action != "hidden" {
		t.Fatalf("hide response = %+v, want hidden inactive", response)
	}
	assertFakeWaylandEventKinds(t, wayland.Events(), "unmap", "destroy")
}

func TestLayerShellOverlayDriverEscapeDestroysSurfaceAndResetsController(t *testing.T) {
	driver, wayland, _ := newTestLayerShellOverlayDriver(t, Monitor{
		Name:          "DP-1",
		LogicalWidth:  10,
		LogicalHeight: 6,
		Scale:         1,
	})
	wayland.keyboard.Enqueue(KeyboardEvent{Kind: KeyboardEventKey, Key: "Escape", State: KeyPressed})
	controller := newDaemonController(driver, statusOutput{})

	response := controller.Dispatch(context.Background(), ipcRequest{Command: "show"})
	if !response.OK || !response.Active {
		t.Fatalf("show response = %+v, want active", response)
	}
	waitForCondition(t, func() bool { return !driver.OverlayActive() })

	status := controller.Dispatch(context.Background(), ipcRequest{Command: "status"})
	if !status.OK || status.Active {
		t.Fatalf("status after Escape = %+v, want inactive", status)
	}
	assertFakeWaylandEventKinds(t, wayland.Events(), "unmap", "destroy")
}

func TestLayerShellOverlayDriverRecoversAfterCompositorClose(t *testing.T) {
	driver, wayland, _ := newTestLayerShellOverlayDriver(t, Monitor{
		Name:          "DP-1",
		LogicalWidth:  10,
		LogicalHeight: 6,
		Scale:         1,
	})
	controller := newDaemonController(driver, statusOutput{})

	response := controller.Dispatch(context.Background(), ipcRequest{Command: "show"})
	if !response.OK || !response.Active {
		t.Fatalf("show response = %+v, want active", response)
	}
	firstSurface := wayland.LastSurface()
	firstSurface.EnqueueLifecycle(OverlayLifecycleEvent{Kind: OverlayLifecycleCompositorClose})
	waitForCondition(t, func() bool { return !driver.OverlayActive() })

	status := controller.Dispatch(context.Background(), ipcRequest{Command: "status"})
	if !status.OK || status.Active {
		t.Fatalf("status after compositor close = %+v, want inactive", status)
	}

	response = controller.Dispatch(context.Background(), ipcRequest{Command: "show"})
	if !response.OK || !response.Active || response.Action != "shown" {
		t.Fatalf("show after compositor close = %+v, want shown active", response)
	}
	if got := wayland.LastSurface().id; got != firstSurface.id+1 {
		t.Fatalf("surface id after compositor close = %d, want %d", got, firstSurface.id+1)
	}
}

func TestLayerShellOverlayDriverLifecycleConfigureReleaseOutputChangeAndDestroy(t *testing.T) {
	driver, wayland, traceBytes := newTestLayerShellOverlayDriver(t, Monitor{
		Name:          "DP-1",
		LogicalWidth:  10,
		LogicalHeight: 6,
		Scale:         1,
	})
	controller := newDaemonController(driver, statusOutput{})

	response := controller.Dispatch(context.Background(), ipcRequest{Command: "show"})
	if !response.OK || !response.Active {
		t.Fatalf("show response = %+v, want active", response)
	}
	surface := wayland.LastSurface()
	surface.EnqueueLifecycle(
		OverlayLifecycleEvent{Kind: OverlayLifecycleConfigure, Width: 14, Height: 9, Scale: 2},
		OverlayLifecycleEvent{Kind: OverlayLifecycleRelease, Width: 14, Height: 9},
		OverlayLifecycleEvent{Kind: OverlayLifecycleOutputChange, Monitor: Monitor{Name: "HDMI-A-1", LogicalWidth: 16, LogicalHeight: 10, Scale: 1.5}},
		OverlayLifecycleEvent{Kind: OverlayLifecycleDestroy},
	)
	waitForCondition(t, func() bool { return !driver.OverlayActive() })

	events := wayland.Events()
	assertFakeWaylandEventKinds(t, events, "configure", "render", "output_change", "destroy")
	if !hasFakeRenderSize(events, 14, 9) {
		t.Fatalf("configure lifecycle did not recreate a 14x9 buffer: %+v", events)
	}
	if !hasFakeRenderSize(events, 16, 10) {
		t.Fatalf("output-change lifecycle did not recreate a 16x10 buffer: %+v", events)
	}

	traceEvents := decodeTraceEvents(t, traceBytes.String())
	assertTraceContains(t, traceEvents, traceOverlaySurfaceCreate, traceOverlayConfigure, traceOverlayRender, traceOverlayKeyboardGrab, traceOverlayOutputChange, traceOverlayDestroy)
}

func TestOverlayEventQueueDeliversLifecycleAndKeyboardCriticalEventsInOrder(t *testing.T) {
	keyboard := newOverlayEventQueue[KeyboardEvent]()
	keyboardEvents := []KeyboardEvent{
		{Kind: KeyboardEventKeymap, Keymap: &KeyboardKeymapFD{Data: []byte("keymap"), Size: 6}},
		{Kind: KeyboardEventEnter},
		{Kind: KeyboardEventKey, Key: "Escape", State: KeyPressed},
		{Kind: KeyboardEventDestroy},
	}
	for _, event := range keyboardEvents {
		keyboard.push(event)
	}
	for i, want := range keyboardEvents {
		got, err := keyboard.pop(context.Background(), nil)
		if err != nil {
			t.Fatalf("keyboard pop[%d] returned error: %v", i, err)
		}
		if got.Kind != want.Kind || got.Key != want.Key {
			t.Fatalf("keyboard pop[%d] = %+v, want %+v", i, got, want)
		}
	}

	lifecycle := newOverlayEventQueue[OverlayLifecycleEvent]()
	lifecycle.fatal(assertiveFatalError("wayland protocol error: invalid_keyboard_interactivity"))
	_, err := lifecycle.pop(context.Background(), nil)
	if err == nil || err.Error() != "wayland protocol error: invalid_keyboard_interactivity" {
		t.Fatalf("fatal lifecycle pop error = %v, want clear fatal error", err)
	}
}

type assertiveFatalError string

func (e assertiveFatalError) Error() string {
	return string(e)
}

func newTestLayerShellOverlayDriver(t *testing.T, monitor Monitor) (*layerShellOverlayDriver, *fakeWaylandBackend, *lockedTraceBuffer) {
	t.Helper()
	traceBytes := &lockedTraceBuffer{}
	trace := NewTraceRecorder(traceBytes, fixedTraceClock(time.Unix(20, 0)))
	config := DefaultConfig()
	config.Grid.Size = 2
	if err := config.Validate(); err != nil {
		t.Fatalf("test config Validate returned error: %v", err)
	}
	wayland := newFakeWaylandBackend(trace)
	driver, err := newLayerShellOverlayDriver(newFakeHyprlandIPC(monitor), wayland, fixedOverlayRenderer{}, config, trace)
	if err != nil {
		t.Fatalf("newLayerShellOverlayDriver returned error: %v", err)
	}
	return driver, wayland, traceBytes
}

type lockedTraceBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedTraceBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedTraceBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func assertFakeWaylandEventKinds(t *testing.T, events []fakeWaylandEvent, wants ...string) {
	t.Helper()
	var kinds []string
	for _, event := range events {
		kinds = append(kinds, event.Kind)
	}
	for _, want := range wants {
		if !slices.Contains(kinds, want) {
			t.Fatalf("Wayland events missing %q: %v", want, kinds)
		}
	}
}

func hasFakeRenderSize(events []fakeWaylandEvent, width, height int) bool {
	for _, event := range events {
		if event.Kind == "render" && event.Width == width && event.Height == height {
			return true
		}
	}
	return false
}

func waitForCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not satisfied before timeout")
}
