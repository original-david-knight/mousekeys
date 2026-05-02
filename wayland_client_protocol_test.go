package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	wlclient "github.com/rajveermalviya/go-wayland/wayland/client"
)

func TestOpenWaylandClientInitializesFromFakeProtocol(t *testing.T) {
	responder := newFakeWaylandProtocolResponder(t)
	socketPath := responder.Start()

	client, err := OpenWaylandClient(context.Background(), socketPath)
	if err != nil {
		t.Fatalf("open Wayland client: %v", err)
	}
	defer client.Close()

	if client.Compositor() == nil || client.Shm() == nil || client.Seat() == nil || client.LayerShell() == nil || client.VirtualPointerManager() == nil {
		t.Fatalf("client did not bind all required core globals")
	}

	outputs, err := client.Outputs(context.Background())
	if err != nil {
		t.Fatalf("outputs: %v", err)
	}
	if len(outputs) != 2 {
		t.Fatalf("outputs = %+v, want two outputs", outputs)
	}
	wantOutputs := []Monitor{
		{Name: "DP-1", X: 0, Y: 0, Width: 1920, Height: 1080, Scale: 1.0},
		{Name: "eDP-1", X: 1920, Y: -120, Width: 1600, Height: 900, Scale: 1.25},
	}
	for i, want := range wantOutputs {
		if outputs[i] != want {
			t.Fatalf("outputs[%d] = %+v, want %+v; requests=%+v", i, outputs[i], want, responder.Requests())
		}
	}

	focused, handle, err := client.FocusedOutput(context.Background(), &fakeFocusedMonitorLookup{monitor: Monitor{Name: "eDP-1"}})
	if err != nil {
		t.Fatalf("focused output: %v", err)
	}
	if focused.Name != "eDP-1" || !focused.Focused {
		t.Fatalf("focused = %+v, want focused eDP-1", focused)
	}
	if handle == nil {
		t.Fatalf("focused output handle is nil")
	}

	requests := responder.Requests()
	for _, want := range []string{
		waylandInterfaceCompositor,
		waylandInterfaceShm,
		waylandInterfaceSeat,
		waylandInterfaceOutput,
		waylandInterfaceLayerShell,
		waylandInterfaceVirtualPointerManager,
		waylandInterfaceXDGOutputManager,
		"zxdg_output_manager_v1.get_xdg_output",
	} {
		if !fakeWaylandRequestsContain(requests, want) {
			t.Fatalf("requests did not include %q: %+v", want, requests)
		}
	}
}

func TestOpenWaylandClientReportsSeatCapabilityFailureFromFakeProtocol(t *testing.T) {
	responder := newFakeWaylandProtocolResponder(t)
	responder.seatCapabilities = uint32(wlclient.SeatCapabilityPointer)
	socketPath := responder.Start()

	_, err := OpenWaylandClient(context.Background(), socketPath)
	if err == nil {
		t.Fatalf("open Wayland client succeeded with missing keyboard capability")
	}
	for _, want := range []string{"seat0", "keyboard"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want substring %q", err.Error(), want)
		}
	}
}

type fakeWaylandProtocolResponder struct {
	t                *testing.T
	socketPath       string
	listener         net.Listener
	seatName         string
	seatCapabilities uint32
	outputs          []fakeWaylandProtocolOutput

	mu       sync.Mutex
	requests []string
}

type fakeWaylandProtocolOutput struct {
	globalName     uint32
	wlName         string
	xdgName        string
	x              int32
	y              int32
	physicalWidth  int32
	physicalHeight int32
	scale          int32
	logicalX       int32
	logicalY       int32
	logicalWidth   int32
	logicalHeight  int32
}

type fakeWaylandRequest struct {
	sender  uint32
	opcode  uint32
	payload []byte
}

