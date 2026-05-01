package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

type TraceRecorder interface {
	Record(kind string, action string, fields map[string]any)
}

type noopTraceRecorder struct{}

func (noopTraceRecorder) Record(string, string, map[string]any) {}

type JSONLTraceRecorder struct {
	out   io.Writer
	clock Clock
	mu    sync.Mutex
}

func NewJSONLTraceRecorder(out io.Writer, clock Clock) *JSONLTraceRecorder {
	if clock == nil {
		clock = systemClock{}
	}
	return &JSONLTraceRecorder{out: out, clock: clock}
}

func (r *JSONLTraceRecorder) Record(kind string, action string, fields map[string]any) {
	if r == nil || r.out == nil {
		return
	}

	event := map[string]any{
		"time":   r.clock.Now().UTC().Format(time.RFC3339Nano),
		"kind":   kind,
		"action": action,
	}
	if len(fields) > 0 {
		event["fields"] = fields
	}

	line, err := json.Marshal(event)
	if err != nil {
		line = []byte(fmt.Sprintf(`{"kind":"trace","action":"marshal_error","fields":{"error":%q}}`, err.Error()))
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	fmt.Fprintln(r.out, string(line))
}

func newTraceRecorderFromEnv(clock Clock) (TraceRecorder, io.Closer, error) {
	path := os.Getenv("MOUSEKEYS_TRACE_JSONL")
	if path == "" {
		return noopTraceRecorder{}, noopCloser{}, nil
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("open MOUSEKEYS_TRACE_JSONL %q: %w", path, err)
	}
	return NewJSONLTraceRecorder(file, clock), file, nil
}

type noopCloser struct{}

func (noopCloser) Close() error {
	return nil
}
