package store

import (
	"context"
	"strings"

	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/lifecycle"
	"github.com/Covalane/turnyard/internal/store/ent"
	"github.com/Covalane/turnyard/internal/store/ent/session"
	"github.com/Covalane/turnyard/internal/store/ent/task"
)

type CreateSessionInput struct {
	ID, SpecJSON, EnvironmentJSON, EnvironmentDigest, Workspace, ImageDigest string
	IdempotencyKey, CreationDigest                                           string
	ParentSessionID, ParentTaskID, ParentInvocationID, DelegateAgentID       string
	DelegationRequestDigest                                                  string
	Repositories                                                             any
}

// SessionCreation is the persisted answer to an idempotent create request.
type SessionCreation struct {
	ID, Status, Workspace, Digest string
	Replayed                      bool
}

// SessionByCreationKey finds a prior creation even if its response was lost.
func (s *Store) SessionByCreationKey(ctx context.Context, key string) (SessionCreation, bool, error) {
	row, err := s.client.Session.Query().Where(session.IdempotencyKeyEQ(key)).Only(ctx)
	if ent.IsNotFound(err) {
		return SessionCreation{}, false, nil
	}
	if err != nil {
		return SessionCreation{}, false, err
	}
	if row.CreationDigest == nil {
		return SessionCreation{}, false, fault.New(fault.CodeSessionCorrupt, "session creation digest is missing")
	}
	return SessionCreation{ID: row.ID, Status: row.Status, Workspace: row.Workspace, Digest: *row.CreationDigest, Replayed: true}, true, nil
}

func (s *Store) CreateSession(ctx context.Context, in CreateSessionInput) (SessionCreation, error) {
	err := s.write(ctx, func(tx *ent.Tx) error {
		create := tx.Session.Create().SetID(in.ID).SetSpec(in.SpecJSON).SetEnvironment(in.EnvironmentJSON).
			SetEnvironmentDigest(in.EnvironmentDigest).SetWorkspace(in.Workspace).SetStatus(lifecycle.Ready).SetCreatedAt(now())
		if in.IdempotencyKey != "" {
			create.SetIdempotencyKey(in.IdempotencyKey).SetCreationDigest(in.CreationDigest)
		}
		if in.ParentSessionID != "" {
			create.SetParentSessionID(in.ParentSessionID).SetParentTaskID(in.ParentTaskID).
				SetParentInvocationID(in.ParentInvocationID).SetDelegateAgentID(in.DelegateAgentID).
				SetDelegationRequestDigest(in.DelegationRequestDigest)
		}
		if err := create.Exec(ctx); err != nil {
			return err
		}
		return addEvent(ctx, tx, in.ID, "", lifecycle.EventSessionCreated, map[string]any{
			"repositories": in.Repositories, "environment_digest": in.EnvironmentDigest, "image_digest": in.ImageDigest})
	})
	if err == nil {
		return SessionCreation{ID: in.ID, Status: lifecycle.Ready, Workspace: in.Workspace, Digest: in.CreationDigest}, nil
	}
	if !ent.IsConstraintError(err) || in.IdempotencyKey == "" {
		return SessionCreation{}, err
	}
	prior, found, lookupErr := s.SessionByCreationKey(ctx, in.IdempotencyKey)
	if lookupErr != nil {
		return SessionCreation{}, lookupErr
	}
	if !found {
		return SessionCreation{}, err
	}
	if prior.Digest != in.CreationDigest {
		return SessionCreation{}, fault.New(fault.CodeIdempotencyConflict, "session idempotency key already names different input")
	}
	return prior, nil
}

