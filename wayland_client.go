package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"mousekeys/internal/wayland/wlr"

	"github.com/rajveermalviya/go-wayland/wayland/client"
	xdgoutput "github.com/rajveermalviya/go-wayland/wayland/unstable/xdg-output-v1"
)

const (
	waylandClientInitTimeout = 2 * time.Second

	waylandGlobalCompositor            = "wl_compositor"
	waylandGlobalShm                   = "wl_shm"
	waylandGlobalSeat                  = "wl_seat"
	waylandGlobalOutput                = "wl_output"
	waylandGlobalLayerShell            = "zwlr_layer_shell_v1"
	waylandGlobalVirtualPointerManager = "zwlr_virtual_pointer_manager_v1"
	waylandGlobalXDGOutputManager      = "zxdg_output_manager_v1"

	waylandCompositorBindVersion            = 6
	waylandSeatBindVersion                  = 7
	waylandOutputBindVersion                = 4
	waylandLayerShellBindVersion            = 5
	waylandVirtualPointerManagerBindVersion = 2
	waylandXDGOutputManagerBindVersion      = 3
)

type WaylandOutputInfo struct {
	GlobalName     uint32  `json:"global_name"`
	Name           string  `json:"name"`
	LogicalX       int     `json:"logical_x"`
	LogicalY       int     `json:"logical_y"`
	LogicalWidth   int     `json:"logical_width"`
	LogicalHeight  int     `json:"logical_height"`
	Scale          float64 `json:"scale"`
	WLName         string  `json:"wl_name,omitempty"`
	XDGName        string  `json:"xdg_name,omitempty"`
	Description    string  `json:"description,omitempty"`
	UsingXDGOutput bool    `json:"using_xdg_output"`
}

type WaylandClientBase struct {
	mu sync.RWMutex

	socketPath  string
	displayName string
	driver      waylandBaseDriver

	display               *client.Display
	registry              *client.Registry
	compositor            *client.Compositor
	shm                   *client.Shm
	seat                  *client.Seat
	keyboard              *client.Keyboard
	pointer               *client.Pointer
	layerShell            *wlr.LayerShell
	virtualPointerManager *wlr.VirtualPointerManager

	hasCompositor            bool
	hasShm                   bool
	hasSeat                  bool
	hasLayerShell            bool
	hasVirtualPointerManager bool
	hasXDGOutputManager      bool

	seatGlobal       uint32
	seatCapabilities uint32
	seatName         string
	keyboardCapable  bool
	pointerCapable   bool
	keyboardBound    bool
	pointerBound     bool

	outputs map[uint32]*waylandOutputState
	bindErr []error
}

type waylandClientBaseOptions struct {
	Getenv  getenvFunc
	Timeout time.Duration
	Driver  waylandBaseDriver
}

type waylandBaseDriver interface {
	Open(ctx context.Context, socketPath string, c *WaylandClientBase) error
	Close() error
}

func OpenWaylandClientBase(ctx context.Context, getenv getenvFunc) (*WaylandClientBase, error) {
	return openWaylandClientBase(ctx, waylandClientBaseOptions{Getenv: getenv})
}

func openWaylandClientBase(ctx context.Context, opts waylandClientBaseOptions) (*WaylandClientBase, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	displayName, socketPath, err := waylandSocketPathFromEnv(opts.Getenv)
	if err != nil {
		return nil, err
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = waylandClientInitTimeout
	}
	if timeout > 0 {
		if _, ok := ctx.Deadline(); !ok {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
	}

	driver := opts.Driver
	if driver == nil {
		driver = &realWaylandBaseDriver{}
	}
	c := &WaylandClientBase{
		socketPath:  socketPath,
		displayName: displayName,
		driver:      driver,
		outputs:     make(map[uint32]*waylandOutputState),
	}
	if err := driver.Open(ctx, socketPath, c); err != nil {
		_ = driver.Close()
		return nil, fmt.Errorf("initialize Wayland client from %q: %w", socketPath, err)
	}
	if err := c.validateRequired(); err != nil {
		_ = driver.Close()
		return nil, err
	}
	return c, nil
}

func (c *WaylandClientBase) Close(ctx context.Context) error {
	if c == nil || c.driver == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	done := make(chan error, 1)
	go func() {
		done <- c.driver.Close()
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *WaylandClientBase) SocketPath() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.socketPath
}

func (c *WaylandClientBase) DisplayName() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.displayName
}

