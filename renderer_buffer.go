package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
)

type ARGBBuffer struct {
	Width  int
	Height int
	Stride int
	Pixels []uint32
}

func NewARGBBuffer(width, height int) (ARGBBuffer, error) {
	if width <= 0 || height <= 0 {
		return ARGBBuffer{}, fmt.Errorf("invalid ARGB buffer size %dx%d", width, height)
	}

	stride := width
	return ARGBBuffer{
		Width:  width,
		Height: height,
		Stride: stride,
		Pixels: make([]uint32, stride*height),
	}, nil
}

func (b ARGBBuffer) Validate() error {
	if b.Width <= 0 || b.Height <= 0 || b.Stride < b.Width {
		return fmt.Errorf("invalid ARGB buffer geometry %dx%d stride %d", b.Width, b.Height, b.Stride)
	}
	if len(b.Pixels) < b.Stride*b.Height {
		return errors.New("ARGB buffer pixel slice is shorter than stride*height")
	}
	return nil
}

func (b ARGBBuffer) Set(x, y int, argb uint32) error {
	if err := b.Validate(); err != nil {
		return err
	}
	if x < 0 || y < 0 || x >= b.Width || y >= b.Height {
		return fmt.Errorf("ARGB buffer coordinate out of bounds: %d,%d", x, y)
	}
	b.Pixels[y*b.Stride+x] = argb
	return nil
}

func ARGBSnapshot(b ARGBBuffer) ([]byte, error) {
	if err := b.Validate(); err != nil {
		return nil, err
	}

	pixelCount := b.Stride * b.Height
	out := make([]byte, 12+(pixelCount*4))
	binary.BigEndian.PutUint32(out[0:4], uint32(b.Width))
	binary.BigEndian.PutUint32(out[4:8], uint32(b.Height))
	binary.BigEndian.PutUint32(out[8:12], uint32(b.Stride))

	offset := 12
	for _, pixel := range b.Pixels[:pixelCount] {
		binary.BigEndian.PutUint32(out[offset:offset+4], pixel)
		offset += 4
	}
	return out, nil
}

func ARGBHash(b ARGBBuffer) (string, error) {
	snapshot, err := ARGBSnapshot(b)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(snapshot)
	return hex.EncodeToString(sum[:]), nil
}
