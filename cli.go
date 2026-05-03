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
		return runClientCommand(ctx, args[0], args[1:], stderr, logger, getenv)
	case "hide":
		return runClientCommand(ctx, args[0], args[1:], stderr, logger, getenv)
	case "status":
		return runStatusCommand(ctx, args[1:], stdout, stderr, logger, getenv)
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
  show     Request the daemon to show the grid, or toggle it off if active.
  hide     Request the daemon to hide the grid if active.
  status   Print daemon state and build metadata.

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

func runClientCommand(ctx context.Context, command string, args []string, stderr io.Writer, logger *slog.Logger, getenv getenvFunc) int {
	fs := flag.NewFlagSet("mousekeys "+command, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage:\n  mousekeys %s\n\nSends an IPC command to the running mousekeys daemon.\n", command)
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

	response, err := sendIPCCommand(ctx, getenv, command)
	if err != nil {
		fmt.Fprintf(stderr, "mousekeys %s: %v\n", command, err)
		return 1
	}

	logger.Debug("client command completed", "command", command, "active", response.Active, "action", response.Action)
	return 0
}

type statusOutput struct {
	Command    string          `json:"command"`
	IPC        string          `json:"ipc"`
	Active     bool            `json:"active"`
	State      string          `json:"state"`
	PID        int             `json:"pid"`
	BuildID    string          `json:"build_id"`
	Build      buildInfo       `json:"build"`
	Executable string          `json:"executable,omitempty"`
	Socket     string          `json:"socket,omitempty"`
	RuntimeDir string          `json:"runtime_dir,omitempty"`
	Binary     binaryMetadata  `json:"binary"`
	Service    serviceMetadata `json:"service"`
	Client     *binaryMetadata `json:"client,omitempty"`
}

func runStatusCommand(ctx context.Context, args []string, stdout, stderr io.Writer, logger *slog.Logger, getenv getenvFunc) int {
	fs := flag.NewFlagSet("mousekeys status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `Usage:
  mousekeys status

Prints structured daemon state from the running mousekeys daemon.
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

	response, err := sendIPCCommand(ctx, getenv, "status")
	if err != nil {
		fmt.Fprintf(stderr, "mousekeys status: %v\n", err)
		return 1
	}
	if response.Status == nil {
		fmt.Fprintln(stderr, "mousekeys status: daemon returned no status payload")
		return 1
	}

	out := *response.Status
	client := currentBinaryMetadata()
	out.Client = &client
	logger.Debug("status requested", "active", out.Active, "pid", out.PID, "socket", out.Socket)

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		logger.Error("status encode failed", "error", err)
		return 1
	}

	return 0
}