func (c *WaylandClientBase) Outputs() []WaylandOutputInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	outputs := make([]WaylandOutputInfo, 0, len(c.outputs))
	for _, output := range c.outputs {
		outputs = append(outputs, output.snapshot())
	}
	sort.Slice(outputs, func(i, j int) bool {
		if outputs[i].Name == outputs[j].Name {
			return outputs[i].GlobalName < outputs[j].GlobalName
		}
		return outputs[i].Name < outputs[j].Name
	})
	return outputs
}

func (c *WaylandClientBase) OutputForMonitor(m Monitor) (WaylandOutputInfo, error) {
	if err := m.Validate(); err != nil {
		return WaylandOutputInfo{}, err
	}
	outputs := c.Outputs()
	for _, output := range outputs {
		if output.Name == m.Name {
			return output, nil
		}
	}
	for _, output := range outputs {
		if output.Name == "" &&
			output.LogicalX == m.OriginX &&
			output.LogicalY == m.OriginY &&
			output.LogicalWidth == m.LogicalWidth &&
			output.LogicalHeight == m.LogicalHeight {
			return output, nil
		}
	}
	names := make([]string, 0, len(outputs))
	for _, output := range outputs {
		if output.Name != "" {
			names = append(names, output.Name)
		}
	}
	if len(names) == 0 {
		return WaylandOutputInfo{}, fmt.Errorf("no Wayland output name matched Hyprland monitor %q: compositor did not provide wl_output.name or zxdg_output.name", m.Name)
	}
	return WaylandOutputInfo{}, fmt.Errorf("no Wayland output name matched Hyprland monitor %q; available Wayland outputs: %s", m.Name, strings.Join(names, ", "))
}

func waylandSocketPathFromEnv(getenv getenvFunc) (string, string, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	runtimeDir := strings.TrimSpace(getenv("XDG_RUNTIME_DIR"))
	displayName := strings.TrimSpace(getenv("WAYLAND_DISPLAY"))
	var missing []string
	if runtimeDir == "" {
		missing = append(missing, "XDG_RUNTIME_DIR")
	}
	if displayName == "" {
		missing = append(missing, "WAYLAND_DISPLAY")
	}
	if len(missing) > 0 {
		return "", "", fmt.Errorf("missing required Wayland client environment: %s", strings.Join(missing, ", "))
	}
	if !filepath.IsAbs(runtimeDir) {
		return "", "", fmt.Errorf("invalid XDG_RUNTIME_DIR %q for Wayland client: must be an absolute path", runtimeDir)
	}
	info, err := os.Stat(runtimeDir)
	if err != nil {
		return "", "", fmt.Errorf("invalid XDG_RUNTIME_DIR %q for Wayland client: %w", runtimeDir, err)
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("invalid XDG_RUNTIME_DIR %q for Wayland client: not a directory", runtimeDir)
	}

	socketPath := displayName
	if !filepath.IsAbs(socketPath) {
		socketPath = filepath.Join(runtimeDir, displayName)
	}
	socketInfo, err := os.Lstat(socketPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", fmt.Errorf("Wayland socket %q from WAYLAND_DISPLAY=%q does not exist: %w", socketPath, displayName, err)
		}
		return "", "", fmt.Errorf("stat Wayland socket %q from WAYLAND_DISPLAY=%q: %w", socketPath, displayName, err)
	}
	if socketInfo.Mode()&os.ModeSocket == 0 {
		return "", "", fmt.Errorf("Wayland socket path %q from WAYLAND_DISPLAY=%q is not a Unix socket", socketPath, displayName)
	}
	return displayName, socketPath, nil
}

