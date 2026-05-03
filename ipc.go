package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	ipcSocketName       = "mousekeys.sock"
	ipcClientTimeout    = 2 * time.Second
	ipcHandlerTimeout   = 5 * time.Second
	liveSocketProbeTime = 200 * time.Millisecond
)

type ipcRequest struct {
	Command string `json:"command"`
}

type ipcResponse struct {
	OK      bool          `json:"ok"`
	Command string        `json:"command"`
	Active  bool          `json:"active"`
	State   string        `json:"state"`
	Action  string        `json:"action,omitempty"`
	Status  *statusOutput `json:"status,omitempty"`
	Error   string        `json:"error,omitempty"`
}

type binaryMetadata struct {
	Executable       string   `json:"executable,omitempty"`
	Args             []string `json:"args,omitempty"`
	WorkingDirectory string   `json:"working_directory,omitempty"`
}

type serviceMetadata struct {
	UnitName       string `json:"unit_name"`
	Manager        string `json:"manager"`
	InvocationID   string `json:"invocation_id,omitempty"`
	SystemdExecPID string `json:"systemd_exec_pid,omitempty"`
	JournalStream  string `json:"journal_stream,omitempty"`
}

type overlayCancelReason string

const (
	overlayCancelShowToggle     overlayCancelReason = "show_toggle"
	overlayCancelHide           overlayCancelReason = "hide"
	overlayCancelDaemonShutdown overlayCancelReason = "daemon_shutdown"
)

type overlayDriver interface {
	ShowOverlay(ctx context.Context) error
	CancelOverlay(ctx context.Context, reason overlayCancelReason) error
}

type noopOverlayDriver struct{}

func (noopOverlayDriver) ShowOverlay(ctx context.Context) error {
	return ctx.Err()
}

func (noopOverlayDriver) CancelOverlay(ctx context.Context, reason overlayCancelReason) error {
	return ctx.Err()
}

type daemonController struct {
	mu      sync.Mutex
	overlay overlayDriver
	base    statusOutput
	active  bool
}

func newDaemonController(overlay overlayDriver, base statusOutput) *daemonController {
	if overlay == nil {
		overlay = noopOverlayDriver{}
	}
	return &daemonController{
		overlay: overlay,
		base:    base,
	}
}

func (c *daemonController) Dispatch(ctx context.Context, request ipcRequest) ipcResponse {
	command := strings.ToLower(strings.TrimSpace(request.Command))
	switch command {
	case "show":
		return c.show(ctx)
	case "hide":
		return c.hide(ctx)
	case "status":
		return c.statusResponse()
	default:
		if command == "" {
			command = request.Command
		}
		return ipcResponse{
			OK:      false,
			Command: command,
			Error:   fmt.Sprintf("unknown IPC command %q", request.Command),
		}
	}
}

func (c *daemonController) show(ctx context.Context) ipcResponse {
	c.mu.Lock()
	defer c.mu.Unlock()

	action := "shown"
	if c.active {
		if err := c.overlay.CancelOverlay(ctx, overlayCancelShowToggle); err != nil {
			return ipcErrorResponse("show", c.active, err)
		}
		c.active = false
		action = "hidden"
	} else {
		if err := c.overlay.ShowOverlay(ctx); err != nil {
			return ipcErrorResponse("show", c.active, err)
		}
		c.active = true
	}

	return ipcResponse{
		OK:      true,
		Command: "show",
		Active:  c.active,
		State:   activeState(c.active),
		Action:  action,
	}
}

func (c *daemonController) hide(ctx context.Context) ipcResponse {
	c.mu.Lock()
	defer c.mu.Unlock()

	action := "noop"
	if c.active {
		if err := c.overlay.CancelOverlay(ctx, overlayCancelHide); err != nil {
			return ipcErrorResponse("hide", c.active, err)
		}
		c.active = false
		action = "hidden"
	}

	return ipcResponse{
		OK:      true,
		Command: "hide",
		Active:  c.active,
		State:   activeState(c.active),
		Action:  action,
	}
}

