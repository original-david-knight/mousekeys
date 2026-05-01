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
  show      TODO: ask the daemon to show or toggle the overlay
  hide      TODO: ask the daemon to hide the overlay
  status    TODO: ask the daemon for current state

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
		return runTODOCommand(command, commandArgs, log)
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("daemon starting", map[string]string{
		"xdg_runtime_dir":             os.Getenv("XDG_RUNTIME_DIR"),
		"wayland_display":             os.Getenv("WAYLAND_DISPLAY"),
		"hyprland_instance_signature": os.Getenv("HYPRLAND_INSTANCE_SIGNATURE"),
	})
	log.Debug("daemon entering stub loop", nil)

	return runDaemonLoop(ctx, log)
}

func runTODOCommand(command string, args []string, log *logger) error {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: mousekeys %s\n\nTODO: IPC wiring lands in ipc-socket-and-toggle.\n", command)
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

	log.Info("TODO: IPC wiring lands in ipc-socket-and-toggle", map[string]string{"command": command})
	log.Debug("short-lived command completed", map[string]string{"command": command})
	return nil
}

func runDaemonLoop(ctx context.Context, log *logger) error {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				log.Info("daemon stopping", nil)
				return nil
			}
			return ctx.Err()
		case <-ticker.C:
			log.Debug("daemon stub loop tick", nil)
		}
	}
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
