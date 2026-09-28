package transport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/engine"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/lifecycle"
	"github.com/Covalane/turnyard/internal/observe"
	"github.com/Covalane/turnyard/internal/stateops"
)

type Request struct {
	Action       Action `json:"action"`
	RequestID    string `json:"requestId,omitempty"`
	File         string `json:"file,omitempty"`
	SessionID    string `json:"sessionId,omitempty"`
	TaskID       string `json:"taskId,omitempty"`
	CheckpointID string `json:"checkpointId,omitempty"`
	After        int64  `json:"after,omitempty"`
	Reply        string `json:"reply,omitempty"`
	Reason       string `json:"reason,omitempty"`
	Retry        bool   `json:"retry,omitempty"`
	Timeout      int    `json:"timeout,omitempty"`
}
type Response struct {
	OK        bool         `json:"ok"`
	Result    any          `json:"result,omitempty"`
	Error     *fault.Error `json:"error,omitempty"`
	RequestID string       `json:"requestId,omitempty"`
}

const scheduledOperationVerify = "verify"

func SocketPath(home string) string {
	abs, _ := filepath.Abs(home)
	hash := sha256.Sum256([]byte(abs))
	return filepath.Join(os.TempDir(), "turnyard-"+hex.EncodeToString(hash[:8])+".sock")
}

type Supervisor struct {
	Service        *engine.Service
	mu             sync.Mutex
	active         map[string]bool
	listener       net.Listener
	stopping       atomic.Bool
	stateBytes     atomic.Int64
	stateWarnBytes int64
	stateWarned    atomic.Bool
}

