package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rajveermalviya/go-wayland/wayland/client"
	xdgoutput "github.com/rajveermalviya/go-wayland/wayland/unstable/xdg-output-v1"
)

func TestWaylandSocketPathFromEnv(t *testing.T) {
	t.Run("relative display resolves under runtime dir", func(t *testing.T) {
		runtimeDir, env, socketPath := waylandSocketEnvForTest(t, "wayland-test")
		display, got, err := waylandSocketPathFromEnv(env)
		if err != nil {
			t.Fatalf("waylandSocketPathFromEnv returned error: %v", err)
		}
		if display != "wayland-test" || got != socketPath {
			t.Fatalf("display/path = %q/%q, want %q/%q", display, got, "wayland-test", socketPath)
		}
		if !strings.HasPrefix(got, runtimeDir) {
			t.Fatalf("socket path %q is not under runtime dir %q", got, runtimeDir)
		}
	})

	t.Run("absolute display path is accepted", func(t *testing.T) {
		runtimeDir := t.TempDir()
		socketPath := filepath.Join(runtimeDir, "absolute-wayland")
		listener := listenUnixSocketForWaylandTest(t, socketPath)
		defer listener.Close()

		display, got, err := waylandSocketPathFromEnv(mapEnv(map[string]string{
			"XDG_RUNTIME_DIR": runtimeDir,
			"WAYLAND_DISPLAY": socketPath,
		}))
		if err != nil {
			t.Fatalf("waylandSocketPathFromEnv returned error: %v", err)
		}
		if display != socketPath || got != socketPath {
			t.Fatalf("display/path = %q/%q, want absolute %q", display, got, socketPath)
		}
	})

	t.Run("missing environment is clear", func(t *testing.T) {
		_, _, err := waylandSocketPathFromEnv(emptyEnv)
		assertErrorContains(t, err, "missing required Wayland client environment", "XDG_RUNTIME_DIR", "WAYLAND_DISPLAY")
	})

	t.Run("missing socket is clear", func(t *testing.T) {
		runtimeDir := t.TempDir()
		_, _, err := waylandSocketPathFromEnv(mapEnv(map[string]string{
			"XDG_RUNTIME_DIR": runtimeDir,
			"WAYLAND_DISPLAY": "wayland-missing",
		}))
		assertErrorContains(t, err, "Wayland socket", "WAYLAND_DISPLAY", "does not exist")
	})

	t.Run("non socket path is rejected", func(t *testing.T) {
		runtimeDir := t.TempDir()
		path := filepath.Join(runtimeDir, "wayland-file")
		if err := os.WriteFile(path, []byte("not a socket"), 0o600); err != nil {
			t.Fatalf("write non-socket path: %v", err)
		}
		_, _, err := waylandSocketPathFromEnv(mapEnv(map[string]string{
			"XDG_RUNTIME_DIR": runtimeDir,
			"WAYLAND_DISPLAY": "wayland-file",
		}))
		assertErrorContains(t, err, "is not a Unix socket")
	})
}

func TestWaylandStringEncodingUsesUnpaddedWireLength(t *testing.T) {
	for _, value := range []string{"wl_compositor", layerShellOverlayNamespace} {
		t.Run(value, func(t *testing.T) {
			buf := make([]byte, 4+waylandStringPayloadLen(value))
			n, err := putWaylandString(buf, value)
			if err != nil {
				t.Fatalf("putWaylandString returned error: %v", err)
			}
			if n != len(buf) {
				t.Fatalf("putWaylandString wrote %d bytes, want %d", n, len(buf))
			}
			if got, want := client.Uint32(buf[:4]), uint32(len(value)+1); got != want {
				t.Fatalf("Wayland string wire length = %d, want unpadded length %d", got, want)
			}
			for i, b := range []byte(value) {
				if got := buf[4+i]; got != b {
					t.Fatalf("payload byte %d = %q, want %q", i, got, b)
				}
			}
			for i := 4 + len(value); i < len(buf); i++ {
				if buf[i] != 0 {
					t.Fatalf("padding byte %d = %d, want zero", i, buf[i])
				}
			}
		})
	}

	buf := make([]byte, 4+waylandStringPayloadLen("bad\x00name"))
	if _, err := putWaylandString(buf, "bad\x00name"); err == nil {
		t.Fatal("putWaylandString accepted an embedded NUL")
	}
}