func (c *daemonController) statusResponse() ipcResponse {
	c.mu.Lock()
	defer c.mu.Unlock()

	status := c.statusLocked()
	return ipcResponse{
		OK:      true,
		Command: "status",
		Active:  c.active,
		State:   activeState(c.active),
		Status:  &status,
	}
}

func (c *daemonController) Shutdown(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.active {
		return nil
	}
	if err := c.overlay.CancelOverlay(ctx, overlayCancelDaemonShutdown); err != nil {
		return err
	}
	c.active = false
	return nil
}

func (c *daemonController) statusLocked() statusOutput {
	status := c.base
	status.Command = "status"
	status.IPC = "connected"
	status.Active = c.active
	status.State = activeState(c.active)
	return status
}

func ipcErrorResponse(command string, active bool, err error) ipcResponse {
	return ipcResponse{
		OK:      false,
		Command: command,
		Active:  active,
		State:   activeState(active),
		Error:   err.Error(),
	}
}

func activeState(active bool) string {
	if active {
		return "active"
	}
	return "inactive"
}

type ipcServer struct {
	listener   net.Listener
	controller *daemonController
	logger     *slog.Logger
}

func newIPCServer(listener net.Listener, controller *daemonController, logger *slog.Logger) *ipcServer {
	return &ipcServer{
		listener:   listener,
		controller: controller,
		logger:     logger,
	}
}

func (s *ipcServer) Serve(ctx context.Context) error {
	var wg sync.WaitGroup
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = s.listener.Close()
		case <-done:
		}
	}()
	defer close(done)
	defer wg.Wait()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.handleConnection(ctx, conn)
		}()
	}
}

func (s *ipcServer) handleConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(ipcHandlerTimeout))

	var request ipcRequest
	if err := json.NewDecoder(conn).Decode(&request); err != nil {
		if !errors.Is(err, io.EOF) {
			s.logger.Debug("IPC request decode failed", "error", err)
			_ = json.NewEncoder(conn).Encode(ipcResponse{OK: false, Error: fmt.Sprintf("decode IPC request: %v", err)})
		}
		return
	}

	response := s.controller.Dispatch(ctx, request)
	if err := json.NewEncoder(conn).Encode(response); err != nil {
		s.logger.Debug("IPC response encode failed", "command", request.Command, "error", err)
	}
}

type ipcSocketOwner struct {
	path     string
	listener net.Listener
	info     os.FileInfo
	once     sync.Once
	err      error
}

func listenIPCSocket(path string) (*ipcSocketOwner, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create IPC socket directory %q: %w", filepath.Dir(path), err)
	}

	listener, err := net.Listen("unix", path)
	if err != nil {
		if staleErr := removeStaleSocketIfSafe(path); staleErr != nil {
			return nil, staleErr
		}
		listener, err = net.Listen("unix", path)
		if err != nil {
			if liveUnixSocket(path) {
				return nil, fmt.Errorf("refusing to start: another live mousekeys daemon owns IPC socket %q", path)
			}
			return nil, fmt.Errorf("listen on IPC socket %q: %w", path, err)
		}
	}

	info, err := os.Lstat(path)
	if err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("stat IPC socket %q after listen: %w", path, err)
	}
	return &ipcSocketOwner{path: path, listener: listener, info: info}, nil
}

func removeStaleSocketIfSafe(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat existing IPC socket path %q: %w", path, err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing to remove existing non-socket IPC path %q", path)
	}
	if liveUnixSocket(path) {
		return fmt.Errorf("refusing to start: another live mousekeys daemon owns IPC socket %q", path)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale IPC socket %q: %w", path, err)
	}
	return nil
}

func liveUnixSocket(path string) bool {
	conn, err := net.DialTimeout("unix", path, liveSocketProbeTime)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (o *ipcSocketOwner) Close() error {
	if o == nil {
		return nil
	}
	o.once.Do(func() {
		if o.listener != nil {
			if err := o.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				o.err = err
			}
		}
		if o.path == "" || o.info == nil {
			return
		}
		current, err := os.Lstat(o.path)
		if err != nil {
			if os.IsNotExist(err) {
				return
			}
			if o.err == nil {
				o.err = fmt.Errorf("stat IPC socket %q during cleanup: %w", o.path, err)
			}
			return
		}
		if os.SameFile(current, o.info) {
			if err := os.Remove(o.path); err != nil && !os.IsNotExist(err) && o.err == nil {
				o.err = fmt.Errorf("remove IPC socket %q: %w", o.path, err)
			}
		}
	})
	return o.err
}