func NewSupervisor(ctx context.Context, home string) (*Supervisor, error) {
	limits, err := capacityFromEnvironment()
	if err != nil {
		return nil, err
	}
	warnBytes, err := stateWarningBytes()
	if err != nil {
		return nil, err
	}
	service, err := engine.NewServiceWithCapacity(ctx, home, limits)
	if err != nil {
		return nil, err
	}
	recovered, err := service.Store.RecoverInterrupted(ctx)
	if err != nil {
		return nil, errors.Join(err, fault.At(service.Close(), "close failed supervisor initialization"))
	}
	if len(recovered) > 0 {
		observe.Log.WarnContext(ctx, "interrupted invocations marked unknown", "count", len(recovered), "taskIds", recovered)
		for _, tid := range recovered {
			if err := service.StopTaskContainers(ctx, tid); err != nil {
				observe.LogFailure(observe.WithIDs(ctx, observe.IDs{TaskID: tid}), "interrupted container cleanup unconfirmed", err)
			} else {
				observe.Log.InfoContext(ctx, "interrupted containers stopped", "taskId", tid)
			}
		}
	}
	return &Supervisor{Service: service, active: map[string]bool{}, stateWarnBytes: warnBytes}, nil
}
func (s *Supervisor) schedule(ctx context.Context, tid, reply string, retry bool, timeout int) (map[string]any, error) {
	ctx = observe.WithIDs(ctx, observe.IDs{TaskID: tid})
	task, err := s.Service.Store.Task(ctx, tid)
	if err != nil {
		return nil, err
	}
	if timeout == 0 {
		timeout = 900
	}
	if timeout < 1 || timeout > 7200 {
		return nil, fault.New(fault.CodeInvalidRequest, "timeout must be between 1 and 7200 seconds")
	}
	switch task.Status {
	case lifecycle.Queued:
		if reply != "" || retry {
			return nil, fault.New(fault.CodeInvalidTransition, "queued task requires a first run")
		}
	case lifecycle.NeedsInput:
		if reply == "" || retry {
			return nil, fault.New(fault.CodeInputRequired, "waiting task requires a human reply")
		}
	case lifecycle.Failed:
		if !retry || reply != "" {
			return nil, fault.New(fault.CodeInvalidTransition, "failed task requires explicit retry")
		}
	default:
		return nil, fault.New(fault.CodeInvalidTransition, "task is %s", task.Status)
	}
	s.mu.Lock()
	if s.active[task.SessionID] {
		s.mu.Unlock()
		return nil, fault.New(fault.CodeConcurrentRun, "session already has active work")
	}
	s.active[task.SessionID] = true
	s.mu.Unlock()
	runCtx := context.WithoutCancel(observe.WithIDs(ctx, observe.IDs{SessionID: task.SessionID, TaskID: tid}))
	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.active, task.SessionID)
			s.mu.Unlock()
		}()
		started := time.Now()
		observe.Log.InfoContext(runCtx, "task run started", "sessionId", task.SessionID, "taskId", tid, "retry", retry)
		_, err := s.Service.RunTask(runCtx, tid, reply, retry, time.Duration(timeout)*time.Second)
		if err != nil {
			observe.LogFailure(runCtx, "task run failed", fault.Ensure(err, "run task"), "durationMs", time.Since(started).Milliseconds())
		} else {
			observe.Log.InfoContext(runCtx, "task run finished", "sessionId", task.SessionID, "taskId", tid, "durationMs", time.Since(started).Milliseconds())
		}
	}()
	return map[string]any{"taskId": tid, "accepted": true}, nil
}
func (s *Supervisor) scheduleVerify(ctx context.Context, tid string) (map[string]any, error) {
	ctx = observe.WithIDs(ctx, observe.IDs{TaskID: tid})
	task, err := s.Service.Store.Task(ctx, tid)
	if err != nil {
		return nil, err
	}
	if task.Status != lifecycle.Failed || !task.CandidateID.Present {
		return nil, fault.New(fault.CodeInvalidTransition, "task has no failed candidate")
	}
	s.mu.Lock()
	if s.active[task.SessionID] {
		s.mu.Unlock()
		return nil, fault.New(fault.CodeConcurrentRun, "session already has active work")
	}
	s.active[task.SessionID] = true
	s.mu.Unlock()
	runCtx := context.WithoutCancel(observe.WithIDs(ctx, observe.IDs{SessionID: task.SessionID, TaskID: tid}))
	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.active, task.SessionID)
			s.mu.Unlock()
		}()
		started := time.Now()
		observe.Log.InfoContext(runCtx, "candidate verification started", "sessionId", task.SessionID, "taskId", tid)
		_, err := s.Service.VerifyCandidate(runCtx, tid)
		if err != nil {
			observe.LogFailure(runCtx, "candidate verification failed", fault.Ensure(err, "verify candidate"), "durationMs", time.Since(started).Milliseconds())
		} else {
			observe.Log.InfoContext(runCtx, "candidate verification finished", "sessionId", task.SessionID, "taskId", tid, "durationMs", time.Since(started).Milliseconds())
		}
	}()
	return map[string]any{"taskId": tid, "accepted": true, "operation": scheduledOperationVerify}, nil
}
func (s *Supervisor) Dispatch(ctx context.Context, req Request) (any, error) {
	switch req.Action {
	case actionPing:
		return map[string]any{"ok": true, "pid": os.Getpid(), "capacity": s.Service.Capacity.Status(),
			"stateBytes": s.stateBytes.Load(), "stateWarning": s.stateWarned.Load()}, nil
	case actionSessionCreate:
		return s.Service.CreateSession(ctx, req.File)
	case actionSessionShow:
		return s.Service.SessionResult(ctx, req.SessionID)
	case actionSessionComplete:
		s.mu.Lock()
		if s.active[req.SessionID] {
			s.mu.Unlock()
			return nil, fault.New(fault.CodeSessionBusy, "session has active work")
		}
		s.active[req.SessionID] = true
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			delete(s.active, req.SessionID)
			s.mu.Unlock()
		}()
		return s.Service.CompleteSession(ctx, req.SessionID)
	case actionSessionPublish:
		s.mu.Lock()
		if s.active[req.SessionID] {
			s.mu.Unlock()
			return nil, fault.New(fault.CodeSessionBusy, "session has active work")
		}
		s.active[req.SessionID] = true
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			delete(s.active, req.SessionID)
			s.mu.Unlock()
		}()
		return s.Service.PublishSession(ctx, req.SessionID)
	case actionSessionCancel:
		s.mu.Lock()
		if s.active[req.SessionID] {
			s.mu.Unlock()
			return nil, fault.New(fault.CodeSessionBusy, "session has active work")
		}
		s.active[req.SessionID] = true
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			delete(s.active, req.SessionID)
			s.mu.Unlock()
		}()
		return s.Service.CancelSession(ctx, req.SessionID, req.Reason)
	case actionTaskAdd:
		return s.Service.AddTask(ctx, req.SessionID, req.File)
	case actionTaskRun:
		return s.schedule(ctx, req.TaskID, req.Reply, req.Retry, req.Timeout)
	case actionTaskShow:
		result, err := s.Service.TaskResult(ctx, req.TaskID)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		active := s.active[result.Task.SessionID]
		s.mu.Unlock()
		result.Active = &active
		return result, nil
	case actionTaskReconcile:
		return s.Service.ReconcileFailed(ctx, req.TaskID)
	case actionTaskVerify:
		return s.scheduleVerify(ctx, req.TaskID)
	case actionEvents:
		return s.Service.Events(ctx, req.SessionID, req.After)
	case actionCheckpointRestore:
		return s.Service.Restore(ctx, req.CheckpointID)
	case actionDaemonStop:
		s.mu.Lock()
		busy := len(s.active) > 0 || s.Service.ActiveDelegations() > 0
		s.mu.Unlock()
		if busy {
			return nil, fault.New(fault.CodeSessionBusy, "active work must finish before shutdown")
		}
		s.stopping.Store(true)
		go func() {
			time.Sleep(100 * time.Millisecond)
			if err := s.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				observe.LogFailure(ctx, "supervisor listener shutdown failed", fault.Ensure(err, "close listener"))
			}
		}()
		return map[string]any{"stopping": true}, nil
	default:
		return nil, fault.New(fault.CodeUnknownAction, "unknown action %s", req.Action)
	}
}
func (s *Supervisor) handle(parent context.Context, conn net.Conn) {
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(supervisorWireTimeout)); err != nil {
		observe.LogFailure(parent, "supervisor connection deadline failed", fault.Wrap(fault.CodeDaemonUnavailable, "set supervisor deadline", err, "connection deadline unavailable"))
		return
	}
	line, readErr := readFrame(conn, maxRequestBytes)
	if readErr != nil && readErr != errFrameTooLarge {
		return
	}
	var req Request
	response := Response{}
	logCtx := parent
	if readErr == errFrameTooLarge || json.Unmarshal(line, &req) != nil {
		response.Error = &fault.Error{Code: fault.CodeInvalidRequest, Message: "invalid request JSON"}
	} else {
		if req.RequestID == "" {
			req.RequestID = contracts.NewID("req")
		}
		response.RequestID = req.RequestID
		base := observe.WithIDs(parent, observe.IDs{RequestID: req.RequestID, SessionID: req.SessionID, TaskID: req.TaskID})
		logCtx = base
		workLimit, limitErr := workTimeout(req)
		if limitErr != nil {
			response.Error = &fault.Error{Code: fault.CodeInvalidRequest, Message: limitErr.Error()}
		} else {
			if err := conn.SetDeadline(time.Now().Add(workLimit + wireResponseMargin)); err != nil {
				observe.LogFailure(base, "supervisor work deadline failed", fault.Wrap(fault.CodeDaemonUnavailable, "set supervisor work deadline", err, "connection deadline unavailable"))
				response.Error = &fault.Error{Code: fault.CodeDaemonUnavailable, Message: "connection deadline unavailable"}
			} else {
				ctx, cancel := context.WithTimeout(base, workLimit)
				defer cancel()
				value, err := s.Dispatch(ctx, req)
				if err != nil {
					observed := fault.Ensure(err, "dispatch supervisor request")
					observe.LogFailure(ctx, "supervisor request failed", observed, "action", req.Action)
					response.Error = &fault.Error{Code: fault.CodeOf(observed), Message: err.Error()}
				} else {
					response.OK = true
					response.Result = value
				}
			}
		}
	}
	data, err := json.Marshal(response)
	if err != nil || len(data)+1 > maxResponseBytes {
		if err != nil {
			observe.LogFailure(logCtx, "supervisor response encoding failed", fault.Wrap(fault.CodeInternalError, "encode supervisor response", err, "response encoding failed"))
		}
		response = Response{RequestID: response.RequestID, Error: &fault.Error{Code: fault.CodeInternalError, Message: "supervisor response cannot be encoded within size limit"}}
		data, err = json.Marshal(response)
		if err != nil {
			observe.LogFailure(logCtx, "supervisor fallback response encoding failed", fault.Wrap(fault.CodeInternalError, "encode fallback response", err, "fallback response encoding failed"))
			return
		}
	}
	if _, err := conn.Write(append(data, '\n')); err != nil {
		observe.LogFailure(logCtx, "supervisor response delivery failed", fault.Wrap(fault.CodeDaemonUnavailable, "send supervisor response", err, "response could not be delivered"))
	}
}
func Serve(ctx context.Context, home string) error {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	lock, err := stateops.Acquire(home)
	if err != nil {
		return fault.Wrap(fault.CodeAlreadyRunning, "lock state", err, "supervisor lock is held")
	}
	defer func() {
		if err := lock.Close(); err != nil {
			observe.LogFailure(ctx, "supervisor lock release failed", fault.Ensure(err, "release supervisor lock"))
		}
	}()
	path := SocketPath(home)
	if _, err := os.Stat(path); err == nil {
		connection, err := net.DialTimeout("unix", path, time.Second)
		if err == nil {
			_ = connection.Close()
			return fault.New(fault.CodeAlreadyRunning, "supervisor already running")
		}
		if err := os.Remove(path); err != nil {
			return fault.Wrap(fault.CodeDaemonUnavailable, "remove stale socket", err, "stale supervisor socket could not be removed")
		}
	}
	s, err := NewSupervisor(ctx, home)
	if err != nil {
		return err
	}
	defer func() {
		if err := s.Service.Close(); err != nil {
			observe.LogFailure(ctx, "supervisor store close failed", fault.Ensure(err, "close supervisor store"))
		}
	}()
	listener, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	s.listener = listener
	defer func() {
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			observe.LogFailure(ctx, "supervisor listener close failed", fault.Ensure(err, "close listener"))
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			observe.LogFailure(ctx, "supervisor socket removal failed", fault.Ensure(err, "remove socket"))
		}
	}()
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	s.scanState(ctx, home)
	observe.Log.InfoContext(ctx, "supervisor listening", "socket", path, "pid", os.Getpid())
	monitorCtx, stopMonitor := context.WithCancel(ctx)
	defer stopMonitor()
	go s.monitorState(monitorCtx, home)
	for {
		conn, err := listener.Accept()
		if err != nil {
			if s.stopping.Load() {
				return nil
			}
			return err
		}
		go s.handle(ctx, conn)
	}
}
