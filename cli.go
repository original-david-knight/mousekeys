package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
)

type getenvFunc func(string) string

func run(ctx context.Context, args []string, stdout, stderr io.Writer, getenv getenvFunc) int {
	logger := newLogger(stderr, getenv)

	if len(args) == 0 {
		fmt.Fprintln(stderr, "mousekeys: missing command")
		printRootUsage(stderr)
		return 2
	}

	switch args[0] {
	case "-h", "--help", "help":
		printRootUsage(stdout)
		return 0
	case "daemon":
		return runDaemonCommand(ctx, args[1:], stderr, logger, getenv)
	case "show":
		return runClientCommand(args[0], args[1:], stderr, logger)
	case "hide":
		return runClientCommand(args[0], args[1:], stderr, logger)
	case "status":
		return runStatusCommand(args[1:], stdout, stderr, logger)
	default:
		fmt.Fprintf(stderr, "mousekeys: unknown command %q\n", args[0])
		printRootUsage(stderr)
		return 2
	}
}

func printRootUsage(w io.Writer) {
	fmt.Fprint(w, `Usage:
  mousekeys <command> [options]

Commands:
  daemon   Run the persistent Hyprland/Wayland daemon.
  show     Request the daemon to show or toggle the grid. IPC wiring is pending.
  hide     Request the daemon to hide the grid. IPC wiring is pending.
  status   Print local binary status and build metadata.

Environment:
  MOUSEKEYS_LOG=debug              Enable debug structured logs on stderr.
  MOUSEKEYS_TRACE_JSONL=<path>     Append JSONL trace events for tests/smoke checks.
`)
}

func runDaemonCommand(ctx context.Context, args []string, stderr io.Writer, logger *slog.Logger, getenv getenvFunc) int {
	fs := flag.NewFlagSet("mousekeys daemon", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `Usage:
  mousekeys daemon

Runs the persistent daemon. It must start inside a Hyprland Wayland session with
XDG_RUNTIME_DIR, WAYLAND_DISPLAY, and HYPRLAND_INSTANCE_SIGNATURE set.
`)
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "mousekeys daemon: unexpected argument %q\n", fs.Arg(0))
		fs.Usage()
		return 2
	}

	return runDaemon(ctx, logger, getenv)
}

func runClientCommand(command string, args []string, stderr io.Writer, logger *slog.Logger) int {
	fs := flag.NewFlagSet("mousekeys "+command, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage:\n  mousekeys %s\n\nIPC wiring for this command is scheduled in a later task.\n", command)
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "mousekeys %s: unexpected argument %q\n", command, fs.Arg(0))
		fs.Usage()
		return 2
	}

	logger.Debug("client command accepted", "command", command, "ipc", "not_implemented")
	return 0
}

type statusOutput struct {
	Command    string    `json:"command"`
	IPC        string    `json:"ipc"`
	Build      buildInfo `json:"build"`
	Executable string    `json:"executable,omitempty"`
}

func runStatusCommand(args []string, stdout, stderr io.Writer, logger *slog.Logger) int {
	fs := flag.NewFlagSet("mousekeys status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `Usage:
  mousekeys status

Prints this binary's build metadata. Daemon status will be added with IPC.
`)
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "mousekeys status: unexpected argument %q\n", fs.Arg(0))
		fs.Usage()
		return 2
	}

	logger.Debug("status requested", "ipc", "not_implemented")

	out := statusOutput{
		Command:    "status",
		IPC:        "not_implemented",
		Build:      currentBuildInfo(),
		Executable: executablePath(),
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		logger.Error("status encode failed", "error", err)
		return 1
	}

	return 0
}