// CompleteSession freezes a requirement after every task has a verified
// candidate. Its rows remain available for review and later retention policy.
func (s *Store) CompleteSession(ctx context.Context, sid string) (float64, error) {
	completedAt := 0.0
	err := s.write(ctx, func(tx *ent.Tx) error {
		current, err := tx.Session.Get(ctx, sid)
		if err != nil {
			return notFound(err, "session")
		}
		if current.Status == lifecycle.Completed {
			if current.CompletedAt == nil {
				return fault.New(fault.CodeSessionCorrupt, "completed session has no completion time")
			}
			completedAt = *current.CompletedAt
			return nil
		}
		if current.Status != lifecycle.Ready {
			return fault.New(fault.CodeInvalidTransition, "session must be ready before completion")
		}
		tasks, err := tx.Task.Query().Where(task.SessionIDEQ(sid)).Order(task.BySequence()).All(ctx)
		if err != nil {
			return err
		}
		if len(tasks) == 0 {
			return fault.New(fault.CodeInvalidTransition, "session has no delivered tasks")
		}
		ids := make([]string, 0, len(tasks))
		for _, item := range tasks {
			if item.Status != lifecycle.Verified || item.CandidateID == nil {
				return fault.New(fault.CodeInvalidTransition, "session has an unfinished task %s", item.ID)
			}
			candidate, err := tx.Candidate.Get(ctx, *item.CandidateID)
			if err != nil || candidate.TaskID != item.ID || candidate.Status != lifecycle.Verified {
				return fault.New(fault.CodeCandidateCorrupt, "task %s has no verified candidate", item.ID)
			}
			ids = append(ids, item.ID)
		}
		children, err := tx.Session.Query().Where(session.ParentSessionIDEQ(sid)).All(ctx)
		if err != nil {
			return err
		}
		for _, child := range children {
			if child.Status != lifecycle.Completed {
				return fault.New(fault.CodeInvalidTransition, "delegated session %s is not completed", child.ID)
			}
		}
		completedAt = now()
		if err := tx.Session.UpdateOneID(sid).SetStatus(lifecycle.Completed).SetCompletedAt(completedAt).Exec(ctx); err != nil {
			return err
		}
		return addEvent(ctx, tx, sid, "", lifecycle.EventSessionCompleted, map[string]any{"task_ids": ids, "completed_at": completedAt})
	})
	return completedAt, err
}

// CancelSession ends an abandoned requirement without treating its unfinished
// tasks as delivered. The original candidates and events remain available.
func (s *Store) CancelSession(ctx context.Context, sid, reason string) (float64, string, error) {
	if strings.TrimSpace(reason) == "" {
		return 0, "", fault.New(fault.CodeInvalidRequest, "cancellation requires a reason")
	}
	cancelledAt := 0.0
	savedReason := reason
	err := s.write(ctx, func(tx *ent.Tx) error {
		current, err := tx.Session.Get(ctx, sid)
		if err != nil {
			return notFound(err, "session")
		}
		if current.Status == lifecycle.Cancelled {
			if current.CancelledAt == nil || current.CancelReason == nil {
				return fault.New(fault.CodeSessionCorrupt, "cancelled session has incomplete terminal metadata")
			}
			cancelledAt, savedReason = *current.CancelledAt, *current.CancelReason
			return nil
		}
		if current.Status != lifecycle.Ready && current.Status != lifecycle.Paused {
			return fault.New(fault.CodeInvalidTransition, "session in %s state cannot be cancelled", current.Status)
		}
		tasks, err := tx.Task.Query().Where(task.SessionIDEQ(sid)).All(ctx)
		if err != nil {
			return err
		}
		children, err := tx.Session.Query().Where(session.ParentSessionIDEQ(sid)).All(ctx)
		if err != nil {
			return err
		}
		for _, child := range children {
			if !lifecycle.TerminalSession(child.Status) {
				return fault.New(fault.CodeSessionBusy, "delegated session %s must finish or be cancelled first", child.ID)
			}
		}
		for _, item := range tasks {
			if item.Status == lifecycle.Running {
				return fault.New(fault.CodeSessionBusy, "session has a running task")
			}
			if item.Status == lifecycle.Unknown {
				return fault.New(fault.CodeInvalidTransition, "reconcile unknown task %s before cancellation", item.ID)
			}
			if item.Status != lifecycle.Verified && item.Status != lifecycle.Cancelled {
				if err := tx.Task.UpdateOneID(item.ID).SetStatus(lifecycle.Cancelled).Exec(ctx); err != nil {
					return err
				}
			}
		}
		cancelledAt = now()
		if err := tx.Session.UpdateOneID(sid).SetStatus(lifecycle.Cancelled).SetCancelledAt(cancelledAt).
			SetCancelReason(reason).Exec(ctx); err != nil {
			return err
		}
		return addEvent(ctx, tx, sid, "", lifecycle.EventSessionCancelled, map[string]any{
			"reason": reason, "cancelled_at": cancelledAt})
	})
	return cancelledAt, savedReason, err
}