func newFakeWaylandProtocolResponder(t *testing.T) *fakeWaylandProtocolResponder {
	t.Helper()
	return &fakeWaylandProtocolResponder{
		t:          t,
		socketPath: filepath.Join(t.TempDir(), "wayland-test"),
		seatName:   "seat0",
		seatCapabilities: uint32(
			wlclient.SeatCapabilityKeyboard |
				wlclient.SeatCapabilityPointer,
		),
		outputs: []fakeWaylandProtocolOutput{
			{
				globalName:     4,
				wlName:         "DP-1",
				xdgName:        "DP-1",
				x:              0,
				y:              0,
				physicalWidth:  1920,
				physicalHeight: 1080,
				scale:          1,
				logicalX:       0,
				logicalY:       0,
				logicalWidth:   1920,
				logicalHeight:  1080,
			},
			{
				globalName:     5,
				wlName:         "wl-fallback",
				xdgName:        "eDP-1",
				x:              0,
				y:              0,
				physicalWidth:  2000,
				physicalHeight: 1125,
				scale:          2,
				logicalX:       1920,
				logicalY:       -120,
				logicalWidth:   1600,
				logicalHeight:  900,
			},
		},
	}
}

func (r *fakeWaylandProtocolResponder) Start() string {
	r.t.Helper()
	if err := os.MkdirAll(filepath.Dir(r.socketPath), 0o700); err != nil {
		r.t.Fatalf("create fake Wayland socket directory: %v", err)
	}
	listener, err := net.Listen("unix", r.socketPath)
	if err != nil {
		r.t.Fatalf("listen on fake Wayland socket: %v", err)
	}
	r.listener = listener
	r.t.Cleanup(func() {
		_ = listener.Close()
		_ = os.Remove(r.socketPath)
	})

	go r.acceptLoop()
	return r.socketPath
}

func (r *fakeWaylandProtocolResponder) Requests() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.requests...)
}

func (r *fakeWaylandProtocolResponder) acceptLoop() {
	conn, err := r.listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	r.handle(conn)
}