func TestWaylandClientBaseBindsRequiredGlobalsAndMatchesOutput(t *testing.T) {
	_, env, _ := waylandSocketEnvForTest(t, "wayland-bind")
	driver := newFakeWaylandBaseDriver()
	driver.outputs = []fakeWaylandOutput{
		{
			global: 4,
			geometry: client.OutputGeometryEvent{
				X: 1920, Y: 120, PhysicalWidth: 600, PhysicalHeight: 340,
			},
			mode:        client.OutputModeEvent{Flags: uint32(client.OutputModeCurrent), Width: 2560, Height: 1440},
			scale:       client.OutputScaleEvent{Factor: 1},
			wlName:      "DP-1",
			description: "Dell DP display",
			xdgPosition: xdgoutput.OutputLogicalPositionEvent{
				X: 1920, Y: 120,
			},
			xdgSize: xdgoutput.OutputLogicalSizeEvent{
				Width: 2048, Height: 1152,
			},
			xdgName: "ignored-fallback",
		},
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
	wantBindings := []string{
		"wl_compositor@6",
		"wl_shm@1",
		"wl_seat@7",
		"wl_output@4",
		"zxdg_output_manager_v1@3",
		"zwlr_layer_shell_v1@5",
		"zwlr_virtual_pointer_manager_v1@2",
	}
	if !reflect.DeepEqual(gotBindings, wantBindings) {
		t.Fatalf("bindings = %v, want %v", gotBindings, wantBindings)
	}
	if !reflect.DeepEqual(driver.xdgRequests, []uint32{4}) {
		t.Fatalf("xdg output requests = %v, want [4]", driver.xdgRequests)
	}

	outputs := wc.Outputs()
	if len(outputs) != 1 {
		t.Fatalf("Outputs length = %d, want 1: %+v", len(outputs), outputs)
	}
	output := outputs[0]
	if output.Name != "DP-1" || output.LogicalX != 1920 || output.LogicalY != 120 ||
		output.LogicalWidth != 2048 || output.LogicalHeight != 1152 || output.Scale != 1.25 ||
		!output.UsingXDGOutput {
		t.Fatalf("unexpected output snapshot: %+v", output)
	}

	matched, err := wc.OutputForMonitor(Monitor{
		Name:          "DP-1",
		OriginX:       1920,
		OriginY:       120,
		LogicalWidth:  2048,
		LogicalHeight: 1152,
		Scale:         1.25,
	})
	if err != nil {
		t.Fatalf("OutputForMonitor returned error: %v", err)
	}
	if matched.GlobalName != 4 {
		t.Fatalf("matched output global = %d, want 4", matched.GlobalName)
	}
}

func TestWaylandOutputEnumerationUsesXDGNameFallback(t *testing.T) {
	_, env, _ := waylandSocketEnvForTest(t, "wayland-xdg-name")
	driver := newFakeWaylandBaseDriver()
	driver.outputs = []fakeWaylandOutput{
		{
			global: 4,
			geometry: client.OutputGeometryEvent{
				X: -1280, Y: -360,
			},
			mode: client.OutputModeEvent{Flags: uint32(client.OutputModeCurrent), Width: 1280, Height: 720},
			scale: client.OutputScaleEvent{
				Factor: 1,
			},
			xdgPosition: xdgoutput.OutputLogicalPositionEvent{X: -1280, Y: -360},
			xdgSize:     xdgoutput.OutputLogicalSizeEvent{Width: 1280, Height: 720},
			xdgName:     "HDMI-A-1",
		},
	}

	wc, err := openWaylandClientBase(context.Background(), waylandClientBaseOptions{
		Getenv: env,
		Driver: driver,
	})
	if err != nil {
		t.Fatalf("openWaylandClientBase returned error: %v", err)
	}
	matched, err := wc.OutputForMonitor(Monitor{
		Name:          "HDMI-A-1",
		OriginX:       -1280,
		OriginY:       -360,
		LogicalWidth:  1280,
		LogicalHeight: 720,
		Scale:         1,
	})
	if err != nil {
		t.Fatalf("OutputForMonitor returned error: %v", err)
	}
	if matched.Name != "HDMI-A-1" || matched.WLName != "" || matched.XDGName != "HDMI-A-1" {
		t.Fatalf("matched output did not use xdg-output name fallback: %+v", matched)
	}
}

func TestWaylandClientBaseMissingGlobalsAndSeatCapabilities(t *testing.T) {
	t.Run("missing required global", func(t *testing.T) {
		_, env, _ := waylandSocketEnvForTest(t, "wayland-missing-global")
		driver := newFakeWaylandBaseDriver()
		driver.omitLayerShell = true

		_, err := openWaylandClientBase(context.Background(), waylandClientBaseOptions{
			Getenv: env,
			Driver: driver,
		})
		assertErrorContains(t, err, "missing required Wayland global", waylandGlobalLayerShell)
	})

	t.Run("seat missing keyboard capability", func(t *testing.T) {
		_, env, _ := waylandSocketEnvForTest(t, "wayland-seat-fail")
		driver := newFakeWaylandBaseDriver()
		driver.seatCapabilities = uint32(client.SeatCapabilityPointer)

		_, err := openWaylandClientBase(context.Background(), waylandClientBaseOptions{
			Getenv: env,
			Driver: driver,
		})
		assertErrorContains(t, err, "Wayland seat", "keyboard")
	})
}

func TestWaylandClientBaseDeadlineCancellation(t *testing.T) {
	_, env, _ := waylandSocketEnvForTest(t, "wayland-deadline")
	driver := &blockingWaylandBaseDriver{}

	start := time.Now()
	_, err := openWaylandClientBase(context.Background(), waylandClientBaseOptions{
		Getenv:  env,
		Timeout: 20 * time.Millisecond,
		Driver:  driver,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("openWaylandClientBase error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("deadline cancellation took %s", elapsed)
	}
	if !driver.closed {
		t.Fatal("blocking driver was not closed after deadline failure")
	}
}

func TestWaylandClientBaseStateIsRaceSafe(t *testing.T) {
	_, env, _ := waylandSocketEnvForTest(t, "wayland-race")
	driver := newFakeWaylandBaseDriver()
	wc, err := openWaylandClientBase(context.Background(), waylandClientBaseOptions{
		Getenv: env,
		Driver: driver,
	})
	if err != nil {
		t.Fatalf("openWaylandClientBase returned error: %v", err)
	}

	monitor := Monitor{Name: "DP-1", OriginX: 0, OriginY: 0, LogicalWidth: 1920, LogicalHeight: 1080, Scale: 1}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = wc.Outputs()
				_, _ = wc.OutputForMonitor(monitor)
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				wc.handleXDGOutputLogicalPosition(4, xdgoutput.OutputLogicalPositionEvent{X: int32(offset), Y: int32(j)})
				wc.handleXDGOutputLogicalSize(4, xdgoutput.OutputLogicalSizeEvent{Width: 1920, Height: 1080})
				wc.handleOutputName(4, client.OutputNameEvent{Name: "DP-1"})
			}
		}(i)
	}
	wg.Wait()
}