type waylandBindingKind int

const (
	waylandBindingNone waylandBindingKind = iota
	waylandBindingCompositor
	waylandBindingShm
	waylandBindingSeat
	waylandBindingOutput
	waylandBindingLayerShell
	waylandBindingVirtualPointerManager
	waylandBindingXDGOutputManager
)

type waylandGlobalBinding struct {
	Kind      waylandBindingKind
	Name      uint32
	Interface string
	Version   uint32
}

type waylandSeatBindingNeeds struct {
	Keyboard bool
	Pointer  bool
}

func (c *WaylandClientBase) handleRegistryGlobal(name uint32, iface string, version uint32) waylandGlobalBinding {
	c.mu.Lock()
	defer c.mu.Unlock()

	binding := waylandGlobalBinding{Name: name, Interface: iface, Version: version}
	switch iface {
	case waylandGlobalCompositor:
		if !c.hasCompositor {
			c.hasCompositor = true
			binding.Kind = waylandBindingCompositor
			binding.Version = minUint32(version, waylandCompositorBindVersion)
		}
	case waylandGlobalShm:
		if !c.hasShm {
			c.hasShm = true
			binding.Kind = waylandBindingShm
			binding.Version = 1
		}
	case waylandGlobalSeat:
		if !c.hasSeat {
			c.hasSeat = true
			c.seatGlobal = name
			binding.Kind = waylandBindingSeat
			binding.Version = minUint32(version, waylandSeatBindVersion)
		}
	case waylandGlobalOutput:
		if _, ok := c.outputs[name]; !ok {
			c.outputs[name] = newWaylandOutputState(name, version)
			binding.Kind = waylandBindingOutput
			binding.Version = minUint32(version, waylandOutputBindVersion)
		}
	case waylandGlobalLayerShell:
		if !c.hasLayerShell {
			c.hasLayerShell = true
			binding.Kind = waylandBindingLayerShell
			binding.Version = minUint32(version, waylandLayerShellBindVersion)
		}
	case waylandGlobalVirtualPointerManager:
		if !c.hasVirtualPointerManager {
			c.hasVirtualPointerManager = true
			binding.Kind = waylandBindingVirtualPointerManager
			binding.Version = minUint32(version, waylandVirtualPointerManagerBindVersion)
		}
	case waylandGlobalXDGOutputManager:
		if !c.hasXDGOutputManager {
			c.hasXDGOutputManager = true
			binding.Kind = waylandBindingXDGOutputManager
			binding.Version = minUint32(version, waylandXDGOutputManagerBindVersion)
		}
	}
	return binding
}

func (c *WaylandClientBase) handleRegistryGlobalRemove(name uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.outputs, name)
	if c.seatGlobal == name {
		c.seatGlobal = 0
		c.hasSeat = false
		c.keyboardBound = false
		c.pointerBound = false
		c.keyboardCapable = false
		c.pointerCapable = false
	}
}

func (c *WaylandClientBase) noteDisplay(display *client.Display) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.display = display
}

func (c *WaylandClientBase) noteRegistry(registry *client.Registry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.registry = registry
}

func (c *WaylandClientBase) noteCompositor(compositor *client.Compositor, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.compositor = compositor
	c.appendBindErrLocked(waylandGlobalCompositor, err)
}

func (c *WaylandClientBase) noteShm(shm *client.Shm, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.shm = shm
	c.appendBindErrLocked(waylandGlobalShm, err)
}

func (c *WaylandClientBase) noteSeat(seat *client.Seat, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seat = seat
	c.appendBindErrLocked(waylandGlobalSeat, err)
}

func (c *WaylandClientBase) noteLayerShell(layerShell *wlr.LayerShell, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.layerShell = layerShell
	c.appendBindErrLocked(waylandGlobalLayerShell, err)
}

func (c *WaylandClientBase) noteVirtualPointerManager(manager *wlr.VirtualPointerManager, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.virtualPointerManager = manager
	c.appendBindErrLocked(waylandGlobalVirtualPointerManager, err)
}

