package main

import (
	"context"
	"testing"
	"time"
)

func TestDaemonShowCreatesExclusiveOverlayOnFocusedWaylandOutput(t *testing.T) {
	ctx := context.Background()
	focusedFromHyprland := Monitor{
		Name:    "eDP-1",
		X:       999,
		Y:       999,
		Width:   1,
		Height:  1,
		Scale:   1,
		Focused: true,
	}
	focusedWaylandOutput := Monitor{
		Name:    "eDP-1",
		X:       1920,
		Y:       -120,
		Width:   1706,
		Height:  960,
		Scale:   1.25,
		Focused: true,
	}
	wayland := newFakeWaylandBackend(
		Monitor{Name: "DP-1", X: 0, Y: 0, Width: 1920, Height: 1080, Scale: 1},
		focusedWaylandOutput,
	)
	controller := NewDaemonController(DaemonDeps{
		MonitorLookup: &fakeFocusedMonitorLookup{monitor: focusedFromHyprland},
		Overlay:       wayland,
	})

	if err := controller.Show(ctx); err != nil {
		t.Fatalf("show overlay: %v", err)
	}

	events := wayland.Events()
	create := requireFakeWaylandEvent(t, events, "surface_create")
	if create.OutputName != focusedWaylandOutput.Name || create.Width != focusedWaylandOutput.Width || create.Height != focusedWaylandOutput.Height || create.Scale != focusedWaylandOutput.Scale {
		t.Fatalf("surface_create = %+v, want focused Wayland output %+v", create, focusedWaylandOutput)
	}
	configure := requireFakeWaylandEvent(t, events, "configure")
	if configure.OutputName != focusedWaylandOutput.Name || configure.Width != focusedWaylandOutput.Width || configure.Height != focusedWaylandOutput.Height || configure.Scale != focusedWaylandOutput.Scale {
		t.Fatalf("configure = %+v, want focused Wayland output %+v", configure, focusedWaylandOutput)
	}
	keyboard := requireFakeWaylandEvent(t, events, "keyboard_grab")
	if keyboard.KeyboardInteractivity != "exclusive" {
		t.Fatalf("keyboard interactivity = %q, want exclusive; event=%+v", keyboard.KeyboardInteractivity, keyboard)
	}
	render := requireFakeWaylandEvent(t, events, "render")
	if render.Width != focusedWaylandOutput.Width || render.Height != focusedWaylandOutput.Height || render.BufferHash == "" {
		t.Fatalf("render = %+v, want focused output-sized buffer with hash", render)
	}

	if err := controller.Hide(ctx); err != nil {
		t.Fatalf("hide overlay: %v", err)
	}
	if got := wayland.Count("buffer_destroy"); got != 1 {
		t.Fatalf("buffer_destroy count after hide = %d, want 1; events=%+v", got, wayland.Events())
	}
	if got := wayland.Count("destroy"); got != 1 {
		t.Fatalf("destroy count after hide = %d, want 1; events=%+v", got, wayland.Events())
	}
}

func TestFakeOverlayRecreatesBuffersOnConfigureAndScale(t *testing.T) {
	ctx := context.Background()
	focused := fakeFocusedMonitorFixture()
	wayland := newFakeWaylandBackend(focused)
	controller := NewDaemonController(DaemonDeps{
		MonitorLookup: &fakeFocusedMonitorLookup{monitor: focused},
		Overlay:       wayland,
	})
	if err := controller.Show(ctx); err != nil {
		t.Fatalf("show overlay: %v", err)
	}

	surface, ok := controller.surface.(*fakeOverlaySurface)
	if !ok {
		t.Fatalf("controller surface = %T, want *fakeOverlaySurface", controller.surface)
	}
	if err := surface.SimulateConfigure(ctx, 800, 600); err != nil {
		t.Fatalf("simulate configure: %v", err)
	}
	if err := surface.SimulateScale(ctx, 2.0); err != nil {
		t.Fatalf("simulate scale: %v", err)
	}

	events := wayland.Events()
	configureEvent := requireLastFakeWaylandEvent(t, events, "configure_event")
	if configureEvent.Width != 800 || configureEvent.Height != 600 {
		t.Fatalf("configure event = %+v, want 800x600", configureEvent)
	}
	scaleEvent := requireLastFakeWaylandEvent(t, events, "scale_event")
	if scaleEvent.Scale != 2.0 {
		t.Fatalf("scale event = %+v, want scale 2.0", scaleEvent)
	}
	lastRender := requireLastFakeWaylandEvent(t, events, "render")
	if lastRender.Width != 800 || lastRender.Height != 600 {
		t.Fatalf("last render = %+v, want 800x600 after configure/scale", lastRender)
	}
	if got := wayland.Count("render"); got != 3 {
		t.Fatalf("render count = %d, want initial + configure + scale; events=%+v", got, events)
	}
	if got := wayland.Count("buffer_destroy"); got != 2 {
		t.Fatalf("buffer_destroy before final hide = %d, want two recreated buffers destroyed; events=%+v", got, events)
	}

	if err := controller.Hide(ctx); err != nil {
		t.Fatalf("hide overlay: %v", err)
	}
	if got := wayland.Count("buffer_destroy"); got != 3 {
		t.Fatalf("buffer_destroy after hide = %d, want all three buffers destroyed; events=%+v", got, wayland.Events())
	}
}

