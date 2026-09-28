package store

import (
	"context"
	"errors"

	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/store/ent"
)

// write owns the transaction boundary. Callers never receive an Ent client or transaction.
func (s *Store) write(ctx context.Context, fn func(*ent.Tx) error) error {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fault.Wrap(fault.CodeInternalError, "begin store transaction", err, "cannot begin state transaction")
	}
	if err := fn(tx); err != nil {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			return fault.Wrap(fault.CodeInternalError, "rollback store transaction", errors.Join(rollbackErr, err), "state transaction rollback failed")
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fault.Wrap(fault.CodeInternalError, "commit store transaction", err, "state transaction commit failed")
	}
	return nil
}

func addEvent(ctx context.Context, tx *ent.Tx, sid, tid, typ string, payload any) error {
	encoded, err := jsonText(payload)
	if err != nil {
		return fault.Wrap(fault.CodeInternalError, "encode event", err, "event payload cannot be encoded")
	}
	create := tx.Event.Create().SetSessionID(sid).SetType(typ).SetPayload(encoded).SetCreatedAt(now())
	if tid != "" {
		create.SetTaskID(tid)
	}
	return create.Exec(ctx)
}
func (s *Store) AppendEvent(ctx context.Context, sid, tid, typ string, payload any) error {
	return s.write(ctx, func(tx *ent.Tx) error { return addEvent(ctx, tx, sid, tid, typ, payload) })
}
