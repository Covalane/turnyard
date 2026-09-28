package store

import (
	"context"
	"encoding/json"

	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/gitstate"
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

func optionalString(x *string) OptionalString {
	if x == nil {
		return OptionalString{}
	}
	return OptionalString{*x, true}
}
func optionalInt(x *int) OptionalInt {
	if x == nil {
		return OptionalInt{}
	}
	return OptionalInt{int64(*x), true}
}
func optionalFloat(x *float64) OptionalFloat {
	if x == nil {
		return OptionalFloat{}
	}
	return OptionalFloat{*x, true}
}
func notFound(err error, object string) error {
	if ent.IsNotFound(err) {
		return fault.Wrap(fault.CodeNotFound, "store query", err, "%s not found", object)
	}
	return err
}
func sessionRow(x *ent.Session) SessionRow {
	return SessionRow{ID: x.ID, Spec: x.Spec, Environment: x.Environment, EnvironmentDigest: x.EnvironmentDigest,
		Workspace: x.Workspace, Status: x.Status, NativeID: optionalString(x.NativeID), CreatedAt: x.CreatedAt,
		CompletedAt: optionalFloat(x.CompletedAt), CancelledAt: optionalFloat(x.CancelledAt),
		CancelReason: optionalString(x.CancelReason), ParentSessionID: optionalString(x.ParentSessionID),
		ParentTaskID: optionalString(x.ParentTaskID), ParentInvocationID: optionalString(x.ParentInvocationID),
		DelegateAgentID: optionalString(x.DelegateAgentID), DelegationRequestDigest: optionalString(x.DelegationRequestDigest)}
}

// DelegatedSessions returns child sessions linked to a parent task.
func (s *Store) DelegatedSessions(ctx context.Context, taskID string) ([]SessionRow, error) {
	items, err := s.client.Session.Query().Where(session.ParentTaskIDEQ(taskID)).Order(session.ByCreatedAt()).All(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]SessionRow, 0, len(items))
	for _, item := range items {
		result = append(result, sessionRow(item))
	}
	return result, nil
}
func taskRow(x *ent.Task) TaskRow {
	return TaskRow{x.ID, x.SessionID, x.Sequence, x.IdempotencyKey, x.Spec, x.SpecDigest, x.Status, optionalString(x.CandidateID), optionalString(x.ErrorCode), x.CreatedAt}
}
func candidateRow(x *ent.Candidate) CandidateRow {
	return CandidateRow{x.ID, x.TaskID, x.Vector, x.Digest, x.Checks, x.Deliverables, x.Status, x.CreatedAt}
}
func invocationRow(x *ent.Invocation) InvocationRow {
	return InvocationRow{x.ID, x.TurnID, x.Runtime, x.Model, x.SandboxBackend, x.Status, optionalInt(x.ExitCode), optionalString(x.NativeID), optionalString(x.LogPath), x.StartedAt, optionalFloat(x.EndedAt)}
}
func (s *Store) Session(ctx context.Context, id string) (SessionRow, error) {
	x, err := s.client.Session.Get(ctx, id)
	if err != nil {
		return SessionRow{}, notFound(err, "session")
	}
	return sessionRow(x), nil
}
func (s *Store) Task(ctx context.Context, id string) (TaskRow, error) {
	x, err := s.client.Task.Get(ctx, id)
	if err != nil {
		return TaskRow{}, notFound(err, "task")
	}
	return taskRow(x), nil
}
func (s *Store) TaskByKey(ctx context.Context, sessionID, key string) (TaskRow, bool, error) {
	x, err := s.client.Task.Query().Where(task.SessionIDEQ(sessionID), task.IdempotencyKeyEQ(key)).Only(ctx)
	if ent.IsNotFound(err) {
		return TaskRow{}, false, nil
	}
	if err != nil {
		return TaskRow{}, false, err
	}
	return taskRow(x), true, nil
}
func (s *Store) Tasks(ctx context.Context, sid string) ([]TaskRow, error) {
	xs, err := s.client.Task.Query().Where(task.SessionIDEQ(sid)).Order(task.BySequence()).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]TaskRow, 0, len(xs))
	for _, x := range xs {
		out = append(out, taskRow(x))
	}
	return out, nil
}
func (s *Store) Candidate(ctx context.Context, id string) (CandidateRow, error) {
	x, err := s.client.Candidate.Get(ctx, id)
	if err != nil {
		return CandidateRow{}, notFound(err, "candidate")
	}
	return candidateRow(x), nil
}
func (s *Store) Invocations(ctx context.Context, taskID string) ([]InvocationRow, error) {
	xs, err := s.client.Invocation.Query().Where(invocation.HasTurnWith(turn.TaskIDEQ(taskID))).Order(invocation.ByStartedAt()).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]InvocationRow, 0, len(xs))
	for _, x := range xs {
		out = append(out, invocationRow(x))
	}
	return out, nil
}
func (s *Store) Events(ctx context.Context, sid string, after int64) ([]EventRow, error) {
	xs, err := s.client.Event.Query().Where(event.SessionIDEQ(sid), event.IDGT(after)).Order(event.ByID()).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]EventRow, 0, len(xs))
	for _, x := range xs {
		out = append(out, EventRow{Sequence: x.ID, SessionID: x.SessionID, TaskID: x.TaskID, Type: x.Type, Payload: x.Payload, CreatedAt: x.CreatedAt})
	}
	return out, nil
}

