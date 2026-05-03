package main

import (
	"context"
	"log/slog"
)

func runDaemon(ctx context.Context, logger *slog.Logger, getenv getenvFunc) int {
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

	<-ctx.Done()

	logger.Info("daemon stopping", "reason", ctx.Err())
	return 0
}
