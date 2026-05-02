package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"time"
	"unsafe"

	wlclient "github.com/rajveermalviya/go-wayland/wayland/client"
	xdgoutput "github.com/rajveermalviya/go-wayland/wayland/unstable/xdg-output-v1"

	"mousekeys/internal/waylandprotocols/wlrlayershell"
	"mousekeys/internal/waylandprotocols/wlrvirtualpointer"
)

const waylandDispatchPollInterval = 100 * time.Millisecond

type WaylandClient struct {
	mu                    sync.RWMutex
	protocolMu            sync.Mutex
	dispatchOnce          sync.Once
	closeOnce             sync.Once
	closeErr              error
	display               *wlclient.Display
	registry              *wlclient.Registry
	compositor            *wlclient.Compositor
	shm                   *wlclient.Shm
	seat                  *wlclient.Seat
	seatCapabilities      uint32
	seatName              string
	layerShell            *wlrlayershell.LayerShell
	virtualPointerManager *wlrvirtualpointer.VirtualPointerManager
	xdgOutputManager      *xdgoutput.OutputManager
	outputs               map[uint32]*waylandOutputState
	outputHandles         map[uint32]*wlclient.Output
	outputListeners       map[string]map[waylandOutputChangeListener]struct{}
	protocolErr           error
}

func WaylandSocketPathFromEnv() (string, error) {
	display := os.Getenv("WAYLAND_DISPLAY")
	if display == "" {
		return "", fmt.Errorf("locate Wayland socket: WAYLAND_DISPLAY is required")
	}
	if filepath.IsAbs(display) {
		return display, nil
	}

	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		return "", fmt.Errorf("locate Wayland socket: XDG_RUNTIME_DIR is required when WAYLAND_DISPLAY is relative")
	}
	if !filepath.IsAbs(runtimeDir) {
		return "", fmt.Errorf("locate Wayland socket: XDG_RUNTIME_DIR must be an absolute path, got %q", runtimeDir)
	}
	return filepath.Join(runtimeDir, display), nil
}

func OpenWaylandClientFromEnv(ctx context.Context) (*WaylandClient, error) {
	socketPath, err := WaylandSocketPathFromEnv()
	if err != nil {
		return nil, err
	}
	return OpenWaylandClient(ctx, socketPath)
}

func OpenWaylandClient(ctx context.Context, socketPath string) (*WaylandClient, error) {
	if socketPath == "" {
		return nil, fmt.Errorf("Wayland socket path is required")
	}
	if _, err := os.Stat(socketPath); err != nil {
		return nil, fmt.Errorf("connect to Wayland compositor socket %q: %w", socketPath, err)
	}

	display, err := wlclient.Connect(socketPath)
	if err != nil {
		return nil, fmt.Errorf("connect to Wayland compositor socket %q: %w", socketPath, err)
	}

	client := &WaylandClient{
		display:         display,
		outputs:         map[uint32]*waylandOutputState{},
		outputHandles:   map[uint32]*wlclient.Output{},
		outputListeners: map[string]map[waylandOutputChangeListener]struct{}{},
	}
	display.SetErrorHandler(func(event wlclient.DisplayErrorEvent) {
		objectID := uint32(0)
		if event.ObjectId != nil {
			objectID = event.ObjectId.ID()
		}
		client.setProtocolErr(fmt.Errorf("Wayland protocol error on object %d: code=%d message=%q", objectID, event.Code, event.Message))
	})

	if err := client.initialize(ctx); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

func (c *WaylandClient) Close() error {
	if c == nil || c.display == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		c.closeErr = c.display.Context().Close()
	})
	return c.closeErr
}

func (c *WaylandClient) Compositor() *wlclient.Compositor {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.compositor
}

func (c *WaylandClient) Shm() *wlclient.Shm {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.shm
}

func (c *WaylandClient) Seat() *wlclient.Seat {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.seat
}

func (c *WaylandClient) LayerShell() *wlrlayershell.LayerShell {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.layerShell
}

func (c *WaylandClient) VirtualPointerManager() *wlrvirtualpointer.VirtualPointerManager {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.virtualPointerManager
}

