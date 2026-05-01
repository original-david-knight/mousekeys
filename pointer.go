package main

import (
	"context"
	"fmt"
)

func EmitPointerClick(ctx context.Context, pointer PointerSynthesizer, clock Clock, outputName string, p Point, button PointerButton, groupID string) error {
	if pointer == nil {
		return fmt.Errorf("pointer synthesizer is required")
	}
	if clock == nil {
		clock = systemClock{}
	}
	if groupID == "" {
		groupID = "click"
	}

	now := clock.Now()
	motion := PointerMotion{
		OutputName: outputName,
		X:          p.X,
		Y:          p.Y,
		Time:       now,
		GroupID:    groupID,
	}
	if err := pointer.Motion(ctx, motion); err != nil {
		return err
	}
	if err := pointer.Frame(ctx, PointerFrame{
		OutputName: outputName,
		X:          p.X,
		Y:          p.Y,
		Time:       now,
		GroupID:    groupID,
	}); err != nil {
		return err
	}
	if err := pointer.Button(ctx, PointerButtonEvent{
		OutputName: outputName,
		X:          p.X,
		Y:          p.Y,
		Button:     button,
		State:      ButtonDown,
		Time:       now,
		GroupID:    groupID,
	}); err != nil {
		return err
	}
	if err := pointer.Button(ctx, PointerButtonEvent{
		OutputName: outputName,
		X:          p.X,
		Y:          p.Y,
		Button:     button,
		State:      ButtonUp,
		Time:       now,
		GroupID:    groupID,
	}); err != nil {
		return err
	}
	return pointer.Frame(ctx, PointerFrame{
		OutputName: outputName,
		X:          p.X,
		Y:          p.Y,
		Time:       now,
		GroupID:    groupID,
	})
}

func EmitPointerDoubleClick(ctx context.Context, pointer PointerSynthesizer, clock Clock, outputName string, p Point, button PointerButton, groupID string) error {
	if groupID == "" {
		groupID = "double-click"
	}
	if err := EmitPointerClick(ctx, pointer, clock, outputName, p, button, groupID); err != nil {
		return err
	}
	return EmitPointerClick(ctx, pointer, clock, outputName, p, button, groupID)
}
