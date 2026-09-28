package store

import (
	"context"

	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/lifecycle"
	"github.com/Covalane/turnyard/internal/store/ent"
	"github.com/Covalane/turnyard/internal/store/ent/task"
)

func (s *Store) RecordCandidate(ctx context.Context, sessionID, taskID, candidateID, digest string, vector, deliverables any) error {
	return s.write(ctx, func(tx *ent.Tx) error {
		encoded, err := jsonText(vector)
		if err != nil {
			return fault.Wrap(fault.CodeInternalError, "encode candidate", err, "candidate vector cannot be encoded")
		}
		outputs, err := jsonText(deliverables)
		if err != nil {
			return fault.Wrap(fault.CodeInternalError, "encode candidate", err, "candidate deliverables cannot be encoded")
		}
		if err := tx.Candidate.Create().SetID(candidateID).SetTaskID(taskID).SetVector(encoded).
			SetDigest(digest).SetChecks("[]").SetDeliverables(outputs).SetStatus(lifecycle.CandidateReady).SetCreatedAt(now()).Exec(ctx); err != nil {
			return err
		}
		if err := tx.Task.UpdateOneID(taskID).SetCandidateID(candidateID).Exec(ctx); err != nil {
			return err
		}
		return addEvent(ctx, tx, sessionID, taskID, lifecycle.EventCandidateRecorded, map[string]any{
			"candidateId": candidateID, "digest": digest, "vector": vector, "status": lifecycle.CandidateReady})
	})
}

type CandidateOutcome struct {
	SessionID, TaskID, CandidateID, TurnID, Status, CheckpointID string
	ErrorCode                                                    fault.Code
	Checks                                                       any
	Deliverables                                                 any
	Reverify                                                     bool
}

func (s *Store) FinishCandidate(ctx context.Context, in CandidateOutcome) error {
	return s.write(ctx, func(tx *ent.Tx) error {
		encoded, err := jsonText(in.Checks)
		if err != nil {
			return fault.Wrap(fault.CodeInternalError, "encode checks", err, "check results cannot be encoded")
		}
		updateCandidate := tx.Candidate.UpdateOneID(in.CandidateID).SetChecks(encoded).SetStatus(in.Status)
		if in.Deliverables != nil {
			artifacts, err := jsonText(in.Deliverables)
			if err != nil {
				return fault.Wrap(fault.CodeInternalError, "encode deliverables", err, "deliverable results cannot be encoded")
			}
			updateCandidate.SetDeliverables(artifacts)
		}
		if err := updateCandidate.Exec(ctx); err != nil {
			return err
		}
		update := tx.Task.UpdateOneID(in.TaskID).SetStatus(in.Status)
		if in.ErrorCode == "" {
			update.ClearErrorCode()
		} else {
			update.SetErrorCode(string(in.ErrorCode))
		}
		if err := update.Exec(ctx); err != nil {
			return err
		}
		if !in.Reverify {
			if err := tx.Turn.UpdateOneID(in.TurnID).SetStatus(lifecycle.Completed).Exec(ctx); err != nil {
				return err
			}
		}
		sessionStatus := lifecycle.Paused
		if in.Status == lifecycle.Verified {
			sessionStatus = lifecycle.Ready
		}
		if err := tx.Session.UpdateOneID(in.SessionID).SetStatus(sessionStatus).Exec(ctx); err != nil {
			return err
		}
		typ := lifecycle.EventCandidateFailed
		if in.Status == lifecycle.Verified {
			typ = lifecycle.EventCandidateVerified
		} else if in.Status == lifecycle.NeedsInput {
			typ = lifecycle.EventCandidateNeedsInput
		}
		if in.Reverify {
			typ = lifecycle.EventCandidateReverifyEnd
		}
		if err := addEvent(ctx, tx, in.SessionID, in.TaskID, typ, map[string]any{
			"candidateId": in.CandidateID, "status": in.Status, "checks": in.Checks, "deliverables": in.Deliverables}); err != nil {
			return err
		}
		if !in.Reverify {
			return addEvent(ctx, tx, in.SessionID, in.TaskID, lifecycle.EventTaskState, map[string]any{
				"status": in.Status, "checkpointId": in.CheckpointID})
		}
		return nil
	})
}
func (s *Store) StartReverify(ctx context.Context, sessionID, taskID, candidateID, digest string) error {
	return s.write(ctx, func(tx *ent.Tx) error {
		current, err := tx.Session.Get(ctx, sessionID)
		if err != nil {
			return notFound(err, "session")
		}
		if lifecycle.TerminalSession(current.Status) {
			return fault.New(fault.CodeInvalidTransition, "terminal session cannot verify tasks")
		}
		taskRow, err := tx.Task.Get(ctx, taskID)
		if err != nil || taskRow.SessionID != sessionID || taskRow.ErrorCode != nil && *taskRow.ErrorCode == string(fault.CodeHandoffUnavailable) {
			return fault.New(fault.CodeInvalidTransition, "task is unavailable for candidate re-verification")
		}
		candidate, err := tx.Candidate.Get(ctx, candidateID)
		if err != nil || candidate.TaskID != taskID || candidate.Digest != digest {
			return fault.New(fault.CodeCandidateStale, "candidate changed before re-verification")
		}
		busy, err := tx.Task.Query().Where(task.SessionIDEQ(sessionID), task.StatusEQ(lifecycle.Running)).Exist(ctx)
		if err != nil {
			return err
		}
		if busy {
			return fault.New(fault.CodeConcurrentRun, "session already has a running task")
		}
		updated, err := tx.Task.Update().Where(task.IDEQ(taskID), task.SessionIDEQ(sessionID),
			task.StatusEQ(lifecycle.Failed), task.CandidateIDEQ(candidateID)).SetStatus(lifecycle.Running).Save(ctx)
		if err != nil {
			return err
		}
		if updated != 1 {
			return fault.New(fault.CodeInvalidTransition, "task changed before re-verification")
		}
		return addEvent(ctx, tx, sessionID, taskID, lifecycle.EventCandidateReverifyStart, map[string]any{"candidateId": candidateID, "digest": digest})
	})
}

