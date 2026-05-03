package main

import (
	"context"
	"log/slog"
)

func runDaemon(ctx context.Context, logger *slog.Logger, getenv getenvFunc) int {
	trace, err := newTraceRecorderFromEnv(getenv)
	if err != nil {
		logger.Error("daemon startup failed", "error", err)
		return 1
	}
	defer func() {
		if err := trace.Close(); err != nil {
			logger.Error("trace recorder close failed", "error", err)
		}
	}()

	session, err := validateSessionEnv(getenv)
	if err != nil {
		logger.Error("daemon startup failed", "error", err)
		return 1
	}

	build := currentBuildInfo()
	logger.Info(
		"daemon starting",
		"version", build.Version,
		"commit", build.Commit,
		"build_date", build.BuildDate,
		"go_version", build.GoVersion,
	)
	logger.Debug("debug logging enabled")
	logger.Info(
		"hyprland session environment validated",
		"xdg_runtime_dir", session.XDGRuntimeDir,
		"wayland_display", session.WaylandDisplay,
		"hyprland_instance_signature", session.HyprlandInstanceSignature,
	)
	trace.Record(traceDaemonStart, map[string]any{
		"version":                      build.Version,
		"commit":                       build.Commit,
		"xdg_runtime_dir":              session.XDGRuntimeDir,
		"wayland_display":              session.WaylandDisplay,
		"hyprland_instance_signature":  session.HyprlandInstanceSignature,
		"trace_enabled_by_environment": trace.Enabled(),
	})

	<-ctx.Done()

	logger.Info("daemon stopping", "reason", ctx.Err())
	trace.Record(traceDaemonStop, map[string]any{"reason": ctx.Err().Error()})
	return 0
}