func (c *WaylandClientBase) noteKeyboard(keyboard *client.Keyboard, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil {
		c.keyboard = keyboard
		c.keyboardBound = true
	}
	c.appendBindErrLocked("wl_keyboard", err)
}

func (c *WaylandClientBase) notePointer(pointer *client.Pointer, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil {
		c.pointer = pointer
		c.pointerBound = true
	}
	c.appendBindErrLocked("wl_pointer", err)
}

func (c *WaylandClientBase) noteSeatName(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seatName = name
}

func (c *WaylandClientBase) handleSeatCapabilities(capabilities uint32) waylandSeatBindingNeeds {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.seatCapabilities = capabilities
	c.keyboardCapable = capabilities&uint32(client.SeatCapabilityKeyboard) != 0
	c.pointerCapable = capabilities&uint32(client.SeatCapabilityPointer) != 0
	return waylandSeatBindingNeeds{
		Keyboard: c.keyboardCapable && !c.keyboardBound,
		Pointer:  c.pointerCapable && !c.pointerBound,
	}
}

func (c *WaylandClientBase) noteOutput(global uint32, output *client.Output, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if state := c.outputs[global]; state != nil && err == nil {
		state.proxy = output
	}
	c.appendBindErrLocked(fmt.Sprintf("%s:%d", waylandGlobalOutput, global), err)
}

func (c *WaylandClientBase) pendingXDGOutputBindings() []uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.hasXDGOutputManager {
		return nil
	}
	var globals []uint32
	for global, output := range c.outputs {
		if !output.xdgRequested {
			output.xdgRequested = true
			globals = append(globals, global)
		}
	}
	sort.Slice(globals, func(i, j int) bool { return globals[i] < globals[j] })
	return globals
}

func (c *WaylandClientBase) handleOutputGeometry(global uint32, ev client.OutputGeometryEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if output := c.outputs[global]; output != nil {
		output.geometrySet = true
		output.x = int(ev.X)
		output.y = int(ev.Y)
		output.physicalWidthMM = int(ev.PhysicalWidth)
		output.physicalHeightMM = int(ev.PhysicalHeight)
		output.transform = int(ev.Transform)
	}
}

func (c *WaylandClientBase) handleOutputMode(global uint32, ev client.OutputModeEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if output := c.outputs[global]; output != nil {
		if ev.Flags&uint32(client.OutputModeCurrent) != 0 || !output.modeSet {
			output.modeSet = true
			output.modeWidth = int(ev.Width)
			output.modeHeight = int(ev.Height)
		}
	}
}

func (c *WaylandClientBase) handleOutputScale(global uint32, ev client.OutputScaleEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if output := c.outputs[global]; output != nil && ev.Factor > 0 {
		output.scale = int(ev.Factor)
	}
}

func (c *WaylandClientBase) handleOutputName(global uint32, ev client.OutputNameEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if output := c.outputs[global]; output != nil {
		output.wlName = ev.Name
	}
}

func (c *WaylandClientBase) handleOutputDescription(global uint32, ev client.OutputDescriptionEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if output := c.outputs[global]; output != nil {
		output.description = ev.Description
	}
}

func (c *WaylandClientBase) handleXDGOutputLogicalPosition(global uint32, ev xdgoutput.OutputLogicalPositionEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if output := c.outputs[global]; output != nil {
		output.xdgPositionSet = true
		output.xdgX = int(ev.X)
		output.xdgY = int(ev.Y)
	}
}

func (c *WaylandClientBase) handleXDGOutputLogicalSize(global uint32, ev xdgoutput.OutputLogicalSizeEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if output := c.outputs[global]; output != nil {
		output.xdgSizeSet = true
		output.xdgWidth = int(ev.Width)
		output.xdgHeight = int(ev.Height)
	}
}

func (c *WaylandClientBase) handleXDGOutputName(global uint32, ev xdgoutput.OutputNameEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if output := c.outputs[global]; output != nil {
		output.xdgName = ev.Name
	}
}

