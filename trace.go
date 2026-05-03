package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

const traceEnvVar = "MOUSEKEYS_TRACE_JSONL"

const (
	traceDaemonStart          = "daemon.start"
	traceDaemonStop           = "daemon.stop"
	traceOverlaySurfaceCreate = "overlay.surface_create"
	traceOverlayConfigure     = "overlay.configure"
	traceOverlayRender        = "overlay.render"
	traceOverlayKeyboardGrab  = "overlay.keyboard_grab"
	traceOverlayUnmap         = "overlay.unmap"
	traceOverlayDestroy       = "overlay.destroy"
	traceOverlayOutputChange  = "overlay.output_change"
	traceOverlayClose         = "overlay.compositor_close"
	traceOverlayRelease       = "overlay.release"
	traceOverlayError         = "overlay.error"
	traceKeyboardKeymap       = "keyboard.keymap"
	traceKeyboardEnter        = "keyboard.enter"
	traceKeyboardLeave        = "keyboard.leave"
	traceKeyboardDestroy      = "keyboard.destroy"
	traceKeyboardKey          = "keyboard.key"
	traceKeyboardModifiers    = "keyboard.modifiers"
	traceKeyboardRepeat       = "keyboard.repeat"
	traceKeyboardToken        = "keyboard.token"
	tracePointerMotion        = "pointer.motion"
	tracePointerButton        = "pointer.button"
	tracePointerFrame         = "pointer.frame"
	traceTimerCreate          = "timer.create"
	traceTimerReset           = "timer.reset"
	traceTimerStop            = "timer.stop"
	traceTimerFire            = "timer.fire"
	traceClickGroupStart      = "click_group.start"
	traceClickGroupComplete   = "click_group.complete"
	traceStayActiveReset      = "stay_active.reset"
)

type TraceEvent struct {
	Seq    uint64         `json:"seq"`
	Time   time.Time      `json:"time"`
	Event  string         `json:"event"`
	Fields map[string]any `json:"fields,omitempty"`
}

type TraceRecorder struct {
	mu     sync.Mutex
	enc    *json.Encoder
	closer io.Closer
	now    func() time.Time
	seq    uint64
	err    error
}

func NewTraceRecorder(w io.Writer, now func() time.Time) *TraceRecorder {
	if w == nil {
		return &TraceRecorder{}
	}
	if now == nil {
		now = time.Now
	}
	return &TraceRecorder{
		enc: json.NewEncoder(w),
		now: now,
	}
}

func newTraceRecorderFromEnv(getenv getenvFunc) (*TraceRecorder, error) {
	path := strings.TrimSpace(getenv(traceEnvVar))
	if path == "" {
		return &TraceRecorder{}, nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open %s=%q: %w", traceEnvVar, path, err)
	}
	recorder := NewTraceRecorder(file, time.Now)
	recorder.closer = file
	return recorder, nil
}

func (r *TraceRecorder) Enabled() bool {
	return r != nil && r.enc != nil
}

func (r *TraceRecorder) Record(event string, fields map[string]any) {
	if r == nil || r.enc == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return
	}
	r.seq++
	if r.now == nil {
		r.now = time.Now
	}
	if fields == nil {
		fields = map[string]any{}
	}
	r.err = r.enc.Encode(TraceEvent{
		Seq:    r.seq,
		Time:   r.now().UTC(),
		Event:  event,
		Fields: fields,
	})
}

func (r *TraceRecorder) Err() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

func (r *TraceRecorder) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	err := r.err
	closer := r.closer
	r.closer = nil
	r.mu.Unlock()
	if closer != nil {
		if closeErr := closer.Close(); err == nil {
			err = closeErr
		}
	}
	return err
}
