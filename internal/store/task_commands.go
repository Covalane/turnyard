package store

import (
	"context"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/lifecycle"
	"github.com/Covalane/turnyard/internal/store/ent"
	"github.com/Covalane/turnyard/internal/store/ent/task"
)

type AddTaskInput struct{ SessionID, Key, SpecJSON, Digest string }
type AddTaskResult struct {
	ID, Status string
	Replayed   bool
}

func (s *Store) AddTask(ctx context.Context, in AddTaskInput) (AddTaskResult, error) {
	out := AddTaskResult{}
	err := s.write(ctx, func(tx *ent.Tx) error {
		sessionRow, err := tx.Session.Get(ctx, in.SessionID)
		if err != nil {
			return notFound(err, "session")
		}
		if lifecycle.TerminalSession(sessionRow.Status) {
			return fault.New(fault.CodeInvalidTransition, "terminal session cannot accept tasks")
		}
		existing, err := tx.Task.Query().Where(task.SessionIDEQ(in.SessionID), task.IdempotencyKeyEQ(in.Key)).Only(ctx)
		if err == nil {
			if existing.SpecDigest != in.Digest {
				return fault.New(fault.CodeIdempotencyConflict, "key already used with different task")
			}
			out = AddTaskResult{existing.ID, existing.Status, true}
			return nil
		}
		if !ent.IsNotFound(err) {
			return err
		}
		busy, err := tx.Task.Query().Where(task.SessionIDEQ(in.SessionID), task.StatusNEQ(lifecycle.Verified), task.StatusNEQ(lifecycle.Cancelled)).Exist(ctx)
		if err != nil {
			return err
		}
		if busy {
			return fault.New(fault.CodeSessionBusy, "finish or resolve the current task before adding another")
		}
		count, err := tx.Task.Query().Where(task.SessionIDEQ(in.SessionID)).Count(ctx)
		if err != nil {
			return err
		}
		id, err := contracts.NewID("work")
		if err != nil {
			return err
		}
		if err := tx.Task.Create().SetID(id).SetSessionID(in.SessionID).SetSequence(count + 1).
			SetIdempotencyKey(in.Key).SetSpec(in.SpecJSON).SetSpecDigest(in.Digest).
			SetStatus(lifecycle.Queued).SetCreatedAt(now()).Exec(ctx); err != nil {
			return err
		}
		if err := addEvent(ctx, tx, in.SessionID, id, lifecycle.EventTaskAdded, map[string]any{"sequence": count + 1, "spec_digest": in.Digest}); err != nil {
			return err
		}
		out = AddTaskResult{id, lifecycle.Queued, false}
		return nil
	})
	return out, err
}
