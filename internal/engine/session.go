package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/Covalane/turnyard/internal/agents"
	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/gitstate"
	"github.com/Covalane/turnyard/internal/inputs"
	"github.com/Covalane/turnyard/internal/lifecycle"
	"github.com/Covalane/turnyard/internal/observe"
	"github.com/Covalane/turnyard/internal/store"
)

// CreateSession replays a matching creation key and locks image identity before cloning.
func (s *Service) CreateSession(ctx context.Context, path string) (SessionCreationResult, error) {
	session, env, err := contracts.LoadSession(path)
	if err != nil {
		return SessionCreationResult{}, err
	}
	return s.createSession(ctx, session, env, store.CreateSessionInput{})
}

// createSession also serves child sessions. A child uses the same validation,
// image pinning, clone and persistence path as a top-level session.
func (s *Service) createSession(ctx context.Context, session contracts.SessionSpec, env contracts.EnvironmentSpec, parent store.CreateSessionInput) (SessionCreationResult, error) {
	creationDigest := contracts.Digest(struct {
		Session     contracts.SessionSpec
		Environment contracts.EnvironmentSpec
	}{session, env})
	prior, found, err := s.Store.SessionByCreationKey(ctx, session.IdempotencyKey)
	if err != nil {
		return SessionCreationResult{}, err
	}
	if found {
		if prior.Digest != creationDigest {
			return SessionCreationResult{}, fault.New(fault.CodeIdempotencyConflict, "session idempotency key already names different input")
		}
		return sessionCreationResult(prior), nil
	}
	for _, agent := range env.Agents {
		_, binding, err := agents.SelectAgent(env, agent.ID)
		if err != nil {
			return SessionCreationResult{}, err
		}
		driver, err := s.DriverFactory(agent.Runtime)
		if err != nil {
			return SessionCreationResult{}, err
		}
		if driver.Runtime() != agent.Runtime {
			return SessionCreationResult{}, fault.New(fault.CodeRuntimeUnavailable, "driver for %s reported runtime %s", agent.Runtime, driver.Runtime())
		}
		if err := driver.ValidateBinding(binding); err != nil {
			return SessionCreationResult{}, err
		}
		if err := driver.ValidateEnvironment(agent, env); err != nil {
			return SessionCreationResult{}, err
		}
	}
	sandbox, err := s.BackendFactory(env.Sandbox.Backend)
	if err != nil {
		return SessionCreationResult{}, err
	}
	if err := sandbox.ValidateSpec(ctx, env.Sandbox); err != nil {
		return SessionCreationResult{}, err
	}
	probe, err := sandbox.Probe(ctx)
	if err != nil {
		return SessionCreationResult{}, err
	}
	if probe["available"] != true {
		return SessionCreationResult{}, fault.New(fault.CodeSandboxUnavailable, "configured sandbox is not running")
	}
	env.Sandbox.ImageDigest, err = sandbox.ImageIdentity(ctx, env.Sandbox.Image)
	if err != nil {
		return SessionCreationResult{}, err
	}
	sid := contracts.NewID("ses")
	ctx = observe.WithIDs(ctx, observe.IDs{SessionID: sid})
	root := filepath.Join(s.Store.Root, "sessions", sid)
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return SessionCreationResult{}, err
	}
	if err := gitstate.PrepareRepositories(ctx, session, workspace); err != nil {
		return SessionCreationResult{}, cleanupUnregisteredSession(ctx, root, err)
	}
	status, err := gitstate.WorkspaceStatus(ctx, workspace, session.Repositories)
	if err != nil {
		return SessionCreationResult{}, cleanupUnregisteredSession(ctx, root, err)
	}
	created, err := s.Store.CreateSession(ctx, store.CreateSessionInput{ID: sid, SpecJSON: contracts.JSONText(session), EnvironmentJSON: contracts.JSONText(env), EnvironmentDigest: contracts.Digest(env), Workspace: workspace, Repositories: status, ImageDigest: env.Sandbox.ImageDigest, IdempotencyKey: session.IdempotencyKey, CreationDigest: creationDigest,
		ParentSessionID: parent.ParentSessionID, ParentTaskID: parent.ParentTaskID, ParentInvocationID: parent.ParentInvocationID, DelegateAgentID: parent.DelegateAgentID,
		DelegationRequestDigest: parent.DelegationRequestDigest})
	if err != nil {
		return SessionCreationResult{}, cleanupUnregisteredSession(ctx, root, err)
	}
	if created.Replayed {
		if err := cleanupUnregisteredSession(ctx, root, nil); err != nil {
			return SessionCreationResult{}, err
		}
		return sessionCreationResult(created), nil
	}
	observe.Log.InfoContext(ctx, "session created", "sessionId", sid, "backend", env.Sandbox.Backend, "isolation", env.Sandbox.Isolation, "imageDigest", env.Sandbox.ImageDigest, "repositoryCount", len(session.Repositories))
	return sessionCreationResult(created), nil
}

func cleanupUnregisteredSession(ctx context.Context, root string, cause error) error {
	if err := os.RemoveAll(root); err != nil {
		combined := fault.Wrap(fault.CodeInternalError, "remove unregistered session workspace", errors.Join(err, cause), "session workspace cleanup failed")
		observe.LogFailure(ctx, "session workspace cleanup failed", combined)
		return combined
	}
	return cause
}
func sessionCreationResult(record store.SessionCreation) SessionCreationResult {
	return SessionCreationResult{SessionID: record.ID, Status: record.Status,
		Workspace: record.Workspace, Replayed: record.Replayed}
}

