package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/Covalane/turnyard/internal/agents"
	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/gitstate"
	"github.com/Covalane/turnyard/internal/inputs"
	"github.com/Covalane/turnyard/internal/lifecycle"
	"github.com/Covalane/turnyard/internal/observe"
	"github.com/Covalane/turnyard/internal/store"
)

func (s *Service) RunTask(ctx context.Context, tid, reply string, retry bool, timeout time.Duration) (result TaskRunResult, runError error) {
	task, err := s.Store.Task(ctx, tid)
	if err != nil {
		return TaskRunResult{}, err
	}
	release, err := s.reserveSession(task.SessionID)
	if err != nil {
		return TaskRunResult{}, err
	}
	defer release()
	ctx = observe.WithIDs(ctx, observe.IDs{SessionID: task.SessionID, TaskID: tid})
	preflightStarted, registered := false, false
	defer func() {
		if runError == nil || !preflightStarted || registered {
			return
		}
		if err := s.recordPreflightFailure(ctx, task, runError); err != nil {
			runError = fault.Wrap(fault.CodeInternalError, "persist preflight outcome", errors.Join(err, runError), "preflight failure could not be recorded")
			observe.LogFailure(ctx, "preflight outcome persistence failed", runError)
		}
	}()
	sessionRow, err := s.Store.Session(ctx, task.SessionID)
	if err != nil {
		return TaskRunResult{}, err
	}
	session, env, err := parseSessionRow(sessionRow)
	if err != nil {
		return TaskRunResult{}, err
	}
	var work contracts.WorkSpec
	if err := json.Unmarshal([]byte(task.Spec), &work); err != nil {
		return TaskRunResult{}, err
	}
	workspace := sessionRow.Workspace
	state := filepath.Join(filepath.Dir(workspace), gitstate.AgentStateDirName)
	backend, err := s.BackendFactory(env.Sandbox.Backend)
	if err != nil {
		return TaskRunResult{}, err
	}
	switch task.Status {
	case lifecycle.Queued:
		if reply != "" || retry {
			return TaskRunResult{}, fault.New(fault.CodeInvalidTransition, "new task takes no reply or retry flag")
		}
	case lifecycle.NeedsInput:
		if reply == "" || retry {
			return TaskRunResult{}, fault.New(fault.CodeInputRequired, "waiting task needs a human reply")
		}
	case lifecycle.Failed:
		if !retry || reply != "" {
			return TaskRunResult{}, fault.New(fault.CodeInvalidTransition, "failed task requires explicit retry")
		}
	default:
		return TaskRunResult{}, fault.New(fault.CodeInvalidTransition, "cannot run task in state %s", task.Status)
	}
	preflightStarted = true
	kind := CapacityRegular
	if sessionRow.ParentSessionID.Present {
		kind = CapacityChild
	} else {
		for _, candidate := range env.Agents {
			if candidate.ID == session.PrimaryAgent && len(candidate.Delegates) > 0 {
				kind = CapacityDelegating
				break
			}
		}
	}
	releaseCapacity, err := s.Capacity.Reserve(ctx, env.Sandbox, kind)
	if err != nil {
		return TaskRunResult{}, err
	}
	defer releaseCapacity()
	if _, err := os.Stat(workspace); err != nil {
		return TaskRunResult{}, fault.New(fault.CodeWorkspaceMissing, "restore checkpoint before resuming")
	}
	if sessionRow.NativeID.Present {
		if _, err := os.Stat(state); err != nil {
			return TaskRunResult{}, fault.New(fault.CodeNativeStateMissing, "restore native agent state before resuming")
		}
	}
	if err := s.verifyCheckpointHead(ctx, task.SessionID, session, workspace); err != nil {
		return TaskRunResult{}, err
	}
	if err := inputs.Verify(ctx, filepath.Dir(workspace), work); err != nil {
		return TaskRunResult{}, err
	}
	before, err := gitstate.WorkspaceStatus(ctx, workspace, session.Repositories)
	if err != nil {
		return TaskRunResult{}, err
	}
	if task.Status == lifecycle.Queued {
		for _, v := range before {
			if v.Dirty {
				return TaskRunResult{}, fault.New(fault.CodeWorkspaceDrift, "new task requires a clean workspace")
			}
		}
	}
	turnID, invocationID := contracts.NewID("turn"), contracts.NewID("inv")
	ctx = observe.WithIDs(ctx, observe.IDs{InvocationID: invocationID})
	agent, binding, err := agents.SelectAgent(env, session.PrimaryAgent)
	if err != nil {
		return TaskRunResult{}, err
	}
	logPath := filepath.Join(filepath.Dir(workspace), "logs", invocationID+".jsonl")
	input := reply
	if input == "" {
		input = work.Objective
	}
	err = s.Store.StartInvocation(ctx, store.StartInvocationInput{
		TaskID: tid, SessionID: task.SessionID, ExpectedStatus: task.Status, TurnID: turnID,
		InvocationID: invocationID, Input: input, Runtime: agent.Runtime, Model: binding.Model,
		SandboxBackend: backend.Name(), LogPath: logPath, Before: before,
	})
	if err != nil {
		return TaskRunResult{}, err
	}
	registered = true
	// Once the invocation is registered, every failure is durably classified.
	agentAttempted := false
	plan := turnExecution{task: task, row: sessionRow, session: session, env: env, work: work,
		agent: agent, backend: backend, workspace: workspace, state: state, turnID: turnID,
		invocationID: invocationID, logPath: logPath, reply: reply, timeout: timeout}
	result, runErr := s.performTurn(ctx, plan, &agentAttempted)
	if runErr == nil {
		return result, nil
	}
	code := fault.CodeOf(runErr)
	final := lifecycle.Failed
	// A driver may have made external calls before reporting a parse or evidence error.
	// Require explicit reconciliation before retrying an uncertain outcome.
	if agentAttempted {
		final = lifecycle.Unknown
	}
	persistCtx, cancelPersist := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancelPersist()
	persistErr := s.Store.FailInvocation(persistCtx, store.InvocationFailure{TaskID: tid, SessionID: task.SessionID, TurnID: turnID, InvocationID: invocationID, Status: final, Code: code, AgentAttempted: agentAttempted, Message: runErr.Error(), RequestID: observe.IDsFrom(ctx).RequestID})
	if persistErr != nil {
		combined := fault.Wrap(fault.CodeInternalError, "persist invocation outcome", errors.Join(persistErr, runErr), "could not record %s outcome", final)
		observe.LogFailure(ctx, "invocation outcome persistence failed", combined, "invocation_id", invocationID)
		return TaskRunResult{}, combined
	}
	observe.Log.WarnContext(ctx, "invocation classified", "session_id", task.SessionID, "task_id", tid, "invocation_id", invocationID, "status", final, "code", code, "origin", fault.Origin(runErr), "stack", fault.Stack(runErr))
	return TaskRunResult{}, runErr
}

// Persist scheduled failures that occur before an invocation is registered.
func (s *Service) recordPreflightFailure(ctx context.Context, task store.TaskRow, cause error) error {
	code := ErrorCode(cause)
	next := task.Status
	if next == lifecycle.Queued {
		next = lifecycle.Failed
	}
	persistCtx, cancelPersist := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancelPersist()
	err := s.Store.FailPreflight(persistCtx, task.ID, task.SessionID, task.Status, next, fault.Code(code))
	if err == nil {
		observe.Log.WarnContext(ctx, "task preflight failed", "session_id", task.SessionID, "task_id", task.ID, "status", next, "code", code)
	}
	return err
}