type fakeWaylandBaseDriver struct {
	globals          []fakeWaylandGlobal
	outputs          []fakeWaylandOutput
	seatCapabilities uint32
	omitLayerShell   bool

	mu          sync.Mutex
	socketPath  string
	bindings    []waylandGlobalBinding
	xdgRequests []uint32
	closed      bool
}

type fakeWaylandGlobal struct {
	name    uint32
	iface   string
	version uint32
}

type fakeWaylandOutput struct {
	global      uint32
	geometry    client.OutputGeometryEvent
	mode        client.OutputModeEvent
	scale       client.OutputScaleEvent
	wlName      string
	description string

	xdgPosition xdgoutput.OutputLogicalPositionEvent
	xdgSize     xdgoutput.OutputLogicalSizeEvent
	xdgName     string
}

func newFakeWaylandBaseDriver() *fakeWaylandBaseDriver {
	return &fakeWaylandBaseDriver{
		seatCapabilities: uint32(client.SeatCapabilityKeyboard | client.SeatCapabilityPointer),
		outputs: []fakeWaylandOutput{
			{
				global:   4,
				geometry: client.OutputGeometryEvent{X: 0, Y: 0},
				mode:     client.OutputModeEvent{Flags: uint32(client.OutputModeCurrent), Width: 1920, Height: 1080},
				scale:    client.OutputScaleEvent{Factor: 1},
				wlName:   "DP-1",
				xdgSize:  xdgoutput.OutputLogicalSizeEvent{Width: 1920, Height: 1080},
			},
		},
	}
}