func (r *fakeWaylandProtocolResponder) handle(conn net.Conn) {
	var registryID uint32
	var stage int
	boundByGlobal := map[uint32]uint32{}
	outputGlobalByObject := map[uint32]uint32{}
	var xdgOutputManagerID uint32
	xdgOutputByOutputObject := map[uint32]uint32{}
	objectKindByID := map[uint32]string{}
	layerSurfaceBySurface := map[uint32]uint32{}
	layerSurfaceSize := map[uint32][2]uint32{}
	layerSurfaceConfigured := map[uint32]bool{}
	attachedBufferBySurface := map[uint32]uint32{}

	for {
		request, err := readFakeWaylandRequest(conn)
		if err != nil {
			return
		}
		r.recordRequest(describeFakeWaylandRequest(request, xdgOutputManagerID))

		switch {
		case request.sender == 1 && request.opcode == 1:
			registryID = fakeWaylandUint32(request.payload[0:4])
		case request.sender == 1 && request.opcode == 0:
			callbackID := fakeWaylandUint32(request.payload[0:4])
			if stage == 0 {
				r.sendRegistryGlobals(conn, registryID)
			} else {
				r.sendBoundState(conn, boundByGlobal, outputGlobalByObject, xdgOutputByOutputObject)
			}
			writeFakeWaylandMessage(conn, callbackID, 0, fakeWaylandU32(1))
			stage++
		case request.sender == registryID && request.opcode == 0:
			bind := parseFakeWaylandBind(request.payload)
			boundByGlobal[bind.globalName] = bind.objectID
			if bind.iface == waylandInterfaceOutput {
				outputGlobalByObject[bind.objectID] = bind.globalName
			}
			if bind.iface == waylandInterfaceXDGOutputManager {
				xdgOutputManagerID = bind.objectID
			}
			objectKindByID[bind.objectID] = bind.iface
		case xdgOutputManagerID != 0 && request.sender == xdgOutputManagerID && request.opcode == 1:
			xdgOutputID := fakeWaylandUint32(request.payload[0:4])
			outputID := fakeWaylandUint32(request.payload[4:8])
			xdgOutputByOutputObject[outputID] = xdgOutputID
			objectKindByID[xdgOutputID] = "zxdg_output_v1"
		case objectKindByID[request.sender] == waylandInterfaceCompositor && request.opcode == 0:
			surfaceID := fakeWaylandUint32(request.payload[0:4])
			objectKindByID[surfaceID] = "wl_surface"
			r.recordRequest("wl_compositor.create_surface")
		case objectKindByID[request.sender] == waylandInterfaceCompositor && request.opcode == 1:
			regionID := fakeWaylandUint32(request.payload[0:4])
			objectKindByID[regionID] = "wl_region"
			r.recordRequest("wl_compositor.create_region")
		case objectKindByID[request.sender] == waylandInterfaceLayerShell && request.opcode == 0:
			layerSurfaceID := fakeWaylandUint32(request.payload[0:4])
			surfaceID := fakeWaylandUint32(request.payload[4:8])
			objectKindByID[layerSurfaceID] = "zwlr_layer_surface_v1"
			layerSurfaceBySurface[surfaceID] = layerSurfaceID
			r.recordRequest("zwlr_layer_shell_v1.get_layer_surface")
		case objectKindByID[request.sender] == "zwlr_layer_surface_v1" && request.opcode == 0:
			width := fakeWaylandUint32(request.payload[0:4])
			height := fakeWaylandUint32(request.payload[4:8])
			layerSurfaceSize[request.sender] = [2]uint32{width, height}
			r.recordRequest(fmt.Sprintf("zwlr_layer_surface_v1.set_size:%dx%d", width, height))
		case objectKindByID[request.sender] == "zwlr_layer_surface_v1" && request.opcode == 4:
			interactivity := fakeWaylandUint32(request.payload[0:4])
			r.recordRequest(fmt.Sprintf("zwlr_layer_surface_v1.set_keyboard_interactivity:%d", interactivity))
		case objectKindByID[request.sender] == "zwlr_layer_surface_v1" && request.opcode == 6:
			serial := fakeWaylandUint32(request.payload[0:4])
			r.recordRequest(fmt.Sprintf("zwlr_layer_surface_v1.ack_configure:%d", serial))
		case objectKindByID[request.sender] == "zwlr_layer_surface_v1" && request.opcode == 7:
			delete(objectKindByID, request.sender)
			r.recordRequest("zwlr_layer_surface_v1.destroy")
		case objectKindByID[request.sender] == waylandInterfaceShm && request.opcode == 0:
			poolID := fakeWaylandUint32(request.payload[0:4])
			objectKindByID[poolID] = "wl_shm_pool"
			r.recordRequest("wl_shm.create_pool")
		case objectKindByID[request.sender] == "wl_shm_pool" && request.opcode == 0:
			bufferID := fakeWaylandUint32(request.payload[0:4])
			objectKindByID[bufferID] = "wl_buffer"
			r.recordRequest("wl_shm_pool.create_buffer")
		case objectKindByID[request.sender] == "wl_shm_pool" && request.opcode == 1:
			delete(objectKindByID, request.sender)
			r.recordRequest("wl_shm_pool.destroy")
		case objectKindByID[request.sender] == "wl_buffer" && request.opcode == 0:
			delete(objectKindByID, request.sender)
			r.recordRequest("wl_buffer.destroy")
		case objectKindByID[request.sender] == "wl_region" && request.opcode == 0:
			delete(objectKindByID, request.sender)
			r.recordRequest("wl_region.destroy")
		case objectKindByID[request.sender] == "wl_surface" && request.opcode == 1:
			attachedBufferBySurface[request.sender] = fakeWaylandUint32(request.payload[0:4])
			r.recordRequest("wl_surface.attach")
		case objectKindByID[request.sender] == "wl_surface" && request.opcode == 5:
			r.recordRequest("wl_surface.set_input_region")
		case objectKindByID[request.sender] == "wl_surface" && request.opcode == 6:
			r.recordRequest("wl_surface.commit")
			layerSurfaceID := layerSurfaceBySurface[request.sender]
			attachedBufferID := attachedBufferBySurface[request.sender]
			if layerSurfaceID != 0 && attachedBufferID == 0 && !layerSurfaceConfigured[layerSurfaceID] {
				size := layerSurfaceSize[layerSurfaceID]
				writeFakeWaylandMessage(conn, layerSurfaceID, 0, append(fakeWaylandU32(101), append(fakeWaylandU32(size[0]), fakeWaylandU32(size[1])...)...))
				layerSurfaceConfigured[layerSurfaceID] = true
			}
			if attachedBufferID != 0 {
				writeFakeWaylandMessage(conn, attachedBufferID, 0, nil)
			}
		case objectKindByID[request.sender] == "wl_surface" && request.opcode == 8:
			scale := fakeWaylandUint32(request.payload[0:4])
			r.recordRequest(fmt.Sprintf("wl_surface.set_buffer_scale:%d", scale))
		case objectKindByID[request.sender] == "wl_surface" && request.opcode == 9:
			r.recordRequest("wl_surface.damage_buffer")
		case objectKindByID[request.sender] == "wl_surface" && request.opcode == 0:
			delete(objectKindByID, request.sender)
			r.recordRequest("wl_surface.destroy")
		}
	}
}

