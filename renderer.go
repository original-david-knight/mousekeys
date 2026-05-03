package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

type ARGBPixel uint32

func StraightARGB(a, r, g, b uint8) ARGBPixel {
	return ARGBPixel(uint32(a)<<24 | uint32(r)<<16 | uint32(g)<<8 | uint32(b))
}

func (p ARGBPixel) A() uint8 {
	return uint8(uint32(p) >> 24)
}

func (p ARGBPixel) R() uint8 {
	return uint8(uint32(p) >> 16)
}

func (p ARGBPixel) G() uint8 {
	return uint8(uint32(p) >> 8)
}

func (p ARGBPixel) B() uint8 {
	return uint8(p)
}

func PremultiplyARGBPixel(p ARGBPixel) ARGBPixel {
	a := uint32(p.A())
	r := (uint32(p.R())*a + 127) / 255
	g := (uint32(p.G())*a + 127) / 255
	b := (uint32(p.B())*a + 127) / 255
	return StraightARGB(uint8(a), uint8(r), uint8(g), uint8(b))
}

func IsPremultipliedARGB(p ARGBPixel) bool {
	a := p.A()
	return p.R() <= a && p.G() <= a && p.B() <= a
}

type ARGBSnapshot struct {
	Width  int         `json:"width"`
	Height int         `json:"height"`
	Pixels []ARGBPixel `json:"-"`
}

func NewARGBSnapshot(width, height int, pixels []ARGBPixel) (ARGBSnapshot, error) {
	if width <= 0 || height <= 0 {
		return ARGBSnapshot{}, fmt.Errorf("ARGB snapshot dimensions must be positive, got %dx%d", width, height)
	}
	if len(pixels) != width*height {
		return ARGBSnapshot{}, fmt.Errorf("ARGB snapshot has %d pixels, want %d", len(pixels), width*height)
	}
	copied := make([]ARGBPixel, len(pixels))
	copy(copied, pixels)
	return ARGBSnapshot{Width: width, Height: height, Pixels: copied}, nil
}

func (s ARGBSnapshot) PixelAt(x, y int) (ARGBPixel, bool) {
	if x < 0 || y < 0 || x >= s.Width || y >= s.Height {
		return 0, false
	}
	return s.Pixels[y*s.Width+x], true
}

func (s ARGBSnapshot) StraightHash() string {
	h := sha256.New()
	var buf [8]byte
	binary.BigEndian.PutUint32(buf[:4], uint32(s.Width))
	binary.BigEndian.PutUint32(buf[4:], uint32(s.Height))
	_, _ = h.Write(buf[:])
	for _, pixel := range s.Pixels {
		binary.BigEndian.PutUint32(buf[:4], uint32(pixel))
		_, _ = h.Write(buf[:4])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (s ARGBSnapshot) PremultipliedForWayland() []ARGBPixel {
	out := make([]ARGBPixel, len(s.Pixels))
	for i, pixel := range s.Pixels {
		out[i] = PremultiplyARGBPixel(pixel)
	}
	return out
}

func (s ARGBSnapshot) PremultipliedForWaylandBytes() []byte {
	pixels := s.PremultipliedForWayland()
	out := make([]byte, len(pixels)*4)
	for i, pixel := range pixels {
		binary.LittleEndian.PutUint32(out[i*4:], uint32(pixel))
	}
	return out
}

func AlphaOverStraightARGB(src, dst ARGBPixel) ARGBPixel {
	srcA := uint32(src.A())
	if srcA == 0 {
		return dst
	}
	dstA := uint32(dst.A())
	if srcA == 255 || dstA == 0 {
		return src
	}

	den := srcA*255 + dstA*(255-srcA)
	if den == 0 {
		return 0
	}
	outA := uint8((den + 127) / 255)
	outR := blendStraightChannel(src.R(), srcA, dst.R(), dstA, den)
	outG := blendStraightChannel(src.G(), srcA, dst.G(), dstA, den)
	outB := blendStraightChannel(src.B(), srcA, dst.B(), dstA, den)
	return StraightARGB(outA, outR, outG, outB)
}

func blendStraightChannel(srcC uint8, srcA uint32, dstC uint8, dstA uint32, den uint32) uint8 {
	num := uint32(srcC)*srcA*255 + uint32(dstC)*dstA*(255-srcA)
	return uint8((num + den/2) / den)
}

func (s ARGBSnapshot) CompositeOver(background ARGBPixel) ARGBSnapshot {
	pixels := make([]ARGBPixel, len(s.Pixels))
	for i, pixel := range s.Pixels {
		pixels[i] = AlphaOverStraightARGB(pixel, background)
	}
	return ARGBSnapshot{Width: s.Width, Height: s.Height, Pixels: pixels}
}