func (f *fakeWaylandBaseDriver) Open(ctx context.Context, socketPath string, c *WaylandClientBase) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	f.socketPath = socketPath
	f.mu.Unlock()

	globals := f.globals
	if len(globals) == 0 {
		globals = []fakeWaylandGlobal{
			{name: 1, iface: waylandGlobalCompositor, version: 6},
			{name: 2, iface: waylandGlobalShm, version: 1},
			{name: 3, iface: waylandGlobalSeat, version: 7},
			{name: 4, iface: waylandGlobalOutput, version: 4},
			{name: 5, iface: waylandGlobalXDGOutputManager, version: 3},
			{name: 6, iface: waylandGlobalLayerShell, version: 5},
			{name: 7, iface: waylandGlobalVirtualPointerManager, version: 2},
		}
	}
	for _, global := range globals {
		if f.omitLayerShell && global.iface == waylandGlobalLayerShell {
			continue
		}
		binding := c.handleRegistryGlobal(global.name, global.iface, global.version)
		if binding.Kind != waylandBindingNone {
			f.mu.Lock()
			f.bindings = append(f.bindings, binding)
			f.mu.Unlock()
		}
	}
	for _, output := range f.outputs {
		c.handleOutputGeometry(output.global, output.geometry)
		c.handleOutputMode(output.global, output.mode)
		if output.scale.Factor != 0 {
			c.handleOutputScale(output.global, output.scale)
		}
		if output.wlName != "" {
			c.handleOutputName(output.global, client.OutputNameEvent{Name: output.wlName})
		}
		if output.description != "" {
			c.handleOutputDescription(output.global, client.OutputDescriptionEvent{Description: output.description})
		}
	}
	c.handleSeatCapabilities(f.seatCapabilities)
	for _, global := range c.pendingXDGOutputBindings() {
		f.mu.Lock()
		f.xdgRequests = append(f.xdgRequests, global)
		f.mu.Unlock()
		for _, output := range f.outputs {
			if output.global != global {
				continue
			}
			c.handleXDGOutputLogicalPosition(global, output.xdgPosition)
			c.handleXDGOutputLogicalSize(global, output.xdgSize)
			if output.xdgName != "" {
				c.handleXDGOutputName(global, xdgoutput.OutputNameEvent{Name: output.xdgName})
			}
		}
	}
	return ctx.Err()
}

func (f *fakeWaylandBaseDriver) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeWaylandBaseDriver) boundInterfaces() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.bindings))
	for i, binding := range f.bindings {
		out[i] = binding.Interface + "@" + formatUint32ForTest(binding.Version)
	}
	return out
}

type blockingWaylandBaseDriver struct {
	mu     sync.Mutex
	closed bool
}

func (b *blockingWaylandBaseDriver) Open(ctx context.Context, socketPath string, c *WaylandClientBase) error {
	<-ctx.Done()
	return ctx.Err()
}

func (b *blockingWaylandBaseDriver) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	return nil
}

func waylandSocketEnvForTest(t *testing.T, display string) (string, getenvFunc, string) {
	t.Helper()
	runtimeDir := t.TempDir()
	socketPath := filepath.Join(runtimeDir, display)
	listener := listenUnixSocketForWaylandTest(t, socketPath)
	t.Cleanup(func() {
		_ = listener.Close()
	})
	return runtimeDir, mapEnv(map[string]string{
		"XDG_RUNTIME_DIR": runtimeDir,
		"WAYLAND_DISPLAY": display,
	}), socketPath
}

func listenUnixSocketForWaylandTest(t *testing.T, socketPath string) net.Listener {
	t.Helper()
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen on test Wayland socket: %v", err)
	}
	return listener
}

func formatUint32ForTest(v uint32) string {
	if v == 0 {
		return "0"
	}
	var buf [10]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
