package store

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/lifecycle"
	"github.com/Covalane/turnyard/internal/store/ent"
	"github.com/Covalane/turnyard/internal/store/ent/candidate"
	"github.com/Covalane/turnyard/internal/store/ent/checkpoint"
	"github.com/Covalane/turnyard/internal/store/ent/event"
	"github.com/Covalane/turnyard/internal/store/ent/invocation"
	"github.com/Covalane/turnyard/internal/store/ent/session"
	"github.com/Covalane/turnyard/internal/store/ent/task"
	"github.com/Covalane/turnyard/internal/store/ent/taskbase"
	"github.com/Covalane/turnyard/internal/store/ent/turn"
)

// PrunedSession lists file artifacts to remove after the database commit.
// Callers must hold the offline state lock and verify an up-to-date backup.
type PrunedSession struct {
	SessionRoot string
	Checkpoints []string
}

func (s *Store) PruneTerminalSession(ctx context.Context, sid string) (PrunedSession, error) {
	result := PrunedSession{SessionRoot: filepath.Join(s.Root, "sessions", sid)}
	if filepath.Base(result.SessionRoot) != sid || sid == "." || sid == ".." {
		return PrunedSession{}, fault.New(fault.CodeInvalidRequest, "invalid session ID")
	}
	err := s.write(ctx, func(tx *ent.Tx) error {
		row, err := tx.Session.Query().Where(session.IDEQ(sid)).Only(ctx)
		if err != nil {
			return notFound(err, "session")
		}
		if !lifecycle.TerminalSession(row.Status) {
			return fault.New(fault.CodeInvalidTransition, "only completed or cancelled sessions can be pruned")
		}
		hasChildren, err := tx.Session.Query().Where(session.ParentSessionIDEQ(sid)).Exist(ctx)
		if err != nil {
			return err
		}
		if hasChildren {
			return fault.New(fault.CodeInvalidTransition, "prune delegated child sessions before their parent")
		}
		if filepath.Clean(row.Workspace) != filepath.Join(result.SessionRoot, "workspace") {
			return fault.New(fault.CodeSessionCorrupt, "session workspace is outside its state directory")
		}
		items, err := tx.Checkpoint.Query().Where(checkpoint.SessionIDEQ(sid)).All(ctx)
		if err != nil {
			return err
		}
		for _, item := range items {
			if filepath.Dir(filepath.Clean(item.ArchivePath)) != filepath.Join(s.Root, "checkpoints") {
				return fault.New(fault.CodeSessionCorrupt, "checkpoint archive is outside the state directory")
			}
			result.Checkpoints = append(result.Checkpoints, item.ArchivePath)
		}
		tasks, err := tx.Task.Query().Where(task.SessionIDEQ(sid)).All(ctx)
		if err != nil {
			return err
		}
		ids := make([]string, 0, len(tasks))
		for _, item := range tasks {
			ids = append(ids, item.ID)
		}
		if len(ids) > 0 {
			if _, err := tx.Invocation.Delete().Where(invocation.HasTurnWith(turn.TaskIDIn(ids...))).Exec(ctx); err != nil {
				return err
			}
			if _, err := tx.Candidate.Delete().Where(candidate.TaskIDIn(ids...)).Exec(ctx); err != nil {
				return err
			}
			if _, err := tx.TaskBase.Delete().Where(taskbase.TaskIDIn(ids...)).Exec(ctx); err != nil {
				return err
			}
		}
		if _, err := tx.Event.Delete().Where(event.SessionIDEQ(sid)).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.Checkpoint.Delete().Where(checkpoint.SessionIDEQ(sid)).Exec(ctx); err != nil {
			return err
		}
		if len(ids) > 0 {
			if _, err := tx.Turn.Delete().Where(turn.TaskIDIn(ids...)).Exec(ctx); err != nil {
				return err
			}
		}
		if _, err := tx.Task.Delete().Where(task.SessionIDEQ(sid)).Exec(ctx); err != nil {
			return err
		}
		if err := tx.Session.DeleteOneID(sid).Exec(ctx); err != nil {
			return fmt.Errorf("prune session %s: %w", sid, err)
		}
		return nil
	})
	return result, err
}