func (c *WaylandClient) Outputs(context.Context) ([]Monitor, error) {
	if c == nil {
		return nil, fmt.Errorf("Wayland client is nil")
	}
	return c.outputMonitors()
}

func (c *WaylandClient) OutputHandle(name string) (*wlclient.Output, bool) {
	if c == nil || name == "" {
		return nil, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	for globalName, state := range c.outputs {
		if state.name() == name {
			handle := c.outputHandles[globalName]
			return handle, handle != nil
		}
	}
	return nil, false
}

func (c *WaylandClient) FocusedOutput(ctx context.Context, lookup FocusedMonitorLookup) (Monitor, *wlclient.Output, error) {
	if lookup == nil {
		return Monitor{}, nil, fmt.Errorf("focused monitor lookup is required")
	}
	focused, err := lookup.FocusedMonitor(ctx)
	if err != nil {
		return Monitor{}, nil, err
	}
	outputs, err := c.Outputs(ctx)
	if err != nil {
		return Monitor{}, nil, err
	}
	monitor, err := MatchWaylandOutputByName(outputs, focused)
	if err != nil {
		return Monitor{}, nil, err
	}
	handle, ok := c.OutputHandle(monitor.Name)
	if !ok {
		return Monitor{}, nil, fmt.Errorf("Wayland output %q has no bound wl_output handle", monitor.Name)
	}
	return monitor, handle, nil
}

func (c *WaylandClient) initialize(ctx context.Context) error {
	registry, err := c.display.GetRegistry()
	if err != nil {
		return fmt.Errorf("request Wayland registry: %w", err)
	}
	c.registry = registry

	var globals []WaylandGlobal
	registry.SetGlobalHandler(func(event wlclient.RegistryGlobalEvent) {
		globals = append(globals, WaylandGlobal{
			Name:      event.Name,
			Interface: event.Interface,
			Version:   event.Version,
		})
	})
	if err := c.roundTrip(ctx); err != nil {
		return fmt.Errorf("read Wayland registry globals: %w", err)
	}

	plan, err := buildWaylandBindingPlan(globals)
	if err != nil {
		return err
	}
	if err := c.bindPlan(plan); err != nil {
		return err
	}
	if err := c.roundTrip(ctx); err != nil {
		return fmt.Errorf("read Wayland bound global state: %w", err)
	}
	if err := c.validateSeatCapabilities(); err != nil {
		return err
	}
	if _, err := c.outputMonitors(); err != nil {
		return err
	}
	return nil
}

func (c *WaylandClient) bindPlan(plan waylandBindingPlan) error {
	if err := c.bindCore(plan); err != nil {
		return err
	}
	if err := c.bindOutputs(plan.Outputs); err != nil {
		return err
	}
	if plan.XDGOutputManager.Valid() {
		if err := c.bindXDGOutputs(plan.XDGOutputManager); err != nil {
			return err
		}
	}
	return nil
}

func (c *WaylandClient) bindCore(plan waylandBindingPlan) error {
	ctx := c.display.Context()
	compositor := wlclient.NewCompositor(ctx)
	if err := c.registry.Bind(plan.Compositor.Global.Name, waylandInterfaceCompositor, plan.Compositor.Version, compositor); err != nil {
		return fmt.Errorf("bind %s: %w", waylandInterfaceCompositor, err)
	}

	shm := wlclient.NewShm(ctx)
	if err := c.registry.Bind(plan.Shm.Global.Name, waylandInterfaceShm, plan.Shm.Version, shm); err != nil {
		return fmt.Errorf("bind %s: %w", waylandInterfaceShm, err)
	}

	seat := wlclient.NewSeat(ctx)
	seat.SetCapabilitiesHandler(func(event wlclient.SeatCapabilitiesEvent) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.seatCapabilities = event.Capabilities
	})
	seat.SetNameHandler(func(event wlclient.SeatNameEvent) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.seatName = event.Name
	})
	if err := c.registry.Bind(plan.Seat.Global.Name, waylandInterfaceSeat, plan.Seat.Version, seat); err != nil {
		return fmt.Errorf("bind %s: %w", waylandInterfaceSeat, err)
	}

	layerShell := wlrlayershell.NewLayerShell(ctx)
	if err := c.registry.Bind(plan.LayerShell.Global.Name, waylandInterfaceLayerShell, plan.LayerShell.Version, layerShell); err != nil {
		return fmt.Errorf("bind %s: %w", waylandInterfaceLayerShell, err)
	}

	virtualPointerManager := wlrvirtualpointer.NewVirtualPointerManager(ctx)
	if err := c.registry.Bind(plan.VirtualPointerManager.Global.Name, waylandInterfaceVirtualPointerManager, plan.VirtualPointerManager.Version, virtualPointerManager); err != nil {
		return fmt.Errorf("bind %s: %w", waylandInterfaceVirtualPointerManager, err)
	}

	c.mu.Lock()
	c.compositor = compositor
	c.shm = shm
	c.seat = seat
	c.layerShell = layerShell
	c.virtualPointerManager = virtualPointerManager
	c.mu.Unlock()
	return nil
}

