package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/lifecycle"
	"github.com/Covalane/turnyard/internal/store/ent"
)

func TestHandoffFailurePausesChildForAgentRetry(t *testing.T) {
	ctx := context.Background()
	s, err := OpenStore(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	const sessionID = "ses_handoff"
	const candidateID = "cand_handoff"
	if _, err := s.CreateSession(ctx, CreateSessionInput{ID: sessionID, SpecJSON: "{}", EnvironmentJSON: "{}",
		EnvironmentDigest: "digest", Workspace: filepath.Join(s.Root, "sessions", sessionID, "workspace")}); err != nil {
		t.Fatal(err)
	}
	task, err := s.AddTask(ctx, AddTaskInput{SessionID: sessionID, Key: "child", SpecJSON: "{}", Digest: "digest"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.write(ctx, func(tx *ent.Tx) error {
		if err := tx.Candidate.Create().SetID(candidateID).SetTaskID(task.ID).SetVector("{}").SetDigest("candidate-digest").
			SetChecks("[]").SetDeliverables("[]").SetStatus(lifecycle.Verified).SetCreatedAt(now()).Exec(ctx); err != nil {
			return err
		}
		return tx.Task.UpdateOneID(task.ID).SetCandidateID(candidateID).SetStatus(lifecycle.Verified).Exec(ctx)
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.FailDelegationHandoff(ctx, sessionID, task.ID, candidateID); err != nil {
		t.Fatal(err)
	}
	row, err := s.Task(ctx, task.ID)
	if err != nil || row.Status != lifecycle.Failed || row.ErrorCode.Value != string(fault.CodeHandoffUnavailable) {
		t.Fatalf("handoff failure not persisted: %+v, %v", row, err)
	}
	session, err := s.Session(ctx, sessionID)
	if err != nil || session.Status != lifecycle.Paused {
		t.Fatalf("child session not paused: %+v, %v", session, err)
	}
	candidate, err := s.Candidate(ctx, candidateID)
	if err != nil || candidate.Status != lifecycle.Failed {
		t.Fatalf("candidate was not invalidated: %+v, %v", candidate, err)
	}
	if err := s.StartReverify(ctx, sessionID, task.ID, candidateID, "candidate-digest"); fault.CodeOf(err) != fault.CodeInvalidTransition {
		t.Fatalf("same candidate bypassed handoff failure: %v", err)
	}
}
