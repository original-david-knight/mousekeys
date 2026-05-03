package main

import (
	"fmt"
	"strings"

	"mousekeys/internal/wayland/wlr"

	"github.com/rajveermalviya/go-wayland/wayland/client"
)

func waylandStringPayloadLen(value string) int {
	return client.PaddedLen(len(value) + 1)
}

func putWaylandString(dst []byte, value string) (int, error) {
	if strings.ContainsRune(value, '\x00') {
		return 0, fmt.Errorf("Wayland string contains embedded NUL")
	}
	payloadLen := waylandStringPayloadLen(value)
	if len(dst) < 4+payloadLen {
		return 0, fmt.Errorf("Wayland string buffer too small: got %d want %d", len(dst), 4+payloadLen)
	}
	client.PutUint32(dst[:4], uint32(len(value)+1))
	copy(dst[4:], value)
	for i := 4 + len(value); i < 4+payloadLen; i++ {
		dst[i] = 0
	}
	return 4 + payloadLen, nil
}

func bindWaylandRegistryGlobal(registry *client.Registry, name uint32, iface string, version uint32, id client.Proxy) error {
	if registry == nil {
		return fmt.Errorf("Wayland registry is nil")
	}
	if id == nil {
		return fmt.Errorf("Wayland registry bind target is nil")
	}
	const opcode = 0
	ifaceLen := waylandStringPayloadLen(iface)
	reqBufLen := 8 + 4 + (4 + ifaceLen) + 4 + 4
	reqBuf := make([]byte, reqBufLen)
	l := 0
	client.PutUint32(reqBuf[l:l+4], registry.ID())
	l += 4
	client.PutUint32(reqBuf[l:l+4], uint32(reqBufLen<<16|opcode&0x0000ffff))
	l += 4
	client.PutUint32(reqBuf[l:l+4], name)
	l += 4
	n, err := putWaylandString(reqBuf[l:l+(4+ifaceLen)], iface)
	if err != nil {
		return err
	}
	l += n
	client.PutUint32(reqBuf[l:l+4], version)
	l += 4
	client.PutUint32(reqBuf[l:l+4], id.ID())
	l += 4
	return registry.Context().WriteMsg(reqBuf, nil)
}

func getWLRLayerSurface(layerShell *wlr.LayerShell, surface *client.Surface, output *client.Output, layer uint32, namespace string) (*wlr.LayerSurface, error) {
	if layerShell == nil {
		return nil, fmt.Errorf("wlr layer-shell object is nil")
	}
	if surface == nil {
		return nil, fmt.Errorf("wl_surface is nil")
	}
	const opcode = 0
	id := wlr.NewLayerSurface(layerShell.Context())
	namespaceLen := waylandStringPayloadLen(namespace)
	reqBufLen := 8 + 4 + 4 + 4 + 4 + (4 + namespaceLen)
	reqBuf := make([]byte, reqBufLen)
	l := 0
	client.PutUint32(reqBuf[l:l+4], layerShell.ID())
	l += 4
	client.PutUint32(reqBuf[l:l+4], uint32(reqBufLen<<16|opcode&0x0000ffff))
	l += 4
	client.PutUint32(reqBuf[l:l+4], id.ID())
	l += 4
	client.PutUint32(reqBuf[l:l+4], surface.ID())
	l += 4
	if output == nil {
		client.PutUint32(reqBuf[l:l+4], 0)
	} else {
		client.PutUint32(reqBuf[l:l+4], output.ID())
	}
	l += 4
	client.PutUint32(reqBuf[l:l+4], layer)
	l += 4
	n, err := putWaylandString(reqBuf[l:l+(4+namespaceLen)], namespace)
	if err != nil {
		return nil, err
	}
	l += n
	return id, layerShell.Context().WriteMsg(reqBuf, nil)
}