func (r *fakeWaylandProtocolResponder) recordRequest(request string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, request)
}

func (r *fakeWaylandProtocolResponder) sendRegistryGlobals(conn net.Conn, registryID uint32) {
	globals := []WaylandGlobal{
		{Name: 1, Interface: waylandInterfaceCompositor, Version: 6},
		{Name: 2, Interface: waylandInterfaceShm, Version: 1},
		{Name: 3, Interface: waylandInterfaceSeat, Version: 7},
		{Name: 6, Interface: waylandInterfaceLayerShell, Version: 4},
		{Name: 7, Interface: waylandInterfaceVirtualPointerManager, Version: 2},
		{Name: 8, Interface: waylandInterfaceXDGOutputManager, Version: 3},
	}
	for _, output := range r.outputs {
		globals = append(globals, WaylandGlobal{Name: output.globalName, Interface: waylandInterfaceOutput, Version: 4})
	}
	sort.Slice(globals, func(i, j int) bool { return globals[i].Name < globals[j].Name })
	for _, global := range globals {
		payload := append(fakeWaylandU32(global.Name), fakeWaylandString(global.Interface)...)
		payload = append(payload, fakeWaylandU32(global.Version)...)
		writeFakeWaylandMessage(conn, registryID, 0, payload)
	}
}

func (r *fakeWaylandProtocolResponder) sendBoundState(conn net.Conn, boundByGlobal map[uint32]uint32, outputGlobalByObject map[uint32]uint32, xdgOutputByOutputObject map[uint32]uint32) {
	if seatID := boundByGlobal[3]; seatID != 0 {
		writeFakeWaylandMessage(conn, seatID, 0, fakeWaylandU32(r.seatCapabilities))
		writeFakeWaylandMessage(conn, seatID, 1, fakeWaylandString(r.seatName))
	}

	outputByGlobal := map[uint32]fakeWaylandProtocolOutput{}
	for _, output := range r.outputs {
		outputByGlobal[output.globalName] = output
		if outputID := boundByGlobal[output.globalName]; outputID != 0 {
			r.sendWLOutput(conn, outputID, output)
		}
	}
	for outputID, xdgOutputID := range xdgOutputByOutputObject {
		if xdgOutputID == 0 {
			continue
		}
		output, ok := outputByGlobal[outputGlobalByObject[outputID]]
		if !ok {
			continue
		}
		r.sendXDGOutput(conn, xdgOutputID, output)
	}
}

func (r *fakeWaylandProtocolResponder) sendWLOutput(conn net.Conn, objectID uint32, output fakeWaylandProtocolOutput) {
	geometry := append(fakeWaylandI32(output.x), fakeWaylandI32(output.y)...)
	geometry = append(geometry, fakeWaylandI32(0)...)
	geometry = append(geometry, fakeWaylandI32(0)...)
	geometry = append(geometry, fakeWaylandI32(0)...)
	geometry = append(geometry, fakeWaylandString("fake")...)
	geometry = append(geometry, fakeWaylandString("monitor")...)
	geometry = append(geometry, fakeWaylandI32(0)...)
	writeFakeWaylandMessage(conn, objectID, 0, geometry)

	mode := append(fakeWaylandU32(waylandOutputModeCurrent), fakeWaylandI32(output.physicalWidth)...)
	mode = append(mode, fakeWaylandI32(output.physicalHeight)...)
	mode = append(mode, fakeWaylandI32(60000)...)
	writeFakeWaylandMessage(conn, objectID, 1, mode)
	writeFakeWaylandMessage(conn, objectID, 3, fakeWaylandI32(output.scale))
	writeFakeWaylandMessage(conn, objectID, 4, fakeWaylandString(output.wlName))
}

