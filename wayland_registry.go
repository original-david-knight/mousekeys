package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

const (
	waylandInterfaceCompositor                   = "wl_compositor"
	waylandInterfaceShm                          = "wl_shm"
	waylandInterfaceSeat                         = "wl_seat"
	waylandInterfaceOutput                       = "wl_output"
	waylandInterfaceXDGOutputManager             = "zxdg_output_manager_v1"
	waylandInterfaceLayerShell                   = "zwlr_layer_shell_v1"
	waylandInterfaceVirtualPointerManager        = "zwlr_virtual_pointer_manager_v1"
	waylandOutputModeCurrent              uint32 = 1
)

type WaylandGlobal struct {
	Name      uint32
	Interface string
	Version   uint32
}

type waylandGlobalBinding struct {
	Global  WaylandGlobal
	Version uint32
}

func (b waylandGlobalBinding) Valid() bool {
	return b.Global.Name != 0 && b.Global.Interface != "" && b.Version > 0
}

type waylandBindingPlan struct {
	Compositor            waylandGlobalBinding
	Shm                   waylandGlobalBinding
	Seat                  waylandGlobalBinding
	XDGOutputManager      waylandGlobalBinding
	LayerShell            waylandGlobalBinding
	VirtualPointerManager waylandGlobalBinding
	Outputs               []waylandGlobalBinding
}

func buildWaylandBindingPlan(globals []WaylandGlobal) (waylandBindingPlan, error) {
	var plan waylandBindingPlan
	var missing []string
	var versionErrors []string

	addRequired := func(target *waylandGlobalBinding, iface string, minVersion uint32, maxVersion uint32) {
		binding, found, foundVersion := chooseWaylandGlobal(globals, iface, minVersion, maxVersion)
		if !found {
			missing = append(missing, iface)
			return
		}
		if !binding.Valid() {
			versionErrors = append(versionErrors, fmt.Sprintf("%s requires version >=%d; compositor announced v%d", iface, minVersion, foundVersion))
			return
		}
		*target = binding
	}

	addRequired(&plan.Compositor, waylandInterfaceCompositor, 1, 6)
	addRequired(&plan.Shm, waylandInterfaceShm, 1, 1)
	addRequired(&plan.Seat, waylandInterfaceSeat, 1, 9)
	addRequired(&plan.LayerShell, waylandInterfaceLayerShell, 1, 4)
	addRequired(&plan.VirtualPointerManager, waylandInterfaceVirtualPointerManager, 1, 2)

	xdgOutputManager, _, _ := chooseWaylandGlobal(globals, waylandInterfaceXDGOutputManager, 1, 3)
	plan.XDGOutputManager = xdgOutputManager

	sawOutput := false
	for _, global := range globals {
		if global.Interface != waylandInterfaceOutput {
			continue
		}
		sawOutput = true
		if global.Version < 2 {
			versionErrors = append(versionErrors, fmt.Sprintf("%s global %d requires version >=2 for scale/done; compositor announced v%d", waylandInterfaceOutput, global.Name, global.Version))
			continue
		}
		plan.Outputs = append(plan.Outputs, waylandGlobalBinding{
			Global:  global,
			Version: minUint32(global.Version, 4),
		})
	}
	sort.Slice(plan.Outputs, func(i, j int) bool {
		return plan.Outputs[i].Global.Name < plan.Outputs[j].Global.Name
	})
	if !sawOutput {
		missing = append(missing, waylandInterfaceOutput)
	}

	if len(missing) > 0 {
		return waylandBindingPlan{}, fmt.Errorf("Wayland registry missing required globals: %s", strings.Join(missing, ", "))
	}
	if len(versionErrors) > 0 {
		return waylandBindingPlan{}, fmt.Errorf("Wayland registry has unsupported global versions: %s", strings.Join(versionErrors, "; "))
	}
	return plan, nil
}

func chooseWaylandGlobal(globals []WaylandGlobal, iface string, minVersion uint32, maxVersion uint32) (waylandGlobalBinding, bool, uint32) {
	var found bool
	var highestVersion uint32
	var best WaylandGlobal
	for _, global := range globals {
		if global.Interface != iface {
			continue
		}
		found = true
		if global.Version > highestVersion {
			highestVersion = global.Version
		}
		if global.Version < minVersion {
			continue
		}
		if !bestGlobalIsBetter(best, global) {
			best = global
		}
	}
	if !found {
		return waylandGlobalBinding{}, false, 0
	}
	if best.Name == 0 {
		return waylandGlobalBinding{}, true, highestVersion
	}
	return waylandGlobalBinding{
		Global:  best,
		Version: minUint32(best.Version, maxVersion),
	}, true, highestVersion
}

func bestGlobalIsBetter(current WaylandGlobal, candidate WaylandGlobal) bool {
	if current.Name == 0 {
		return false
	}
	if current.Version != candidate.Version {
		return current.Version > candidate.Version
	}
	return current.Name < candidate.Name
}

func minUint32(a, b uint32) uint32 {
	if a < b {
		return a
	}
	return b
}

type waylandOutputState struct {
	globalName    uint32
	globalVersion uint32

	wlName  string
	xdgName string

	geometryX     int
	geometryY     int
	transform     int
	hasGeometry   bool
	modeWidth     int
	modeHeight    int
	hasMode       bool
	scaleFactor   int
	hasScale      bool
	logicalX      int
	logicalY      int
	hasLogicalPos bool
	logicalWidth  int
	logicalHeight int
	hasLogicalSz  bool
}

