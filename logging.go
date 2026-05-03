package main

import (
	"io"
	"log/slog"
	"strings"
)

func newLogger(stderr io.Writer, getenv getenvFunc) *slog.Logger {
	level := slog.LevelInfo
	if strings.EqualFold(strings.TrimSpace(getenv("MOUSEKEYS_LOG")), "debug") {
		level = slog.LevelDebug
	}

	return slog.New(slog.NewJSONHandler(stderr, &slog.HandlerOptions{
		Level: level,
	}))
}
