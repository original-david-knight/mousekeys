package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	IPCSocketName = "mousekeys.sock"
	Version       = "dev"
)

type IPCRequest struct {
	Command string `json:"command"`
}

type IPCResponse struct {
	OK      bool   `json:"ok"`
	Command string `json:"command"`
	State   string `json:"state"`
	Status  string `json:"status"`
	Active  bool   `json:"active"`
	PID     int    `json:"pid"`
	Version string `json:"version"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

type IPCServer struct {
	path       string
	listener   net.Listener
	controller *DaemonController
	log        *logger
	trace      TraceRecorder

	commandMu sync.Mutex
	closeOnce sync.Once
	closeErr  error
}

func RuntimeSocketPath() (string, error) {
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		return "", fmt.Errorf("XDG_RUNTIME_DIR is required for Mouse Keys IPC")
	}
	if !filepath.IsAbs(runtimeDir) {
		return "", fmt.Errorf("XDG_RUNTIME_DIR must be an absolute path, got %q", runtimeDir)
	}
	return filepath.Join(runtimeDir, IPCSocketName), nil
}

func NewIPCServerFromEnv(controller *DaemonController, log *logger, trace TraceRecorder) (*IPCServer, error) {
	path, err := RuntimeSocketPath()
	if err != nil {
		return nil, err
	}
	return NewIPCServer(path, controller, log, trace)
}

func NewIPCServer(path string, controller *DaemonController, log *logger, trace TraceRecorder) (*IPCServer, error) {
	if controller == nil {
		return nil, fmt.Errorf("daemon controller is required")
	}
	if log == nil {
		log = newLoggerFromEnv()
	}
	if trace == nil {
		trace = noopTraceRecorder{}
	}

	listener, err := listenUnixSocket(path, log)
	if err != nil {
		return nil, err
	}

	return &IPCServer{
		path:       path,
		listener:   listener,
		controller: controller,
		log:        log,
		trace:      trace,
	}, nil
}

func (s *IPCServer) SocketPath() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *IPCServer) Serve(ctx context.Context) error {
	if s == nil {
		return fmt.Errorf("IPC server is nil")
	}

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			if err := s.Close(); err != nil {
				s.log.Error("failed to close IPC socket after context cancellation", map[string]string{
					"socket_path": s.path,
					"error":       err.Error(),
				})
			}
		case <-done:
		}
	}()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			s.log.Error("IPC accept failed", map[string]string{
				"socket_path": s.path,
				"error":       err.Error(),
			})
			return fmt.Errorf("accept IPC connection on %q: %w", s.path, err)
		}
		go s.handleConnection(ctx, conn)
	}
}

func (s *IPCServer) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		if s.listener != nil {
			if err := s.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				s.closeErr = fmt.Errorf("close IPC listener %q: %w", s.path, err)
			}
		}
		if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) && s.closeErr == nil {
			s.closeErr = fmt.Errorf("remove IPC socket %q: %w", s.path, err)
		}
		if s.closeErr != nil {
			s.log.Error("IPC socket cleanup failed", map[string]string{
				"socket_path": s.path,
				"error":       s.closeErr.Error(),
			})
			return
		}
		s.log.Debug("IPC socket removed", map[string]string{"socket_path": s.path})
	})
	return s.closeErr
}

func (s *IPCServer) handleConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		s.log.Error("failed to set IPC connection deadline", map[string]string{"error": err.Error()})
	}

	request, err := readIPCRequest(conn)
	if err != nil {
		if errors.Is(err, io.EOF) {
			s.log.Debug("IPC client disconnected without request", nil)
			return
		}
		s.log.Error("failed to read IPC request", map[string]string{"error": err.Error()})
		_ = EncodeIPCResponse(conn, s.statusResponse("", "", false, err.Error()))
		return
	}

	response := s.dispatch(ctx, request.Command)
	if err := EncodeIPCResponse(conn, response); err != nil {
		s.log.Error("failed to write IPC response", map[string]string{
			"command": request.Command,
			"error":   err.Error(),
		})
	}
}

func (s *IPCServer) dispatch(ctx context.Context, command string) IPCResponse {
	s.commandMu.Lock()
	defer s.commandMu.Unlock()

	command = strings.TrimSpace(command)
	if err := validateIPCCommand(command); err != nil {
		return s.statusResponse(command, "", false, err.Error())
	}

	s.trace.Record("io", "ipc_command", map[string]any{"command": command})

	var err error
	var message string
	switch command {
	case "show":
		before := s.controller.State()
		err = s.controller.Show(ctx)
		if err == nil && before == DaemonStateOverlayShown {
			message = "overlay hidden"
		} else {
			message = "overlay shown"
		}
	case "hide":
		err = s.controller.Hide(ctx)
		message = "overlay hidden"
	case "status":
		message = "daemon running"
	}
	if err != nil {
		s.log.Error("IPC command failed", map[string]string{
			"command": command,
			"error":   err.Error(),
		})
		return s.statusResponse(command, "", false, err.Error())
	}

	return s.statusResponse(command, message, true, "")
}

func (s *IPCServer) statusResponse(command string, message string, ok bool, errText string) IPCResponse {
	state := DaemonStateInactive
	if s != nil && s.controller != nil {
		state = s.controller.State()
	}
	active := state == DaemonStateOverlayShown
	status := "inactive"
	if active {
		status = "active"
	}
	return IPCResponse{
		OK:      ok,
		Command: command,
		State:   string(state),
		Status:  status,
		Active:  active,
		PID:     os.Getpid(),
		Version: Version,
		Message: message,
		Error:   errText,
	}
}

func SendIPCCommand(ctx context.Context, command string) (IPCResponse, error) {
	path, err := RuntimeSocketPath()
	if err != nil {
		return IPCResponse{}, err
	}
	return SendIPCCommandToPath(ctx, path, command)
}

func SendIPCCommandToPath(ctx context.Context, path string, command string) (IPCResponse, error) {
	if err := validateIPCCommand(command); err != nil {
		return IPCResponse{}, err
	}

	dialer := net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return IPCResponse{}, fmt.Errorf("connect to Mouse Keys daemon at %q: %w", path, err)
	}
	defer conn.Close()

	deadline := time.Now().Add(2 * time.Second)
	if contextDeadline, ok := ctx.Deadline(); ok {
		deadline = contextDeadline
	}
	_ = conn.SetDeadline(deadline)

	if err := json.NewEncoder(conn).Encode(IPCRequest{Command: command}); err != nil {
		return IPCResponse{}, fmt.Errorf("send IPC command %q: %w", command, err)
	}

	var response IPCResponse
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		return IPCResponse{}, fmt.Errorf("read IPC response for %q: %w", command, err)
	}
	if !response.OK {
		if response.Error == "" {
			response.Error = "daemon returned an unsuccessful response"
		}
		return response, fmt.Errorf("IPC command %q failed: %s", command, response.Error)
	}
	return response, nil
}

func EncodeIPCResponse(w io.Writer, response IPCResponse) error {
	return json.NewEncoder(w).Encode(response)
}

func readIPCRequest(r io.Reader) (IPCRequest, error) {
	line, err := bufio.NewReader(io.LimitReader(r, 4096)).ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) && strings.TrimSpace(line) != "" {
			err = nil
		} else {
			return IPCRequest{}, err
		}
	}

	line = strings.TrimSpace(line)
	if line == "" {
		return IPCRequest{}, io.EOF
	}
	if strings.HasPrefix(line, "{") {
		var request IPCRequest
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			return IPCRequest{}, fmt.Errorf("decode IPC JSON request: %w", err)
		}
		request.Command = strings.TrimSpace(request.Command)
		if err := validateIPCCommand(request.Command); err != nil {
			return IPCRequest{}, err
		}
		return request, nil
	}

	if err := validateIPCCommand(line); err != nil {
		return IPCRequest{}, err
	}
	return IPCRequest{Command: line}, nil
}

func listenUnixSocket(path string, log *logger) (net.Listener, error) {
	if path == "" {
		return nil, fmt.Errorf("IPC socket path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create IPC socket directory %q: %w", filepath.Dir(path), err)
	}

	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("IPC socket path %q already exists and is not a socket", path)
		}
		conn, dialErr := net.DialTimeout("unix", path, 200*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			return nil, fmt.Errorf("refusing to start: another live Mouse Keys daemon owns %q", path)
		}
		log.Info("removing stale IPC socket", map[string]string{
			"socket_path": path,
			"error":       dialErr.Error(),
		})
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove stale IPC socket %q: %w", path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("stat IPC socket %q: %w", path, err)
	}

	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on IPC socket %q: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("set IPC socket permissions on %q: %w", path, err)
	}
	return listener, nil
}

func validateIPCCommand(command string) error {
	switch command {
	case "show", "hide", "status":
		return nil
	case "":
		return fmt.Errorf("IPC command is required")
	default:
		return fmt.Errorf("unknown IPC command %q", command)
	}
}
