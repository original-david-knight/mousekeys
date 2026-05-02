package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	wlclient "github.com/rajveermalviya/go-wayland/wayland/client"
)

type WaylandKeyboardRawEventSource struct {
	client *WaylandClient

	mu       sync.Mutex
	sendMu   sync.Mutex
	keyboard *wlclient.Keyboard
	events   chan RawKeyboardEvent
	done     chan struct{}
	started  bool
	closed   bool

	closeOnce sync.Once
	closeErr  error
}

func NewWaylandKeyboardRawEventSource(client *WaylandClient) *WaylandKeyboardRawEventSource {
	return &WaylandKeyboardRawEventSource{client: client}
}

func (s *WaylandKeyboardRawEventSource) RawEvents(ctx context.Context) (<-chan RawKeyboardEvent, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("Wayland keyboard source is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, fmt.Errorf("Wayland keyboard source is closed")
	}
	if s.started {
		events := s.events
		s.mu.Unlock()
		return events, nil
	}
	s.started = true
	s.events = make(chan RawKeyboardEvent, 64)
	s.done = make(chan struct{})
	events := s.events
	s.mu.Unlock()

	seat := s.client.Seat()
	if seat == nil {
		s.mu.Lock()
		s.started = false
		done := s.done
		shouldCloseEvents := false
		if s.events == events {
			s.done = nil
			s.events = nil
			shouldCloseEvents = true
		}
		s.mu.Unlock()
		if done != nil {
			close(done)
		}
		if shouldCloseEvents {
			s.sendMu.Lock()
			close(events)
			s.sendMu.Unlock()
		}
		return nil, fmt.Errorf("Wayland seat is not bound")
	}

	var keyboard *wlclient.Keyboard
	if err := s.client.withProtocolLock(func() error {
		var err error
		keyboard, err = seat.GetKeyboard()
		if err != nil {
			return fmt.Errorf("request wl_keyboard from seat: %w", err)
		}
		s.installHandlers(keyboard)
		return nil
	}); err != nil {
		s.mu.Lock()
		s.started = false
		done := s.done
		shouldCloseEvents := false
		if s.events == events {
			s.done = nil
			s.events = nil
			shouldCloseEvents = true
		}
		s.mu.Unlock()
		if done != nil {
			close(done)
		}
		if shouldCloseEvents {
			s.sendMu.Lock()
			close(events)
			s.sendMu.Unlock()
		}
		return nil, err
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		if keyboard != nil {
			_ = s.client.withProtocolLock(func() error {
				return keyboard.Release()
			})
		}
		return nil, fmt.Errorf("Wayland keyboard source is closed")
	}
	s.keyboard = keyboard
	s.mu.Unlock()
	s.client.StartDispatchLoop(ctx)
	if done := ctx.Done(); done != nil {
		go func() {
			<-done
			_ = s.Close()
		}()
	}
	return events, nil
}

func (s *WaylandKeyboardRawEventSource) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		keyboard := s.keyboard
		events := s.events
		done := s.done
		s.keyboard = nil
		s.events = nil
		s.done = nil
		s.mu.Unlock()

		if done != nil {
			close(done)
		}
		if keyboard != nil && s.client != nil {
			s.closeErr = s.client.withProtocolLock(func() error {
				return keyboard.Release()
			})
		}
		if events != nil {
			s.sendMu.Lock()
			close(events)
			s.sendMu.Unlock()
		}
	})
	return s.closeErr
}

func (s *WaylandKeyboardRawEventSource) installHandlers(keyboard *wlclient.Keyboard) {
	keyboard.SetKeymapHandler(func(event wlclient.KeyboardKeymapEvent) {
		keymap, err := readWaylandKeymap(event.Fd, event.Size)
		if err != nil {
			s.emit(RawKeyboardEvent{Kind: RawKeyboardEventError, Err: err})
			return
		}
		s.emit(RawKeyboardEvent{
			Kind:         RawKeyboardEventKeymap,
			KeymapFormat: event.Format,
			Keymap:       keymap,
		})
	})
	keyboard.SetEnterHandler(func(event wlclient.KeyboardEnterEvent) {
		keys, err := parseWaylandKeyboardEnterKeys(event.Keys)
		if err != nil {
			s.emit(RawKeyboardEvent{Kind: RawKeyboardEventError, Err: err})
			return
		}
		s.emit(RawKeyboardEvent{
			Kind:        RawKeyboardEventEnter,
			PressedKeys: keys,
			Time:        time.Now(),
		})
	})
	keyboard.SetKeyHandler(func(event wlclient.KeyboardKeyEvent) {
		s.emit(RawKeyboardEvent{
			Kind:    RawKeyboardEventKey,
			Keycode: event.Key,
			Pressed: event.State == uint32(wlclient.KeyboardKeyStatePressed),
			Time:    waylandKeyboardTime(event.Time),
		})
	})
	keyboard.SetModifiersHandler(func(event wlclient.KeyboardModifiersEvent) {
		s.emit(RawKeyboardEvent{
			Kind:          RawKeyboardEventModifiers,
			ModsDepressed: event.ModsDepressed,
			ModsLatched:   event.ModsLatched,
			ModsLocked:    event.ModsLocked,
			Group:         event.Group,
		})
	})
	keyboard.SetLeaveHandler(func(wlclient.KeyboardLeaveEvent) {
		s.emit(RawKeyboardEvent{Kind: RawKeyboardEventLeave, Time: time.Now()})
	})
	keyboard.SetRepeatInfoHandler(func(event wlclient.KeyboardRepeatInfoEvent) {
		s.emit(RawKeyboardEvent{
			Kind:          RawKeyboardEventRepeatInfo,
			RepeatRate:    event.Rate,
			RepeatDelayMS: event.Delay,
		})
	})
}

func (s *WaylandKeyboardRawEventSource) emit(event RawKeyboardEvent) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	s.mu.Lock()
	events := s.events
	done := s.done
	closed := s.closed
	s.mu.Unlock()
	if events == nil || done == nil || closed {
		return
	}
	select {
	case events <- event:
	case <-done:
	}
}

func readWaylandKeymap(fd int, size uint32) ([]byte, error) {
	if fd < 0 {
		return nil, fmt.Errorf("wl_keyboard keymap fd is invalid")
	}
	file := os.NewFile(uintptr(fd), "wl-keyboard-keymap")
	if file == nil {
		return nil, fmt.Errorf("open wl_keyboard keymap fd")
	}
	defer file.Close()

	if size == 0 {
		return nil, fmt.Errorf("wl_keyboard keymap size is zero")
	}
	keymap := make([]byte, int(size))
	n, err := io.ReadFull(file, keymap)
	if err != nil {
		return nil, fmt.Errorf("read wl_keyboard keymap: %w", err)
	}
	return keymap[:n], nil
}

func parseWaylandKeyboardEnterKeys(keys []byte) ([]uint32, error) {
	if len(keys)%4 != 0 {
		return nil, fmt.Errorf("wl_keyboard enter keys array has invalid byte length %d", len(keys))
	}
	pressed := make([]uint32, 0, len(keys)/4)
	for len(keys) > 0 {
		pressed = append(pressed, wlclient.Uint32(keys[:4]))
		keys = keys[4:]
	}
	return pressed, nil
}

func waylandKeyboardTime(ms uint32) time.Time {
	return time.Unix(0, int64(ms)*int64(time.Millisecond))
}