func newWaylandOutputState(binding waylandGlobalBinding) *waylandOutputState {
	return &waylandOutputState{
		globalName:    binding.Global.Name,
		globalVersion: binding.Version,
		scaleFactor:   1,
	}
}

func (s *waylandOutputState) applyWLGeometry(x int, y int, transform int) {
	s.geometryX = x
	s.geometryY = y
	s.transform = transform
	s.hasGeometry = true
}

func (s *waylandOutputState) applyWLMode(flags uint32, width int, height int) {
	if flags&waylandOutputModeCurrent == 0 {
		return
	}
	s.modeWidth = width
	s.modeHeight = height
	s.hasMode = true
}

func (s *waylandOutputState) applyWLScale(factor int) {
	if factor <= 0 {
		return
	}
	s.scaleFactor = factor
	s.hasScale = true
}

func (s *waylandOutputState) applyWLName(name string) {
	s.wlName = name
}

func (s *waylandOutputState) applyXDGLogicalPosition(x int, y int) {
	s.logicalX = x
	s.logicalY = y
	s.hasLogicalPos = true
}

func (s *waylandOutputState) applyXDGLogicalSize(width int, height int) {
	s.logicalWidth = width
	s.logicalHeight = height
	s.hasLogicalSz = true
}

func (s *waylandOutputState) applyXDGName(name string) {
	s.xdgName = name
}

func (s *waylandOutputState) monitor() (Monitor, error) {
	if s == nil {
		return Monitor{}, fmt.Errorf("Wayland output state is nil")
	}

	name := s.name()
	if name == "" {
		return Monitor{}, fmt.Errorf("Wayland output global %d did not advertise a name via wl_output v4 or zxdg-output", s.globalName)
	}

	x, y, ok := s.logicalPosition()
	if !ok {
		return Monitor{}, fmt.Errorf("Wayland output %q did not advertise logical position or wl_output geometry", name)
	}
	width, height, ok := s.logicalSize()
	if !ok {
		return Monitor{}, fmt.Errorf("Wayland output %q did not advertise logical size or current mode", name)
	}

	monitor := Monitor{
		Name:   name,
		X:      x,
		Y:      y,
		Width:  width,
		Height: height,
		Scale:  s.effectiveScale(width, height),
	}
	if monitor.Scale <= 0 {
		monitor.Scale = 1
	}
	if monitor.Width <= 0 || monitor.Height <= 0 {
		return Monitor{}, fmt.Errorf("Wayland output %q has invalid logical size %dx%d", name, monitor.Width, monitor.Height)
	}
	return monitor, nil
}

func (s *waylandOutputState) name() string {
	if s.xdgName != "" {
		return s.xdgName
	}
	return s.wlName
}

func (s *waylandOutputState) logicalPosition() (int, int, bool) {
	if s.hasLogicalPos {
		return s.logicalX, s.logicalY, true
	}
	if s.hasGeometry {
		return s.geometryX, s.geometryY, true
	}
	return 0, 0, false
}

func (s *waylandOutputState) logicalSize() (int, int, bool) {
	if s.hasLogicalSz {
		return s.logicalWidth, s.logicalHeight, s.logicalWidth > 0 && s.logicalHeight > 0
	}
	physicalWidth, physicalHeight, ok := s.physicalSize()
	if !ok {
		return 0, 0, false
	}
	scale := s.scaleFactor
	if scale <= 0 {
		scale = 1
	}
	return int(math.Round(float64(physicalWidth) / float64(scale))), int(math.Round(float64(physicalHeight) / float64(scale))), true
}

func (s *waylandOutputState) effectiveScale(logicalWidth int, logicalHeight int) float64 {
	physicalWidth, physicalHeight, ok := s.physicalSize()
	if ok && logicalWidth > 0 && logicalHeight > 0 {
		xScale := float64(physicalWidth) / float64(logicalWidth)
		yScale := float64(physicalHeight) / float64(logicalHeight)
		if xScale > 0 && yScale > 0 {
			return (xScale + yScale) / 2
		}
	}
	if s.scaleFactor > 0 {
		return float64(s.scaleFactor)
	}
	return 1
}

func (s *waylandOutputState) physicalSize() (int, int, bool) {
	if !s.hasMode || s.modeWidth <= 0 || s.modeHeight <= 0 {
		return 0, 0, false
	}
	if waylandTransformSwapsAxes(s.transform) {
		return s.modeHeight, s.modeWidth, true
	}
	return s.modeWidth, s.modeHeight, true
}

func waylandTransformSwapsAxes(transform int) bool {
	return transform == 1 || transform == 3 || transform == 5 || transform == 7
}

func MatchWaylandOutputByName(outputs []Monitor, focused Monitor) (Monitor, error) {
	if focused.Name == "" {
		return Monitor{}, fmt.Errorf("focused monitor name is required to match Wayland output")
	}
	for _, output := range outputs {
		if output.Name != focused.Name {
			continue
		}
		output.Focused = true
		return output, nil
	}
	names := make([]string, 0, len(outputs))
	for _, output := range outputs {
		if output.Name != "" {
			names = append(names, output.Name)
		}
	}
	sort.Strings(names)
	return Monitor{}, fmt.Errorf("focused Hyprland monitor %q was not found in Wayland outputs [%s]", focused.Name, strings.Join(names, ", "))
}
