package transport

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
)

func DefaultHome() string {
	if value := os.Getenv("TURNYARD_HOME"); value != "" {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "turnyard")
	}
	return filepath.Join(home, ".local", "share", "turnyard")
}

type clientResponse struct {
	OK        bool            `json:"ok"`
	Result    json.RawMessage `json:"result"`
	Error     *fault.Error    `json:"error"`
	RequestID string          `json:"request_id"`
}

func ClientRequest(ctx context.Context, home string, req Request, start bool) (map[string]any, error) {
	response, err := clientRequestResponse(ctx, home, req, start)
	if err != nil {
		return nil, err
	}
	return decodeClientResult[map[string]any](response)
}

func decodeClientResult[T any](response *clientResponse) (T, error) {
	var result T
	if err := json.Unmarshal(response.Result, &result); err != nil {
		wrapped := fault.Wrap(fault.CodeDaemonUnavailable, "decode supervisor result", err, "supervisor result is invalid")
		if typed, ok := wrapped.(*fault.Error); ok {
			typed.RequestID = response.RequestID
		}
		return result, wrapped
	}
	return result, nil
}

func clientRequestResponse(ctx context.Context, home string, req Request, start bool) (wire *clientResponse, requestErr error) {
	if req.RequestID == "" {
		id, err := contracts.NewID("req")
		if err != nil {
			return nil, err
		}
		req.RequestID = id
	}
	defer func() {
		if requestErr == nil {
			return
		}
		requestErr = fault.Ensure(requestErr, "client supervisor request")
		var typed *fault.Error
		if errors.As(requestErr, &typed) {
			typed.RequestID = req.RequestID
		}
	}()
	path := SocketPath(home)
	if _, err := os.Stat(path); err != nil && start {
		if err := StartDaemon(ctx, home); err != nil {
			return nil, err
		}
	}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "unix", path)
	if err != nil && start {
		if startErr := StartDaemon(ctx, home); startErr != nil {
			return nil, startErr
		}
		conn, err = dialer.DialContext(ctx, "unix", path)
	}
	if err != nil {
		return nil, fault.Wrap(fault.CodeDaemonUnavailable, "dial supervisor", err, "supervisor not reachable")
	}
	defer conn.Close()
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	defer close(done)
	deadline := time.Now().Add(wireTimeout(req))
	if parent, ok := ctx.Deadline(); ok && parent.Before(deadline) {
		deadline = parent
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, fault.Wrap(fault.CodeDaemonUnavailable, "set supervisor deadline", err, "supervisor connection deadline unavailable")
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fault.Wrap(fault.CodeInvalidRequest, "encode request", err, "supervisor request is invalid")
	}
	if len(data)+1 > maxRequestBytes {
		return nil, fault.New(fault.CodeInvalidRequest, "supervisor request exceeds size limit")
	}
	if _, err := conn.Write(append(data, '\n')); err != nil {
		return nil, fault.Wrap(fault.CodeDaemonUnavailable, "send supervisor request", err, "supervisor request could not be sent")
	}
	line, err := readFrame(conn, maxResponseBytes)
	if err != nil {
		return nil, fault.Wrap(fault.CodeDaemonUnavailable, "read supervisor", err, "supervisor response unavailable")
	}
	var response clientResponse
	if err := json.Unmarshal(line, &response); err != nil {
		return nil, fault.Wrap(fault.CodeDaemonUnavailable, "decode supervisor response", err, "supervisor response is invalid")
	}
	if !response.OK {
		if response.Error == nil {
			return nil, fault.New(fault.CodeDaemonUnavailable, "empty supervisor response")
		}
		response.Error.RequestID = response.RequestID
		return nil, response.Error
	}
	return &response, nil
}
func StartDaemon(ctx context.Context, home string) error {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(SocketPath(home)); err == nil {
		if _, err := ClientRequest(ctx, home, Request{Action: actionPing}, false); err == nil {
			return nil
		}
	}
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	log, err := os.OpenFile(filepath.Join(home, "daemon.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := exec.Command(binary, "daemon", "serve", "--home", home)
	cmd.Stdin = nil
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Start(); err != nil {
		return fault.Wrap(fault.CodeDaemonFailed, "start supervisor", err, "supervisor did not start")
	}
	_ = cmd.Process.Release()
	for i := 0; i < 100; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := ClientRequest(ctx, home, Request{Action: actionPing}, false); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fault.New(fault.CodeDaemonFailed, "supervisor did not start; inspect daemon.log")
}
