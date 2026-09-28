package store

import (
	"context"

	"github.com/Covalane/turnyard/internal/lifecycle"
	"github.com/Covalane/turnyard/internal/store/ent"
)

type CheckpointRecord struct{ ID, SessionID, TaskID, Archive, Metadata, SHA256 string }

func (s *Store) SaveCheckpoint(ctx context.Context, in CheckpointRecord) error {
	return s.write(ctx, func(tx *ent.Tx) error {
		create := tx.Checkpoint.Create().SetID(in.ID).SetSessionID(in.SessionID).
			SetArchivePath(in.Archive).SetMetadata(in.Metadata).SetCreatedAt(now())
		if in.TaskID != "" {
			create.SetTaskID(in.TaskID)
		}
		if err := create.Exec(ctx); err != nil {
			return err
		}
		return addEvent(ctx, tx, in.SessionID, in.TaskID, lifecycle.EventCheckpointSaved, map[string]any{"checkpoint_id": in.ID, "sha256": in.SHA256})
	})
}