func (c *WaylandClient) bindOutputs(outputs []waylandGlobalBinding) error {
	ctx := c.display.Context()
	for _, binding := range outputs {
		state := newWaylandOutputState(binding)
		output := wlclient.NewOutput(ctx)
		output.SetGeometryHandler(func(event wlclient.OutputGeometryEvent) {
			c.mu.Lock()
			state.applyWLGeometry(int(event.X), int(event.Y), int(event.Transform))
			listeners := c.outputListenerSnapshotLocked(state)
			c.mu.Unlock()
			notifyWaylandOutputListeners(listeners)
		})
		output.SetModeHandler(func(event wlclient.OutputModeEvent) {
			c.mu.Lock()
			state.applyWLMode(event.Flags, int(event.Width), int(event.Height))
			listeners := c.outputListenerSnapshotLocked(state)
			c.mu.Unlock()
			notifyWaylandOutputListeners(listeners)
		})
		output.SetScaleHandler(func(event wlclient.OutputScaleEvent) {
			c.mu.Lock()
			state.applyWLScale(int(event.Factor))
			listeners := c.outputListenerSnapshotLocked(state)
			c.mu.Unlock()
			notifyWaylandOutputListeners(listeners)
		})
		output.SetNameHandler(func(event wlclient.OutputNameEvent) {
			c.mu.Lock()
			state.applyWLName(event.Name)
			listeners := c.outputListenerSnapshotLocked(state)
			c.mu.Unlock()
			notifyWaylandOutputListeners(listeners)
		})
		if err := c.registry.Bind(binding.Global.Name, waylandInterfaceOutput, binding.Version, output); err != nil {
			return fmt.Errorf("bind %s global %d: %w", waylandInterfaceOutput, binding.Global.Name, err)
		}
		c.mu.Lock()
		c.outputs[binding.Global.Name] = state
		c.outputHandles[binding.Global.Name] = output
		c.mu.Unlock()
	}
	return nil
}

func (c *WaylandClient) bindXDGOutputs(binding waylandGlobalBinding) error {
	ctx := c.display.Context()
	c.xdgOutputManager = xdgoutput.NewOutputManager(ctx)
	if err := c.registry.Bind(binding.Global.Name, waylandInterfaceXDGOutputManager, binding.Version, c.xdgOutputManager); err != nil {
		return fmt.Errorf("bind %s: %w", waylandInterfaceXDGOutputManager, err)
	}

	c.mu.RLock()
	globalNames := make([]uint32, 0, len(c.outputs))
	for globalName := range c.outputs {
		globalNames = append(globalNames, globalName)
	}
	sort.Slice(globalNames, func(i, j int) bool { return globalNames[i] < globalNames[j] })
	for _, globalName := range globalNames {
		state := c.outputs[globalName]
		handle := c.outputHandles[globalName]
		c.mu.RUnlock()
		xdgOutput, err := c.xdgOutputManager.GetXdgOutput(handle)
		if err != nil {
			return fmt.Errorf("create zxdg_output for wl_output global %d: %w", globalName, err)
		}
		xdgOutput.SetLogicalPositionHandler(func(event xdgoutput.OutputLogicalPositionEvent) {
			c.mu.Lock()
			state.applyXDGLogicalPosition(int(event.X), int(event.Y))
			listeners := c.outputListenerSnapshotLocked(state)
			c.mu.Unlock()
			notifyWaylandOutputListeners(listeners)
		})
		xdgOutput.SetLogicalSizeHandler(func(event xdgoutput.OutputLogicalSizeEvent) {
			c.mu.Lock()
			state.applyXDGLogicalSize(int(event.Width), int(event.Height))
			listeners := c.outputListenerSnapshotLocked(state)
			c.mu.Unlock()
			notifyWaylandOutputListeners(listeners)
		})
		xdgOutput.SetNameHandler(func(event xdgoutput.OutputNameEvent) {
			c.mu.Lock()
			state.applyXDGName(event.Name)
			listeners := c.outputListenerSnapshotLocked(state)
			c.mu.Unlock()
			notifyWaylandOutputListeners(listeners)
		})
		c.mu.RLock()
	}
	c.mu.RUnlock()
	return nil
}