func (c *WaylandClientBase) handleXDGOutputDescription(global uint32, ev xdgoutput.OutputDescriptionEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if output := c.outputs[global]; output != nil && output.description == "" {
		output.description = ev.Description
	}
}

func (c *WaylandClientBase) recordBindError(label string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.appendBindErrLocked(label, err)
}

func (c *WaylandClientBase) appendBindErrLocked(label string, err error) {
	if err != nil {
		c.bindErr = append(c.bindErr, fmt.Errorf("bind %s: %w", label, err))
	}
}

func (c *WaylandClientBase) validateRequired() error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.bindErr) > 0 {
		return errors.Join(c.bindErr...)
	}
	var missing []string
	if !c.hasCompositor {
		missing = append(missing, waylandGlobalCompositor)
	}
	if !c.hasShm {
		missing = append(missing, waylandGlobalShm)
	}
	if !c.hasSeat {
		missing = append(missing, waylandGlobalSeat)
	}
	if !c.hasLayerShell {
		missing = append(missing, waylandGlobalLayerShell)
	}
	if !c.hasVirtualPointerManager {
		missing = append(missing, waylandGlobalVirtualPointerManager)
	}
	if len(c.outputs) == 0 {
		missing = append(missing, waylandGlobalOutput)
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required Wayland global(s): %s", strings.Join(missing, ", "))
	}
	var seatMissing []string
	if !c.keyboardCapable || !c.keyboardBound {
		seatMissing = append(seatMissing, "keyboard")
	}
	if !c.pointerCapable || !c.pointerBound {
		seatMissing = append(seatMissing, "pointer")
	}
	if len(seatMissing) > 0 {
		return fmt.Errorf("Wayland seat %q did not advertise or bind required capability/capabilities: %s", c.seatName, strings.Join(seatMissing, ", "))
	}
	return nil
}

type waylandOutputState struct {
	global  uint32
	version uint32
	proxy   *client.Output

	geometrySet      bool
	x                int
	y                int
	physicalWidthMM  int
	physicalHeightMM int
	transform        int

	modeSet    bool
	modeWidth  int
	modeHeight int
	scale      int

	wlName      string
	description string

	xdgRequested   bool
	xdgPositionSet bool
	xdgX           int
	xdgY           int
	xdgSizeSet     bool
	xdgWidth       int
	xdgHeight      int
	xdgName        string
}

func newWaylandOutputState(global uint32, version uint32) *waylandOutputState {
	return &waylandOutputState{
		global:  global,
		version: version,
		scale:   1,
	}
}

func (o *waylandOutputState) snapshot() WaylandOutputInfo {
	name := o.wlName
	if name == "" {
		name = o.xdgName
	}
	x, y := o.x, o.y
	usingXDG := false
	if o.xdgPositionSet {
		x, y = o.xdgX, o.xdgY
		usingXDG = true
	}
	width, height := o.logicalSize()
	if o.xdgSizeSet {
		usingXDG = true
	}
	return WaylandOutputInfo{
		GlobalName:     o.global,
		Name:           name,
		LogicalX:       x,
		LogicalY:       y,
		LogicalWidth:   width,
		LogicalHeight:  height,
		Scale:          o.effectiveScale(width, height),
		WLName:         o.wlName,
		XDGName:        o.xdgName,
		Description:    o.description,
		UsingXDGOutput: usingXDG,
	}
}

func (o *waylandOutputState) logicalSize() (int, int) {
	if o.xdgSizeSet && o.xdgWidth > 0 && o.xdgHeight > 0 {
		return o.xdgWidth, o.xdgHeight
	}
	if !o.modeSet || o.modeWidth <= 0 || o.modeHeight <= 0 {
		return 0, 0
	}
	width, height := o.modeWidth, o.modeHeight
	if waylandTransformSwapsAxes(o.transform) {
		width, height = height, width
	}
	scale := o.scale
	if scale <= 0 {
		scale = 1
	}
	return int(math.Round(float64(width) / float64(scale))), int(math.Round(float64(height) / float64(scale)))
}

