package main

import (
	"context"
	"log/slog"
	"time"
)

func runDaemon(ctx context.Context, logger *slog.Logger, getenv getenvFunc) int {
	return runDaemonWithOptions(ctx, logger, getenv, daemonOptions{})
}

type daemonOptions struct {
	Overlay overlayDriver
}

func runDaemonWithOptions(ctx context.Context, logger *slog.Logger, getenv getenvFunc, opts daemonOptions) int {
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

	loadedConfig, err := LoadConfig(getenv)
	if err != nil {
		logger.Error("daemon startup failed", "error", err)
		return 1
	}
	socketPath, err := ipcSocketPathFromRuntimeDir(session.XDGRuntimeDir)
	if err != nil {
		logger.Error("daemon startup failed", "error", err)
		return 1
	}
	socketOwner, err := listenIPCSocket(socketPath)
	if err != nil {
		logger.Error("daemon startup failed", "error", err)
		return 1
	}
	defer func() {
		if err := socketOwner.Close(); err != nil {
			logger.Error("IPC socket cleanup failed", "error", err)
		}
	}()

	controller := newDaemonController(opts.Overlay, newDaemonStatusBase(session, socketPath, getenv))
	serverCtx, stopServer := context.WithCancel(ctx)
	defer stopServer()
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- newIPCServer(socketOwner.listener, controller, logger).Serve(serverCtx)
	}()

	build := currentBuildInfo()
	logger.Info(
		"daemon starting",
		"version", build.Version,
		"commit", build.Commit,
		"build_date", build.BuildDate,
		"go_version", build.GoVersion,
		"ipc_socket", socketPath,
	)
	logger.Debug("debug logging enabled")
	logger.Info(
		"hyprland session environment validated",
		"xdg_runtime_dir", session.XDGRuntimeDir,
		"wayland_display", session.WaylandDisplay,
		"hyprland_instance_signature", session.HyprlandInstanceSignature,
	)
	logger.Info(
		"configuration loaded",
		"path", loadedConfig.Path,
		"created", loadedConfig.Created,
		"grid_size", loadedConfig.Config.Grid.Size,
		"subgrid_pixel_size", loadedConfig.Config.Grid.SubgridPixelSize,
		"double_click_timeout", loadedConfig.Config.DoubleClickTimeout(),
	)
	trace.Record(traceDaemonStart, map[string]any{
		"version":                      build.Version,
		"commit":                       build.Commit,
		"xdg_runtime_dir":              session.XDGRuntimeDir,
		"wayland_display":              session.WaylandDisplay,
		"hyprland_instance_signature":  session.HyprlandInstanceSignature,
		"trace_enabled_by_environment": trace.Enabled(),
		"config_path":                  loadedConfig.Path,
		"config_created":               loadedConfig.Created,
		"ipc_socket":                   socketPath,
	})

	select {
	case <-ctx.Done():
		stopServer()
		if err := socketOwner.Close(); err != nil {
			logger.Error("IPC socket cleanup failed", "error", err)
		}
		if err := <-serverErr; err != nil {
			logger.Error("IPC server shutdown failed", "error", err)
		}

		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		if err := controller.Shutdown(shutdownCtx); err != nil {
			logger.Error("overlay shutdown failed", "error", err)
		}
		cancel()

		logger.Info("daemon stopping", "reason", ctx.Err())
		trace.Record(traceDaemonStop, map[string]any{"reason": ctx.Err().Error(), "ipc_socket": socketPath})
		return 0
	case err := <-serverErr:
		if err == nil && ctx.Err() != nil {
			logger.Info("daemon stopping", "reason", ctx.Err())
			trace.Record(traceDaemonStop, map[string]any{"reason": ctx.Err().Error(), "ipc_socket": socketPath})
			return 0
		}
		if err == nil {
			err = context.Canceled
		}
		logger.Error("IPC server failed", "error", err)
		return 1
	}
}