// Task bases remain immutable across retries and human replies.
func (s *Store) SaveTaskBases(ctx context.Context, taskID string, bases map[string]string) error {
	return s.write(ctx, func(tx *ent.Tx) error {
		for id, base := range bases {
			if err := tx.TaskBase.Create().SetID(taskID + ":" + id).SetTaskID(taskID).SetRepoID(id).SetBaseCommit(base).Exec(ctx); err != nil {
				return err
			}
		}
		return nil
	})
}
func (s *Store) TaskBases(ctx context.Context, taskID string) (map[string]string, error) {
	xs, err := s.client.TaskBase.Query().Where(taskbase.TaskIDEQ(taskID)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(xs))
	for _, x := range xs {
		out[x.RepoID] = x.BaseCommit
	}
	return out, nil
}
func (s *Store) FirstCandidateBases(ctx context.Context, taskID string) (map[string]string, error) {
	x, err := s.client.Candidate.Query().Where(candidate.TaskIDEQ(taskID)).Order(candidate.ByCreatedAt()).First(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var vector map[string]gitstate.RepoVersion
	if err := json.Unmarshal([]byte(x.Vector), &vector); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(vector))
	for id, version := range vector {
		out[id] = version.Base
	}
	return out, nil
}

type CheckpointRow struct{ ID, SessionID, TaskID, Archive, Metadata string }

func (s *Store) LatestCheckpointMetadata(ctx context.Context, sid string) (string, bool, error) {
	x, err := s.client.Checkpoint.Query().Where(checkpoint.SessionIDEQ(sid)).Order(ent.Desc(checkpoint.FieldCreatedAt)).First(ctx)
	if ent.IsNotFound(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return x.Metadata, true, nil
}
func (s *Store) Checkpoint(ctx context.Context, id string) (CheckpointRow, error) {
	x, err := s.client.Checkpoint.Get(ctx, id)
	if err != nil {
		return CheckpointRow{}, notFound(err, "checkpoint")
	}
	row := CheckpointRow{ID: x.ID, SessionID: x.SessionID, Archive: x.ArchivePath, Metadata: x.Metadata}
	if x.TaskID != nil {
		row.TaskID = *x.TaskID
	}
	return row, nil
}
func (s *Store) Turns(ctx context.Context, taskID string) ([]TurnRow, error) {
	xs, err := s.client.Turn.Query().Where(turn.TaskIDEQ(taskID)).Order(turn.BySequence()).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]TurnRow, 0, len(xs))
	for _, x := range xs {
		invocationID := ""
		if x.InvocationID != nil {
			invocationID = *x.InvocationID
		}
		out = append(out, TurnRow{ID: x.ID, TaskID: x.TaskID, Sequence: x.Sequence, Input: x.Input, Status: x.Status, InvocationID: invocationID, CreatedAt: x.CreatedAt})
	}
	return out, nil
}
func (s *Store) RecoverInterrupted(ctx context.Context) ([]string, error) {
	ids := []string{}
	err := s.write(ctx, func(tx *ent.Tx) error {
		running, err := tx.Task.Query().Where(task.StatusEQ(lifecycle.Running)).All(ctx)
		if err != nil {
			return err
		}
		for _, x := range running {
			if _, err := tx.Turn.Update().Where(turn.TaskIDEQ(x.ID), turn.StatusEQ(lifecycle.Running)).SetStatus(lifecycle.Unknown).Save(ctx); err != nil {
				return err
			}
			if _, err := tx.Invocation.Update().Where(invocation.HasTurnWith(turn.TaskIDEQ(x.ID)), invocation.StatusIn(lifecycle.Starting, lifecycle.Running)).SetStatus(lifecycle.Unknown).SetEndedAt(now()).Save(ctx); err != nil {
				return err
			}
			if err := tx.Task.UpdateOneID(x.ID).SetStatus(lifecycle.Unknown).SetErrorCode(string(fault.CodeInterrupted)).Exec(ctx); err != nil {
				return err
			}
			if err := tx.Session.UpdateOneID(x.SessionID).SetStatus(lifecycle.Paused).Exec(ctx); err != nil {
				return err
			}
			if err := addEvent(ctx, tx, x.SessionID, x.ID, lifecycle.EventTaskRecoveredUnknown, map[string]any{"reason": "supervisor restarted during active invocation"}); err != nil {
				return err
			}
			ids = append(ids, x.ID)
		}
		return nil
	})
	return ids, err
}