func parseSessionRow(row store.SessionRow) (contracts.SessionSpec, contracts.EnvironmentSpec, error) {
	var session contracts.SessionSpec
	var env contracts.EnvironmentSpec
	if err := json.Unmarshal([]byte(row.Spec), &session); err != nil {
		return session, env, err
	}
	if err := json.Unmarshal([]byte(row.Environment), &env); err != nil {
		return session, env, err
	}
	return session, env, nil
}
func (s *Service) AddTask(ctx context.Context, sid, path string) (TaskAdditionResult, error) {
	row, err := s.Store.Session(ctx, sid)
	if err != nil {
		return TaskAdditionResult{}, err
	}
	session, env, err := parseSessionRow(row)
	if err != nil {
		return TaskAdditionResult{}, err
	}
	work, err := contracts.ValidateWork(path, session, env)
	if err != nil {
		return TaskAdditionResult{}, err
	}
	return s.addTask(ctx, sid, work, path)
}

func (s *Service) addTask(ctx context.Context, sid string, work contracts.WorkSpec, sourcePath string) (TaskAdditionResult, error) {
	ctx = observe.WithIDs(ctx, observe.IDs{SessionID: sid})
	row, err := s.Store.Session(ctx, sid)
	if err != nil {
		return TaskAdditionResult{}, err
	}
	if lifecycle.TerminalSession(row.Status) {
		return TaskAdditionResult{}, fault.New(fault.CodeInvalidTransition, "terminal session cannot accept tasks")
	}
	_, env, err := parseSessionRow(row)
	if err != nil {
		return TaskAdditionResult{}, err
	}
	requestDigest := contracts.Digest(work)
	if existing, found, err := s.Store.TaskByKey(ctx, sid, work.IdempotencyKey); err != nil {
		return TaskAdditionResult{}, err
	} else if found {
		var saved contracts.WorkSpec
		if err := json.Unmarshal([]byte(existing.Spec), &saved); err != nil {
			return TaskAdditionResult{}, err
		}
		matches := saved.RequestDigest == requestDigest || saved.RequestDigest == "" && contracts.Digest(saved) == requestDigest
		if !matches {
			return TaskAdditionResult{}, fault.New(fault.CodeIdempotencyConflict, "key already used with different task")
		}
		return TaskAdditionResult{TaskID: existing.ID, Status: existing.Status, Replayed: true}, nil
	}
	work.RequestDigest = requestDigest
	work, err = inputs.Stage(ctx, work, sourcePath, filepath.Dir(row.Workspace), env)
	if err != nil {
		return TaskAdditionResult{}, err
	}
	fingerprint := contracts.Digest(work)
	created, err := s.Store.AddTask(ctx, store.AddTaskInput{SessionID: sid, Key: work.IdempotencyKey, SpecJSON: contracts.JSONText(work), Digest: fingerprint})
	if err != nil {
		return TaskAdditionResult{}, err
	}
	observe.Log.InfoContext(ctx, "task added", "sessionId", sid, "taskId", created.ID, "replayed", created.Replayed)
	return TaskAdditionResult{TaskID: created.ID, Status: created.Status, Replayed: created.Replayed}, nil
}

// CompleteSession ends execution after verified tasks have been reviewed.
func (s *Service) CompleteSession(ctx context.Context, sid string) (SessionCompletionResult, error) {
	return s.completeSession(ctx, sid, false)
}

func (s *Service) completeSession(ctx context.Context, sid string, handoffReady bool) (SessionCompletionResult, error) {
	ctx = observe.WithIDs(ctx, observe.IDs{SessionID: sid})
	row, err := s.Store.Session(ctx, sid)
	if err != nil {
		return SessionCompletionResult{}, err
	}
	if row.ParentSessionID.Present && !handoffReady && row.Status != lifecycle.Completed {
		return SessionCompletionResult{}, fault.New(fault.CodeInvalidTransition, "managed child requires a verified handoff before completion")
	}
	if row.Status != lifecycle.Ready && row.Status != lifecycle.Completed {
		return SessionCompletionResult{}, fault.New(fault.CodeInvalidTransition, "session must be ready before completion")
	}
	if row.Status != lifecycle.Completed {
		session, _, err := parseSessionRow(row)
		if err != nil {
			return SessionCompletionResult{}, err
		}
		if err := s.verifyCheckpointHead(ctx, sid, session, row.Workspace); err != nil {
			return SessionCompletionResult{}, err
		}
	}
	completedAt, err := s.Store.CompleteSession(ctx, sid)
	if err != nil {
		return SessionCompletionResult{}, err
	}
	tasks, err := s.Store.Tasks(ctx, sid)
	if err != nil {
		return SessionCompletionResult{}, err
	}
	deliveries, err := s.sessionDeliveries(ctx, tasks, filepath.Dir(row.Workspace))
	if err != nil {
		return SessionCompletionResult{}, err
	}
	observe.Log.InfoContext(ctx, "session completed", "sessionId", sid, "completedAt", completedAt)
	return SessionCompletionResult{SchemaVersion: contracts.SessionCompletionVersion, SessionID: sid, Status: lifecycle.Completed, CompletedAt: completedAt, Deliveries: deliveries}, nil
}

// CancelSession ends an abandoned requirement without claiming delivery.
func (s *Service) CancelSession(ctx context.Context, sid, reason string) (SessionCancellationResult, error) {
	ctx = observe.WithIDs(ctx, observe.IDs{SessionID: sid})
	cancelledAt, savedReason, err := s.Store.CancelSession(ctx, sid, reason)
	if err != nil {
		return SessionCancellationResult{}, err
	}
	observe.Log.InfoContext(ctx, "session cancelled", "sessionId", sid, "cancelledAt", cancelledAt)
	return SessionCancellationResult{SchemaVersion: contracts.SessionCancellationVersion, SessionID: sid, Status: lifecycle.Cancelled, CancelledAt: cancelledAt, Reason: savedReason}, nil
}