func (c *WaylandClient) roundTrip(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	callback, err := c.display.Sync()
	if err != nil {
		return err
	}
	defer callback.Destroy()

	done := false
	callback.SetDoneHandler(func(wlclient.CallbackDoneEvent) {
		done = true
	})

	conn, err := c.unixConn()
	if err != nil {
		return err
	}
	defer conn.SetReadDeadline(time.Time{})

	for !done {
		if err := c.protocolError(); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := conn.SetReadDeadline(nextWaylandDispatchDeadline(ctx)); err != nil {
			return fmt.Errorf("set Wayland dispatch deadline: %w", err)
		}
		if err := c.display.Context().Dispatch(); err != nil {
			if isTimeoutError(err) {
				if ctx.Err() != nil {
					return fmt.Errorf("Wayland roundtrip canceled: %w", ctx.Err())
				}
				continue
			}
			if ctx.Err() != nil {
				return fmt.Errorf("Wayland roundtrip canceled: %w", ctx.Err())
			}
			return err
		}
	}
	return c.protocolError()
}

func (c *WaylandClient) validateSeatCapabilities() error {
	const required = uint32(wlclient.SeatCapabilityKeyboard | wlclient.SeatCapabilityPointer)
	c.mu.RLock()
	capabilities := c.seatCapabilities
	seatName := c.seatName
	c.mu.RUnlock()
	if capabilities&required == required {
		return nil
	}
	var missing []string
	if capabilities&uint32(wlclient.SeatCapabilityKeyboard) == 0 {
		missing = append(missing, "keyboard")
	}
	if capabilities&uint32(wlclient.SeatCapabilityPointer) == 0 {
		missing = append(missing, "pointer")
	}
	if seatName == "" {
		seatName = "<unnamed>"
	}
	return fmt.Errorf("Wayland seat %s is missing required capabilities: %v", seatName, missing)
}

func (c *WaylandClient) outputMonitors() ([]Monitor, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	globalNames := make([]uint32, 0, len(c.outputs))
	for globalName := range c.outputs {
		globalNames = append(globalNames, globalName)
	}
	sort.Slice(globalNames, func(i, j int) bool { return globalNames[i] < globalNames[j] })

	monitors := make([]Monitor, 0, len(globalNames))
	for _, globalName := range globalNames {
		monitor, err := c.outputs[globalName].monitor()
		if err != nil {
			return nil, err
		}
		monitors = append(monitors, monitor)
	}
	return monitors, nil
}

func (c *WaylandClient) setProtocolErr(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.protocolErr = err
}

func (c *WaylandClient) protocolError() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.protocolErr
}

func (c *WaylandClient) unixConn() (*net.UnixConn, error) {
	ctx := c.display.Context()
	value := reflect.ValueOf(ctx).Elem().FieldByName("conn")
	if !value.IsValid() || value.IsNil() {
		return nil, fmt.Errorf("Wayland context connection is unavailable")
	}
	conn, ok := reflect.NewAt(value.Type(), unsafe.Pointer(value.UnsafeAddr())).Elem().Interface().(*net.UnixConn)
	if !ok || conn == nil {
		return nil, fmt.Errorf("Wayland context connection has unexpected type %s", value.Type())
	}
	return conn, nil
}

func nextWaylandDispatchDeadline(ctx context.Context) time.Time {
	next := time.Now().Add(waylandDispatchPollInterval)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(next) {
		return deadline
	}
	return next
}

func isTimeoutError(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
