package main

import "time"

type realClock struct{}

func (realClock) Now() time.Time {
	return time.Now()
}

func (realClock) NewTimer(d time.Duration) TimerHandle {
	return realTimer{timer: time.NewTimer(d)}
}

type realTimer struct {
	timer *time.Timer
}

func (t realTimer) C() <-chan time.Time {
	return t.timer.C
}

func (t realTimer) Stop() bool {
	return t.timer.Stop()
}

func (t realTimer) Reset(d time.Duration) bool {
	return t.timer.Reset(d)
}
