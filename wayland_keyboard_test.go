package main

import (
	"io"
	"os"
	"syscall"
	"testing"
)

func TestReadWaylandKeymapReadsFromStartOfFD(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "keymap-*")
	if err != nil {
		t.Fatalf("create temp keymap: %v", err)
	}
	if _, err := file.WriteString("test-keymap"); err != nil {
		t.Fatalf("write temp keymap: %v", err)
	}
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		t.Fatalf("seek temp keymap: %v", err)
	}
	dupFD, err := syscall.Dup(int(file.Fd()))
	if err != nil {
		t.Fatalf("dup temp keymap fd: %v", err)
	}

	keymap, err := readWaylandKeymap(dupFD, uint32(len("test-keymap")))
	if err != nil {
		t.Fatalf("readWaylandKeymap returned error: %v", err)
	}
	if string(keymap) != "test-keymap" {
		t.Fatalf("keymap = %q, want test-keymap", string(keymap))
	}
}

func TestWaylandKeyboardRawEventSourceStopSessionAllowsLaterStart(t *testing.T) {
	events := make(chan RawKeyboardEvent)
	done := make(chan struct{})
	source := &WaylandKeyboardRawEventSource{
		events:  events,
		done:    done,
		started: true,
	}

	if err := source.stopSession(events, done, nil); err != nil {
		t.Fatalf("stopSession returned error: %v", err)
	}
	if source.started {
		t.Fatalf("source stayed started after stopSession")
	}
	if source.closed {
		t.Fatalf("stopSession permanently closed source")
	}
	if source.events != nil || source.done != nil {
		t.Fatalf("source session channels were not cleared")
	}
	assertDoneClosed(t, done)
	assertRawEventsClosed(t, events)
}

func TestWaylandKeyboardRawEventSourceStopSessionIgnoresStaleSession(t *testing.T) {
	staleEvents := make(chan RawKeyboardEvent)
	staleDone := make(chan struct{})
	activeEvents := make(chan RawKeyboardEvent)
	activeDone := make(chan struct{})
	source := &WaylandKeyboardRawEventSource{
		events:  activeEvents,
		done:    activeDone,
		started: true,
	}

	if err := source.stopSession(staleEvents, staleDone, nil); err != nil {
		t.Fatalf("stopSession returned error: %v", err)
	}
	if !source.started || source.events != activeEvents || source.done != activeDone {
		t.Fatalf("stale stopSession changed active session")
	}
	assertDoneOpen(t, staleDone)
	assertRawEventsOpen(t, staleEvents)
	assertDoneOpen(t, activeDone)
	assertRawEventsOpen(t, activeEvents)
}

func TestWaylandKeyboardRawEventSourceEmitIgnoresStaleSession(t *testing.T) {
	staleEvents := make(chan RawKeyboardEvent, 1)
	staleDone := make(chan struct{})
	activeEvents := make(chan RawKeyboardEvent, 1)
	activeDone := make(chan struct{})
	source := &WaylandKeyboardRawEventSource{
		events: activeEvents,
		done:   activeDone,
	}

	source.emit(staleEvents, staleDone, RawKeyboardEvent{Kind: RawKeyboardEventKey})
	assertRawEventsOpen(t, staleEvents)

	source.emit(activeEvents, activeDone, RawKeyboardEvent{Kind: RawKeyboardEventKey})
	select {
	case event := <-activeEvents:
		if event.Kind != RawKeyboardEventKey {
			t.Fatalf("event kind = %q, want %q", event.Kind, RawKeyboardEventKey)
		}
	default:
		t.Fatalf("active session did not receive event")
	}
}

func assertDoneClosed(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	default:
		t.Fatalf("done channel is open, want closed")
	}
}

func assertDoneOpen(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
		t.Fatalf("done channel is closed, want open")
	default:
	}
}

func assertRawEventsClosed(t *testing.T, ch <-chan RawKeyboardEvent) {
	t.Helper()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatalf("raw event channel yielded event, want closed")
		}
	default:
		t.Fatalf("raw event channel is open, want closed")
	}
}

func assertRawEventsOpen(t *testing.T, ch <-chan RawKeyboardEvent) {
	t.Helper()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatalf("raw event channel unexpectedly yielded event")
		}
		t.Fatalf("raw event channel is closed, want open")
	default:
	}
}
