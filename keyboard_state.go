package main

import "fmt"

type KeyboardSessionState struct {
	Entered          bool
	Destroyed        bool
	Modifiers        ModifierState
	KeymapBytes      []byte
	RepeatRate       int
	RepeatDelay      int64
	Pressed          map[string]struct{}
	HeldDirection    string
	HeldRepeatActive bool
}

func (s *KeyboardSessionState) Apply(event KeyboardEvent) error {
	_, err := s.ApplyEvent(event)
	return err
}

func (s *KeyboardSessionState) ApplyEvent(event KeyboardEvent) (KeyboardEvent, error) {
	switch event.Kind {
	case KeyboardEventKeymap:
		if event.Keymap == nil {
			return event, fmt.Errorf("keyboard keymap event missing keymap payload")
		}
		bytes, err := event.Keymap.Bytes()
		if err != nil {
			return event, err
		}
		s.KeymapBytes = bytes
		s.Modifiers = ModifierState{}
		s.clearPressedAndHeldRepeat()
	case KeyboardEventEnter:
		s.Entered = true
		s.Destroyed = false
	case KeyboardEventLeave:
		s.Entered = false
		s.Modifiers = ModifierState{}
		s.clearPressedAndHeldRepeat()
	case KeyboardEventDestroy:
		s.Reset()
		s.Destroyed = true
	case KeyboardEventModifiers:
		s.Modifiers = event.Modifiers
	case KeyboardEventRepeat:
		s.RepeatRate = event.RepeatRate
		s.RepeatDelay = int64(event.RepeatDelay)
	case KeyboardEventKey:
		if event.Modifiers != (ModifierState{}) {
			s.Modifiers = event.Modifiers
		} else {
			event.Modifiers = s.Modifiers
		}
		if event.Key == "" {
			return event, fmt.Errorf("keyboard key event missing key name")
		}
		if event.State == KeyPressed {
			if s.Pressed == nil {
				s.Pressed = make(map[string]struct{})
			}
			if _, ok := s.Pressed[event.Key]; ok {
				event.Repeated = true
			} else {
				s.Pressed[event.Key] = struct{}{}
			}
		} else if event.State == KeyReleased {
			delete(s.Pressed, event.Key)
		} else {
			return event, fmt.Errorf("keyboard key event has unknown state %q", event.State)
		}
	default:
		return event, fmt.Errorf("unknown keyboard event kind %q", event.Kind)
	}
	return event, nil
}

func (s *KeyboardSessionState) Reset() {
	s.Entered = false
	s.Modifiers = ModifierState{}
	s.KeymapBytes = nil
	s.RepeatRate = 0
	s.RepeatDelay = 0
	s.clearPressedAndHeldRepeat()
}

func (s *KeyboardSessionState) StartHeldDirectionRepeat(direction string) {
	s.HeldDirection = direction
	s.HeldRepeatActive = direction != ""
}

func (s *KeyboardSessionState) StopHeldDirectionRepeat() {
	s.HeldDirection = ""
	s.HeldRepeatActive = false
}

func (s *KeyboardSessionState) clearPressedAndHeldRepeat() {
	clear(s.Pressed)
	s.StopHeldDirectionRepeat()
}
