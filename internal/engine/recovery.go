package engine

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/gitstate"
	"github.com/Covalane/turnyard/internal/lifecycle"
	"github.com/Covalane/turnyard/internal/observe"
)

func (s *Service) StopTaskContainers(ctx context.Context, tid string) error {
	ctx = observe.WithIDs(ctx, observe.IDs{TaskID: tid})
	task, err := s.Store.Task(ctx, tid)
	if err != nil {
		return fault.At(err, "load cleanup task")
	}
	ctx = observe.WithIDs(ctx, observe.IDs{SessionID: task.SessionID})
	row, err := s.Store.Session(ctx, task.SessionID)
	if err != nil {
		return fault.At(err, "load cleanup session")
	}
	_, environment, err := parseSessionRow(row)
	if err != nil {
		return fault.At(err, "decode cleanup environment")
	}
	checkBackend, err := s.BackendFactory(environment.Sandbox.Backend)
	var failures []error
	if err != nil {
		failures = append(failures, fault.At(err, "select check cleanup backend"))
	} else if err := checkBackend.StopTaskChecks(ctx, tid); err != nil {
		failures = append(failures, fault.At(err, "stop task check containers"))
	}
	invocations, err := s.Store.Invocations(ctx, tid)
	if err != nil {
		return errors.Join(append(failures, fault.At(err, "load cleanup invocations"))...)
	}
	for _, invocation := range invocations {
		invocationCtx := observe.WithIDs(ctx, observe.IDs{InvocationID: invocation.ID})
		backend, err := s.BackendFactory(invocation.SandboxBackend)
		if err != nil {
			failures = append(failures, fault.At(err, "select invocation cleanup backend"))
			continue
		}
		if err := backend.StopInvocation(invocationCtx, invocation.ID); err != nil {
			observe.LogFailure(invocationCtx, "invocation container cleanup unconfirmed", err, "backend", invocation.SandboxBackend)
			failures = append(failures, fault.At(err, "stop invocation containers"))
		}
	}
	return errors.Join(failures...)
}

// ReconcileFailed is an explicit operator decision after inspecting the
// workspace and external effects of an unknown invocation. It never retries
// the agent implicitly and refuses to proceed while a container may remain.
func (s *Service) ReconcileFailed(ctx context.Context, tid string) (ReconcileResult, error) {
	task, err := s.Store.Task(ctx, tid)
	if err != nil {
		return ReconcileResult{}, err
	}
	if task.Status != lifecycle.Failed && task.Status != lifecycle.Unknown {
		return ReconcileResult{}, fault.New(fault.CodeInvalidTransition, "only failed or unknown task can reconcile its workspace")
	}
	if task.Status == lifecycle.Unknown {
		if err := s.StopTaskContainers(ctx, tid); err != nil {
			return ReconcileResult{}, err
		}
	}
	row, err := s.Store.Session(ctx, task.SessionID)
	if err != nil {
		return ReconcileResult{}, err
	}
	if lifecycle.TerminalSession(row.Status) {
		return ReconcileResult{}, fault.New(fault.CodeInvalidTransition, "terminal session cannot reconcile tasks")
	}
	session, _, err := parseSessionRow(row)
	if err != nil {
		return ReconcileResult{}, err
	}
	state := filepath.Join(filepath.Dir(row.Workspace), gitstate.AgentStateDirName)
	id, err := s.saveCheckpoint(ctx, task.SessionID, tid, session, row.Workspace, state, row.NativeID.Value)
	if err != nil {
		return ReconcileResult{}, err
	}
	if task.Status == lifecycle.Unknown {
		if err := s.Store.ReconcileUnknown(ctx, task.SessionID, tid, id, task.ErrorCode.Value); err != nil {
			return ReconcileResult{}, err
		}
		observe.Log.WarnContext(ctx, "unknown task reconciled", "session_id", task.SessionID, "task_id", tid, "checkpoint_id", id)
	}
	status, err := gitstate.WorkspaceStatus(ctx, row.Workspace, session.Repositories)
	return ReconcileResult{TaskID: tid, CheckpointID: id, Workspace: status}, err
}
