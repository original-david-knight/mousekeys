package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const usageText = `Usage:
  mousekeys <command> [options]

Commands:
  daemon    Run the long-lived Mouse Keys daemon
  show      Ask the daemon to show or toggle the overlay
  hide      Ask the daemon to hide the overlay
  status    Ask the daemon for current state

Options:
  -h, --help    Show this help text
`

var requiredDaemonEnv = []string{
	"XDG_RUNTIME_DIR",
	"WAYLAND_DISPLAY",
	"HYPRLAND_INSTANCE_SIGNATURE",
}

func main() {
	log := newLoggerFromEnv()
	log.Debug("debug logging enabled", nil)

	if err := run(os.Args[1:], log); err != nil {
		log.Error(err.Error(), nil)
		os.Exit(1)
	}
}

func run(args []string, log *logger) error {
	if len(args) == 0 || isHelp(args[0]) {
		fmt.Fprint(os.Stdout, usageText)
		return nil
	}

	command, commandArgs := args[0], args[1:]
	switch command {
	case "daemon":
		return runDaemonCommand(commandArgs, log)
	case "show", "hide", "status":
		return runClientCommand(command, commandArgs, log)
	default:
		return fmt.Errorf("unknown command %q\n\n%s", command, usageText)
	}
}

func runDaemonCommand(args []string, log *logger) error {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), "Usage: mousekeys daemon\n\nRun the long-lived Mouse Keys daemon.\n")
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("daemon: unexpected argument %q", fs.Arg(0))
	}

	if missing := missingDaemonEnv(); len(missing) > 0 {
		return fmt.Errorf("daemon requires Hyprland Wayland session environment; missing %s", strings.Join(missing, ", "))
	}

	configPath, err := ConfigPath()
	if err != nil {
		return err
	}
	config, err := LoadConfigFile(configPath)
	if err != nil {
		return err
	}
	fontAtlas, err := NewFontAtlasFromConfig(config)
	if err != nil {
		return err
	}

	trace, traceCloser, err := newTraceRecorderFromEnv(systemClock{})
	if err != nil {
		return err
	}
	defer traceCloser.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("daemon starting", map[string]string{
		"config_path":                 configPath,
		"xdg_runtime_dir":             os.Getenv("XDG_RUNTIME_DIR"),
		"wayland_display":             os.Getenv("WAYLAND_DISPLAY"),
		"hyprland_instance_signature": os.Getenv("HYPRLAND_INSTANCE_SIGNATURE"),
		"label_font_size":             fmt.Sprintf("%d", fontAtlas.LabelFontSize()),
		"hud_font_size":               fmt.Sprintf("%d", fontAtlas.HUDFontSize()),
	})
	trace.Record("state", "daemon_starting", map[string]any{
		"xdg_runtime_dir":             os.Getenv("XDG_RUNTIME_DIR"),
		"wayland_display":             os.Getenv("WAYLAND_DISPLAY"),
		"hyprland_instance_signature": os.Getenv("HYPRLAND_INSTANCE_SIGNATURE"),
		"label_font_size":             fontAtlas.LabelFontSize(),
		"hud_font_size":               fontAtlas.HUDFontSize(),
		"font_glyph_count":            fontAtlas.GlyphCount(FontRoleLabel),
	})
	log.Debug("daemon entering IPC loop", nil)

	return runDaemonLoopWithTrace(ctx, log, trace, fontAtlas)
}

func runClientCommand(command string, args []string, log *logger) error {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: mousekeys %s\n\nSend %q to the running Mouse Keys daemon.\n", command, command)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("%s: unexpected argument %q", command, fs.Arg(0))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	response, err := SendIPCCommand(ctx, command)
	if err != nil {
		return err
	}
	if err := EncodeIPCResponse(os.Stdout, response); err != nil {
		return err
	}

	log.Debug("short-lived IPC command completed", map[string]string{
		"command": command,
		"state":   response.State,
	})
	return nil
}

func runDaemonLoopWithTrace(ctx context.Context, log *logger, trace TraceRecorder, fontAtlas *FontAtlas) error {
	wayland, err := OpenWaylandClientFromEnv(ctx)
	if err != nil {
		return err
	}
	defer wayland.Close()

	return runDaemonLoop(ctx, log, trace, NewHyprlandBackedWaylandDaemonController(trace, fontAtlas, wayland))
}

func runDaemonLoop(ctx context.Context, log *logger, trace TraceRecorder, controller *DaemonController) error {
	if trace == nil {
		trace = noopTraceRecorder{}
	}
	server, err := NewIPCServerFromEnv(controller, log, trace)
	if err != nil {
		return err
	}

	trace.Record("state", "daemon_loop_started", nil)
	log.Info("daemon IPC socket listening", map[string]string{"socket_path": server.SocketPath()})

	err = server.Serve(ctx)
	if closeErr := server.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	log.Info("daemon stopping", nil)
	trace.Record("state", "daemon_stopping", nil)
	return nil
}

func missingDaemonEnv() []string {
	var missing []string
	for _, key := range requiredDaemonEnv {
		if os.Getenv(key) == "" {
			missing = append(missing, key)
		}
	}
	return missing
}

func isHelp(arg string) bool {
	return arg == "-h" || arg == "--help" || arg == "help"
}
