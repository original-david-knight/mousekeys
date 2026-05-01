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

type logger struct {
	out   io.Writer
	debug bool
	mu    sync.Mutex
}

func newLoggerFromEnv() *logger {
	return &logger{
		out:   os.Stderr,
		debug: strings.EqualFold(os.Getenv("MOUSEKEYS_LOG"), "debug"),
	}
}

func (l *logger) Info(msg string, fields map[string]string) {
	l.write("info", msg, fields)
}

func (l *logger) Debug(msg string, fields map[string]string) {
	if !l.debug {
		return
	}
	l.write("debug", msg, fields)
}

func (l *logger) Error(msg string, fields map[string]string) {
	l.write("error", msg, fields)
}

func (l *logger) write(level string, msg string, fields map[string]string) {
	event := map[string]any{
		"time":  time.Now().UTC().Format(time.RFC3339Nano),
		"level": level,
		"msg":   msg,
	}
	for k, v := range fields {
		event[k] = v
	}

	line, err := json.Marshal(event)
	if err != nil {
		line = []byte(fmt.Sprintf(`{"level":"error","msg":"failed to marshal log event","error":%q}`, err.Error()))
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintln(l.out, string(line))
}
