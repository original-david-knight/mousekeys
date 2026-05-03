package main

import "fmt"

type KeyboardSessionState struct {
	Entered     bool
	Destroyed   bool
	Modifiers   ModifierState
	KeymapBytes []byte
	RepeatRate  int
	RepeatDelay int64
}

func (s *KeyboardSessionState) Apply(event KeyboardEvent) error {
	switch event.Kind {
	case KeyboardEventKeymap:
		if event.Keymap == nil {
			return fmt.Errorf("keyboard keymap event missing keymap payload")
		}
		bytes, err := event.Keymap.Bytes()
		if err != nil {
			return err
		}
		s.KeymapBytes = bytes
	case KeyboardEventEnter:
		s.Entered = true
		s.Destroyed = false
	case KeyboardEventLeave:
		s.Entered = false
		s.Modifiers = ModifierState{}
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
		}
	default:
		return fmt.Errorf("unknown keyboard event kind %q", event.Kind)
	}
	return nil
}

func (s *KeyboardSessionState) Reset() {
	s.Entered = false
	s.Modifiers = ModifierState{}
	s.KeymapBytes = nil
	s.RepeatRate = 0
	s.RepeatDelay = 0
}