// FailDelegationHandoff keeps a verified child repairable when its candidate
// cannot be exported to the parent. A later explicit retry creates a new
// candidate in the same child task and native session.
func (s *Store) FailDelegationHandoff(ctx context.Context, sessionID, taskID, candidateID string) error {
	return s.write(ctx, func(tx *ent.Tx) error {
		sessionRow, err := tx.Session.Get(ctx, sessionID)
		if err != nil || lifecycle.TerminalSession(sessionRow.Status) {
			return fault.New(fault.CodeInvalidTransition, "delegated session is terminal or missing")
		}
		current, err := tx.Task.Get(ctx, taskID)
		if err != nil || current.SessionID != sessionID || current.Status != lifecycle.Verified ||
			current.CandidateID == nil || *current.CandidateID != candidateID {
			return fault.New(fault.CodeInvalidTransition, "delegated candidate changed before handoff")
		}
		if err := tx.Candidate.UpdateOneID(candidateID).SetStatus(lifecycle.Failed).Exec(ctx); err != nil {
			return err
		}
		if err := tx.Task.UpdateOneID(taskID).SetStatus(lifecycle.Failed).
			SetErrorCode(string(fault.CodeHandoffUnavailable)).Exec(ctx); err != nil {
			return err
		}
		if err := tx.Session.UpdateOneID(sessionID).SetStatus(lifecycle.Paused).Exec(ctx); err != nil {
			return err
		}
		return addEvent(ctx, tx, sessionID, taskID, lifecycle.EventCandidateFailed,
			map[string]any{"candidateId": candidateID, "code": fault.CodeHandoffUnavailable, "phase": lifecycle.PhaseDelegationHandoff})
	})
}
func (s *Store) ErrorReverify(ctx context.Context, sessionID, taskID, candidateID string, cause error) error {
	return s.write(ctx, func(tx *ent.Tx) error {
		if err := tx.Task.UpdateOneID(taskID).SetStatus(lifecycle.Unknown).SetErrorCode(string(fault.CodeCheckError)).Exec(ctx); err != nil {
			return err
		}
		return addEvent(ctx, tx, sessionID, taskID, lifecycle.EventCandidateReverifyError, map[string]any{"candidateId": candidateID, "message": cause.Error()})
	})
}
func (s *Store) ReconcileUnknown(ctx context.Context, sessionID, taskID, checkpointID, priorCode string) error {
	return s.write(ctx, func(tx *ent.Tx) error {
		n, err := tx.Task.Update().Where(task.IDEQ(taskID), task.StatusEQ(lifecycle.Unknown)).SetStatus(lifecycle.Failed).
			SetErrorCode(string(fault.CodeReconciledUnknown)).Save(ctx)
		if err != nil {
			return err
		}
		if n != 1 {
			return fault.New(fault.CodeInvalidTransition, "task is no longer unknown")
		}
		return addEvent(ctx, tx, sessionID, taskID, lifecycle.EventTaskUnknownReconciled, map[string]any{"checkpointId": checkpointID, "previousErrorCode": priorCode})
	})
}