func TestDaemonStateRecoversFromCompositorClosedOverlay(t *testing.T) {
	ctx := context.Background()
	focused := fakeFocusedMonitorFixture()
	wayland := newFakeWaylandBackend(focused)
	controller := NewDaemonController(DaemonDeps{
		MonitorLookup: &fakeFocusedMonitorLookup{monitor: focused},
		Overlay:       wayland,
	})
	if err := controller.Show(ctx); err != nil {
		t.Fatalf("show overlay: %v", err)
	}
	surface, ok := controller.surface.(*fakeOverlaySurface)
	if !ok {
		t.Fatalf("controller surface = %T, want *fakeOverlaySurface", controller.surface)
	}
	if err := surface.SimulateCompositorClosed(ctx); err != nil {
		t.Fatalf("simulate compositor close: %v", err)
	}
	waitForControllerState(t, controller, DaemonStateInactive)

	if err := controller.Show(ctx); err != nil {
		t.Fatalf("show after compositor close: %v", err)
	}
	if got := wayland.Count("surface_create"); got != 2 {
		t.Fatalf("surface_create count after re-show = %d, want 2; events=%+v", got, wayland.Events())
	}
}

func TestLayerShellOverlayBackendRealSurfaceLifecycleWithFakeProtocol(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	responder := newFakeWaylandProtocolResponder(t)
	socketPath := responder.Start()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	client, err := OpenWaylandClient(ctx, socketPath)
	if err != nil {
		t.Fatalf("open Wayland client: %v", err)
	}
	defer client.Close()

	backend := NewLayerShellOverlayBackend(client)
	monitor := Monitor{Name: "eDP-1", X: 1920, Y: -120, Width: 1600, Height: 900, Scale: 1.25}
	surface, err := backend.CreateSurface(ctx, monitor)
	if err != nil {
		t.Fatalf("create layer shell surface: %v", err)
	}
	if err := surface.Configure(ctx, SurfaceConfig{OutputName: monitor.Name, Width: monitor.Width, Height: monitor.Height, Scale: monitor.Scale}); err != nil {
		t.Fatalf("configure layer shell surface: %v", err)
	}
	if err := surface.GrabKeyboard(ctx); err != nil {
		t.Fatalf("grab keyboard: %v", err)
	}
	buffer, err := NewARGBBuffer(monitor.Width, monitor.Height)
	if err != nil {
		t.Fatalf("new buffer: %v", err)
	}
	RenderPlaceholderOverlay(buffer)
	if err := surface.Render(ctx, buffer); err != nil {
		t.Fatalf("render layer shell surface: %v", err)
	}

	waitForFakeWaylandRequest(t, responder, "zwlr_layer_surface_v1.ack_configure:101")
	waitForFakeWaylandRequest(t, responder, "zwlr_layer_surface_v1.set_keyboard_interactivity:1")
	waitForFakeWaylandRequest(t, responder, "wl_surface.set_input_region")
	waitForFakeWaylandRequest(t, responder, "wl_surface.set_buffer_scale:2")
	waitForFakeWaylandRequest(t, responder, "wl_shm_pool.create_buffer")
	waitForFakeWaylandRequest(t, responder, "wl_buffer.destroy")

	realSurface, ok := surface.(*layerShellSurface)
	if !ok {
		t.Fatalf("surface = %T, want *layerShellSurface", surface)
	}
	realSurface.WaylandOutputChanged(Monitor{Name: monitor.Name, X: monitor.X, Y: monitor.Y, Width: 800, Height: 600, Scale: 2})
	waitForFakeWaylandRequest(t, responder, "zwlr_layer_surface_v1.set_size:800x600")
	waitForFakeWaylandRequestCount(t, responder, "wl_shm_pool.create_buffer", 2)

	if err := surface.Destroy(ctx); err != nil {
		t.Fatalf("destroy layer shell surface: %v", err)
	}
	waitForClosedChannel(t, surface.Closed())
	waitForFakeWaylandRequest(t, responder, "zwlr_layer_surface_v1.destroy")
	waitForFakeWaylandRequest(t, responder, "wl_surface.destroy")
}

func TestRenderPlaceholderOverlayFillsBuffer(t *testing.T) {
	buffer, err := NewARGBBuffer(3, 2)
	if err != nil {
		t.Fatalf("new buffer: %v", err)
	}
	RenderPlaceholderOverlay(buffer)
	for i, pixel := range buffer.Pixels {
		if pixel != 0x66080c10 {
			t.Fatalf("pixel %d = %#x, want placeholder color", i, pixel)
		}
	}
}

func waitForControllerState(t *testing.T, controller *DaemonController, want DaemonState) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := controller.State(); got == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("controller state = %q, want %q", controller.State(), want)
}

func waitForClosedChannel(t *testing.T, closed <-chan struct{}) {
	t.Helper()
	if closed == nil {
		t.Fatalf("closed channel is nil")
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatalf("surface closed channel did not close")
	}
}

func waitForFakeWaylandRequest(t *testing.T, responder *fakeWaylandProtocolResponder, want string) {
	t.Helper()
	waitForFakeWaylandRequestCount(t, responder, want, 1)
}

func waitForFakeWaylandRequestCount(t *testing.T, responder *fakeWaylandProtocolResponder, want string, count int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got := 0
		requests := responder.Requests()
		for _, request := range requests {
			if request == want {
				got++
			}
		}
		if got >= count {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("request %q count < %d; requests=%+v", want, count, responder.Requests())
}

func requireFakeWaylandEvent(t *testing.T, events []fakeWaylandEvent, kind string) fakeWaylandEvent {
	t.Helper()
	for _, event := range events {
		if event.Kind == kind {
			return event
		}
	}
	t.Fatalf("event kind %q not found in %+v", kind, events)
	return fakeWaylandEvent{}
}

func requireLastFakeWaylandEvent(t *testing.T, events []fakeWaylandEvent, kind string) fakeWaylandEvent {
	t.Helper()
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind == kind {
			return events[i]
		}
	}
	t.Fatalf("event kind %q not found in %+v", kind, events)
	return fakeWaylandEvent{}
}