func (o *waylandOutputState) effectiveScale(logicalWidth, logicalHeight int) float64 {
	if o.modeSet && logicalWidth > 0 && logicalHeight > 0 {
		width, height := o.modeWidth, o.modeHeight
		if waylandTransformSwapsAxes(o.transform) {
			width, height = height, width
		}
		scaleX := float64(width) / float64(logicalWidth)
		scaleY := float64(height) / float64(logicalHeight)
		if scaleX > 0 && scaleY > 0 {
			return math.Round(((scaleX+scaleY)/2)*1000) / 1000
		}
	}
	if o.scale > 0 {
		return float64(o.scale)
	}
	return 1
}

func waylandTransformSwapsAxes(transform int) bool {
	switch transform {
	case 1, 3, 5, 7:
		return true
	default:
		return false
	}
}

type realWaylandBaseDriver struct {
	display          *client.Display
	registry         *client.Registry
	xdgOutputManager *xdgoutput.OutputManager
	outputs          map[uint32]*client.Output
}

func (d *realWaylandBaseDriver) Open(ctx context.Context, socketPath string, c *WaylandClientBase) error {
	display, err := client.Connect(socketPath)
	if err != nil {
		return fmt.Errorf("connect to Wayland socket %q: %w", socketPath, err)
	}
	d.display = display
	d.outputs = make(map[uint32]*client.Output)
	c.noteDisplay(display)

	registry, err := display.GetRegistry()
	if err != nil {
		return fmt.Errorf("get Wayland registry: %w", err)
	}
	d.registry = registry
	c.noteRegistry(registry)

	registry.SetGlobalHandler(func(ev client.RegistryGlobalEvent) {
		binding := c.handleRegistryGlobal(ev.Name, ev.Interface, ev.Version)
		if err := d.bindGlobal(binding, c); err != nil {
			c.recordBindError(ev.Interface, err)
		}
	})
	registry.SetGlobalRemoveHandler(func(ev client.RegistryGlobalRemoveEvent) {
		c.handleRegistryGlobalRemove(ev.Name)
	})

	if err := d.roundtrip(ctx); err != nil {
		return fmt.Errorf("Wayland registry roundtrip: %w", err)
	}
	if err := d.bindPendingXDGOutputs(c); err != nil {
		return err
	}
	if err := d.roundtrip(ctx); err != nil {
		return fmt.Errorf("Wayland initial event roundtrip: %w", err)
	}
	return nil
}