func (r *fakeWaylandProtocolResponder) sendXDGOutput(conn net.Conn, objectID uint32, output fakeWaylandProtocolOutput) {
	writeFakeWaylandMessage(conn, objectID, 0, append(fakeWaylandI32(output.logicalX), fakeWaylandI32(output.logicalY)...))
	writeFakeWaylandMessage(conn, objectID, 1, append(fakeWaylandI32(output.logicalWidth), fakeWaylandI32(output.logicalHeight)...))
	writeFakeWaylandMessage(conn, objectID, 3, fakeWaylandString(output.xdgName))
}

type fakeWaylandBind struct {
	globalName uint32
	iface      string
	version    uint32
	objectID   uint32
}

func parseFakeWaylandBind(payload []byte) fakeWaylandBind {
	globalName := fakeWaylandUint32(payload[0:4])
	iface, next := parseFakeWaylandString(payload[4:])
	version := fakeWaylandUint32(payload[4+next : 8+next])
	objectID := fakeWaylandUint32(payload[8+next : 12+next])
	return fakeWaylandBind{globalName: globalName, iface: iface, version: version, objectID: objectID}
}

func readFakeWaylandRequest(r io.Reader) (fakeWaylandRequest, error) {
	var header [8]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return fakeWaylandRequest{}, err
	}
	sender := fakeWaylandUint32(header[0:4])
	opcodeAndSize := fakeWaylandUint32(header[4:8])
	opcode := opcodeAndSize & 0xffff
	size := int(opcodeAndSize >> 16)
	payload := make([]byte, size-8)
	if _, err := io.ReadFull(r, payload); err != nil {
		return fakeWaylandRequest{}, err
	}
	return fakeWaylandRequest{sender: sender, opcode: opcode, payload: payload}, nil
}

func writeFakeWaylandMessage(w io.Writer, senderID uint32, opcode uint32, payload []byte) {
	size := 8 + len(payload)
	var header [8]byte
	binary.LittleEndian.PutUint32(header[0:4], senderID)
	binary.LittleEndian.PutUint32(header[4:8], uint32(size<<16)|opcode)
	_, _ = w.Write(header[:])
	_, _ = w.Write(payload)
}

func fakeWaylandU32(value uint32) []byte {
	var out [4]byte
	binary.LittleEndian.PutUint32(out[:], value)
	return out[:]
}

func fakeWaylandI32(value int32) []byte {
	return fakeWaylandU32(uint32(value))
}

func fakeWaylandString(value string) []byte {
	length := len(value) + 1
	padded := fakeWaylandPaddedLen(length)
	out := make([]byte, 4+padded)
	binary.LittleEndian.PutUint32(out[0:4], uint32(length))
	copy(out[4:], value)
	return out
}

func parseFakeWaylandString(payload []byte) (string, int) {
	length := int(fakeWaylandUint32(payload[0:4]))
	raw := payload[4 : 4+length]
	if idx := bytes.IndexByte(raw, 0); idx >= 0 {
		raw = raw[:idx]
	}
	return string(raw), 4 + fakeWaylandPaddedLen(length)
}

func fakeWaylandUint32(payload []byte) uint32 {
	return binary.LittleEndian.Uint32(payload)
}

func fakeWaylandPaddedLen(length int) int {
	if length&0x3 != 0 {
		return length + (4 - (length & 0x3))
	}
	return length
}

func describeFakeWaylandRequest(request fakeWaylandRequest, xdgOutputManagerID uint32) string {
	if request.sender == 1 && request.opcode == 1 {
		return "wl_display.get_registry"
	}
	if request.sender == 1 && request.opcode == 0 {
		return "wl_display.sync"
	}
	if xdgOutputManagerID != 0 && request.sender == xdgOutputManagerID && request.opcode == 1 {
		return "zxdg_output_manager_v1.get_xdg_output"
	}
	if request.opcode == 0 && len(request.payload) >= 12 {
		bind := parseFakeWaylandBind(request.payload)
		return fmt.Sprintf("wl_registry.bind:%s", bind.iface)
	}
	return fmt.Sprintf("sender=%d opcode=%d", request.sender, request.opcode)
}

func fakeWaylandRequestsContain(requests []string, want string) bool {
	for _, request := range requests {
		if strings.Contains(request, want) {
			return true
		}
	}
	return false
}