func sendIPCCommand(ctx context.Context, getenv getenvFunc, command string) (ipcResponse, error) {
	socketPath, err := ipcSocketPathFromEnv(getenv)
	if err != nil {
		return ipcResponse{}, err
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, ipcClientTimeout)
		defer cancel()
	}

	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
	if err != nil {
		return ipcResponse{}, fmt.Errorf("connect to mousekeys daemon at %q: %w", socketPath, err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(ipcClientTimeout))
	}

	if err := json.NewEncoder(conn).Encode(ipcRequest{Command: command}); err != nil {
		return ipcResponse{}, fmt.Errorf("send IPC command %q: %w", command, err)
	}

	var response ipcResponse
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		return ipcResponse{}, fmt.Errorf("read IPC response for %q: %w", command, err)
	}
	if !response.OK {
		if response.Error == "" {
			response.Error = "daemon returned an unsuccessful response"
		}
		return response, errors.New(response.Error)
	}
	return response, nil
}

func ipcSocketPathFromEnv(getenv getenvFunc) (string, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	runtimeDir := strings.TrimSpace(getenv("XDG_RUNTIME_DIR"))
	if runtimeDir == "" {
		return "", fmt.Errorf("missing XDG_RUNTIME_DIR for mousekeys IPC")
	}
	return ipcSocketPathFromRuntimeDir(runtimeDir)
}

func ipcSocketPathFromRuntimeDir(runtimeDir string) (string, error) {
	if !filepath.IsAbs(runtimeDir) {
		return "", fmt.Errorf("invalid XDG_RUNTIME_DIR %q: must be an absolute path", runtimeDir)
	}
	info, err := os.Stat(runtimeDir)
	if err != nil {
		return "", fmt.Errorf("invalid XDG_RUNTIME_DIR %q: %w", runtimeDir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("invalid XDG_RUNTIME_DIR %q: not a directory", runtimeDir)
	}
	return filepath.Join(runtimeDir, ipcSocketName), nil
}

func newDaemonStatusBase(session sessionEnv, socketPath string, getenv getenvFunc) statusOutput {
	build := currentBuildInfo()
	binary := currentBinaryMetadata()
	return statusOutput{
		Command:    "status",
		IPC:        "connected",
		PID:        os.Getpid(),
		BuildID:    buildIdentifier(build),
		Build:      build,
		Executable: binary.Executable,
		Socket:     socketPath,
		RuntimeDir: session.XDGRuntimeDir,
		Binary:     binary,
		Service:    currentServiceMetadata(getenv),
	}
}

func currentBinaryMetadata() binaryMetadata {
	workingDirectory, _ := os.Getwd()
	args := make([]string, len(os.Args))
	copy(args, os.Args)
	return binaryMetadata{
		Executable:       executablePath(),
		Args:             args,
		WorkingDirectory: workingDirectory,
	}
}

func currentServiceMetadata(getenv getenvFunc) serviceMetadata {
	if getenv == nil {
		getenv = os.Getenv
	}
	service := serviceMetadata{
		UnitName: "mousekeys.service",
		Manager:  "unknown",
	}
	if invocationID := strings.TrimSpace(getenv("INVOCATION_ID")); invocationID != "" {
		service.Manager = "systemd-user"
		service.InvocationID = invocationID
	}
	if execPID := strings.TrimSpace(getenv("SYSTEMD_EXEC_PID")); execPID != "" {
		service.Manager = "systemd-user"
		service.SystemdExecPID = execPID
	}
	if journalStream := strings.TrimSpace(getenv("JOURNAL_STREAM")); journalStream != "" {
		service.JournalStream = journalStream
	}
	return service
}

func buildIdentifier(build buildInfo) string {
	return strings.Join([]string{build.Version, build.Commit, build.BuildDate, build.GoVersion, build.GOOS, build.GOARCH}, "|")
}
