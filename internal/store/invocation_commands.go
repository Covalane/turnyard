package store

import (
	"context"

	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/lifecycle"
	"github.com/Covalane/turnyard/internal/store/ent"
	"github.com/Covalane/turnyard/internal/store/ent/task"
	"github.com/Covalane/turnyard/internal/store/ent/turn"
)

type StartInvocationInput struct {
	TaskID, SessionID, ExpectedStatus, TurnID, InvocationID, Input, Runtime, Model, SandboxBackend, LogPath string
	Before                                                                                                  any
}

func (s *Store) StartInvocation(ctx context.Context, in StartInvocationInput) error {
	return s.write(ctx, func(tx *ent.Tx) error {
		currentSession, err := tx.Session.Get(ctx, in.SessionID)
		if err != nil {
			return notFound(err, "session")
		}
		if lifecycle.TerminalSession(currentSession.Status) {
			return fault.New(fault.CodeInvalidTransition, "terminal session cannot run tasks")
		}
		current, err := tx.Task.Get(ctx, in.TaskID)
		if err != nil {
			return err
		}
		if current.Status != in.ExpectedStatus {
			return fault.New(fault.CodeConcurrentRun, "task changed during preparation")
		}
		active, err := tx.Task.Query().Where(task.SessionIDEQ(in.SessionID), task.StatusEQ(lifecycle.Running)).Exist(ctx)
		if err != nil {
			return err
		}
		if active {
			return fault.New(fault.CodeConcurrentRun, "another task is running")
		}
		count, err := tx.Turn.Query().Where(turn.TaskIDEQ(in.TaskID)).Count(ctx)
		if err != nil {
			return err
		}
		if err := tx.Task.UpdateOneID(in.TaskID).SetStatus(lifecycle.Running).ClearErrorCode().Exec(ctx); err != nil {
			return err
		}
		if err := tx.Session.UpdateOneID(in.SessionID).SetStatus(lifecycle.Running).Exec(ctx); err != nil {
			return err
		}
		if err := tx.Turn.Create().SetID(in.TurnID).SetTaskID(in.TaskID).SetSequence(count + 1).
			SetInput(in.Input).SetStatus(lifecycle.Running).SetInvocationID(in.InvocationID).SetCreatedAt(now()).Exec(ctx); err != nil {
			return err
		}
		if err := tx.Invocation.Create().SetID(in.InvocationID).SetTurnID(in.TurnID).
			SetRuntime(in.Runtime).SetModel(in.Model).SetSandboxBackend(in.SandboxBackend).
			SetStatus(lifecycle.Starting).SetLogPath(in.LogPath).SetStartedAt(now()).Exec(ctx); err != nil {
			return err
		}
		return addEvent(ctx, tx, in.SessionID, in.TaskID, lifecycle.EventInvocationStarting, map[string]any{
			"turn_id": in.TurnID, "invocation_id": in.InvocationID, "before": in.Before, "model": in.Model, "sandbox": in.SandboxBackend})
	})
}

type InvocationFailure struct {
	TaskID, SessionID, TurnID, InvocationID, Status string
	Code                                            fault.Code
	AgentAttempted                                  bool
	Message, RequestID                              string
}

func (s *Store) FailInvocation(ctx context.Context, in InvocationFailure) error {
	return s.write(ctx, func(tx *ent.Tx) error {
		if err := tx.Task.UpdateOneID(in.TaskID).SetStatus(in.Status).SetErrorCode(string(in.Code)).Exec(ctx); err != nil {
			return err
		}
		if err := tx.Turn.UpdateOneID(in.TurnID).SetStatus(in.Status).Exec(ctx); err != nil {
			return err
		}
		if err := tx.Invocation.UpdateOneID(in.InvocationID).SetStatus(in.Status).SetEndedAt(now()).Exec(ctx); err != nil {
			return err
		}
		if err := tx.Session.UpdateOneID(in.SessionID).SetStatus(lifecycle.Paused).Exec(ctx); err != nil {
			return err
		}
		return addEvent(ctx, tx, in.SessionID, in.TaskID, lifecycle.EventInvocationError, map[string]any{
			"invocation_id": in.InvocationID, "request_id": in.RequestID, "code": in.Code,
			"agent_attempted": in.AgentAttempted, "message": in.Message})
	})
}
func (s *Store) FailPreflight(ctx context.Context, taskID, sessionID, expectedStatus, nextStatus string, code fault.Code) error {
	return s.write(ctx, func(tx *ent.Tx) error {
		currentSession, err := tx.Session.Get(ctx, sessionID)
		if err != nil {
			return notFound(err, "session")
		}
		if lifecycle.TerminalSession(currentSession.Status) {
			return fault.New(fault.CodeInvalidTransition, "terminal session cannot record task preflight failure")
		}
		current, err := tx.Task.Get(ctx, taskID)
		if err != nil {
			return err
		}
		if current.Status != expectedStatus {
			return fault.New(fault.CodeConcurrentRun, "task changed during preflight")
		}
		if err := tx.Task.UpdateOneID(taskID).SetStatus(nextStatus).SetErrorCode(string(code)).Exec(ctx); err != nil {
			return err
		}
		if err := tx.Session.UpdateOneID(sessionID).SetStatus(lifecycle.Paused).Exec(ctx); err != nil {
			return err
		}
		return addEvent(ctx, tx, sessionID, taskID, lifecycle.EventTaskPreflightFailed, map[string]any{"status": nextStatus, "code": code})
	})
}
func (s *Store) CompleteAgent(ctx context.Context, sessionID, taskID, invocationID, nativeID string, payload any) error {
	return s.write(ctx, func(tx *ent.Tx) error {
		if err := tx.Session.UpdateOneID(sessionID).SetNativeID(nativeID).Exec(ctx); err != nil {
			return err
		}
		if err := tx.Invocation.UpdateOneID(invocationID).SetStatus(lifecycle.Completed).SetNativeID(nativeID).
			SetExitCode(0).SetEndedAt(now()).Exec(ctx); err != nil {
			return err
		}
		return addEvent(ctx, tx, sessionID, taskID, lifecycle.EventAgentCompleted, payload)
	})
}