func (d *realWaylandBaseDriver) bindGlobal(binding waylandGlobalBinding, c *WaylandClientBase) error {
	if binding.Kind == waylandBindingNone {
		return nil
	}
	ctx := d.display.Context()
	switch binding.Kind {
	case waylandBindingCompositor:
		compositor := client.NewCompositor(ctx)
		err := d.registry.Bind(binding.Name, binding.Interface, binding.Version, compositor)
		c.noteCompositor(compositor, err)
	case waylandBindingShm:
		shm := client.NewShm(ctx)
		err := d.registry.Bind(binding.Name, binding.Interface, binding.Version, shm)
		c.noteShm(shm, err)
	case waylandBindingSeat:
		seat := client.NewSeat(ctx)
		seat.SetNameHandler(func(ev client.SeatNameEvent) {
			c.noteSeatName(ev.Name)
		})
		seat.SetCapabilitiesHandler(func(ev client.SeatCapabilitiesEvent) {
			needs := c.handleSeatCapabilities(ev.Capabilities)
			if needs.Keyboard {
				keyboard, err := seat.GetKeyboard()
				c.noteKeyboard(keyboard, err)
			}
			if needs.Pointer {
				pointer, err := seat.GetPointer()
				c.notePointer(pointer, err)
			}
		})
		err := d.registry.Bind(binding.Name, binding.Interface, binding.Version, seat)
		c.noteSeat(seat, err)
	case waylandBindingOutput:
		output := client.NewOutput(ctx)
		global := binding.Name
		output.SetGeometryHandler(func(ev client.OutputGeometryEvent) {
			c.handleOutputGeometry(global, ev)
		})
		output.SetModeHandler(func(ev client.OutputModeEvent) {
			c.handleOutputMode(global, ev)
		})
		output.SetScaleHandler(func(ev client.OutputScaleEvent) {
			c.handleOutputScale(global, ev)
		})
		output.SetNameHandler(func(ev client.OutputNameEvent) {
			c.handleOutputName(global, ev)
		})
		output.SetDescriptionHandler(func(ev client.OutputDescriptionEvent) {
			c.handleOutputDescription(global, ev)
		})
		err := d.registry.Bind(binding.Name, binding.Interface, binding.Version, output)
		d.outputs[global] = output
		c.noteOutput(global, output, err)
	case waylandBindingLayerShell:
		layerShell := wlr.NewLayerShell(ctx)
		err := d.registry.Bind(binding.Name, binding.Interface, binding.Version, layerShell)
		c.noteLayerShell(layerShell, err)
	case waylandBindingVirtualPointerManager:
		manager := wlr.NewVirtualPointerManager(ctx)
		err := d.registry.Bind(binding.Name, binding.Interface, binding.Version, manager)
		c.noteVirtualPointerManager(manager, err)
	case waylandBindingXDGOutputManager:
		manager := xdgoutput.NewOutputManager(ctx)
		err := d.registry.Bind(binding.Name, binding.Interface, binding.Version, manager)
		d.xdgOutputManager = manager
		if err != nil {
			c.recordBindError(binding.Interface, err)
		}
	}
	return nil
}

func (d *realWaylandBaseDriver) bindPendingXDGOutputs(c *WaylandClientBase) error {
	if d.xdgOutputManager == nil {
		return nil
	}
	for _, global := range c.pendingXDGOutputBindings() {
		output := d.outputs[global]
		if output == nil {
			c.recordBindError(fmt.Sprintf("zxdg_output_v1:%d", global), fmt.Errorf("missing bound wl_output proxy"))
			continue
		}
		xdgOutput, err := d.xdgOutputManager.GetXdgOutput(output)
		if err != nil {
			c.recordBindError(fmt.Sprintf("zxdg_output_v1:%d", global), err)
			continue
		}
		xdgOutput.SetLogicalPositionHandler(func(ev xdgoutput.OutputLogicalPositionEvent) {
			c.handleXDGOutputLogicalPosition(global, ev)
		})
		xdgOutput.SetLogicalSizeHandler(func(ev xdgoutput.OutputLogicalSizeEvent) {
			c.handleXDGOutputLogicalSize(global, ev)
		})
		xdgOutput.SetNameHandler(func(ev xdgoutput.OutputNameEvent) {
			c.handleXDGOutputName(global, ev)
		})
		xdgOutput.SetDescriptionHandler(func(ev xdgoutput.OutputDescriptionEvent) {
			c.handleXDGOutputDescription(global, ev)
		})
	}
	return nil
}

func (d *realWaylandBaseDriver) roundtrip(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	callback, err := d.display.Sync()
	if err != nil {
		return err
	}
	done := make(chan struct{})
	var once sync.Once
	callback.SetDoneHandler(func(client.CallbackDoneEvent) {
		once.Do(func() {
			close(done)
		})
	})
	defer callback.Destroy()

	for {
		select {
		case <-done:
			return nil
		default:
		}
		if err := d.dispatchOnce(ctx); err != nil {
			return err
		}
	}
}

func (d *realWaylandBaseDriver) dispatchOnce(ctx context.Context) error {
	errc := make(chan error, 1)
	go func() {
		errc <- d.display.Context().Dispatch()
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		_ = d.display.Context().Close()
		return ctx.Err()
	}
}

func (d *realWaylandBaseDriver) Close() error {
	if d == nil || d.display == nil {
		return nil
	}
	return d.display.Context().Close()
}

func minUint32(a uint32, b uint32) uint32 {
	if a < b {
		return a
	}
	return b
}
