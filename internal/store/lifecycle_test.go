package store

import (
	"context"
	"testing"

	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/lifecycle"
)

func TestPreflightFailureCannotReopenCancelledSession(t *testing.T) {
	ctx := context.Background()
	s, err := OpenStore(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.CreateSession(ctx, CreateSessionInput{ID: "ses_test", SpecJSON: "{}", EnvironmentJSON: "{}",
		EnvironmentDigest: "digest", Workspace: "workspace"}); err != nil {
		t.Fatal(err)
	}
	added, err := s.AddTask(ctx, AddTaskInput{SessionID: "ses_test", Key: "work", SpecJSON: "{}", Digest: "digest"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CancelSession(ctx, "ses_test", "withdrawn"); err != nil {
		t.Fatal(err)
	}
	err = s.FailPreflight(ctx, added.ID, "ses_test", lifecycle.Queued, lifecycle.Failed, fault.CodeWorkspaceDrift)
	if fault.CodeOf(err) != fault.CodeInvalidTransition {
		t.Fatalf("late preflight changed terminal session: %v", err)
	}
	session, err := s.Session(ctx, "ses_test")
	if err != nil || session.Status != lifecycle.Cancelled {
		t.Fatalf("session was reopened: %+v %v", session, err)
	}
	task, err := s.Task(ctx, added.ID)
	if err != nil || task.Status != lifecycle.Cancelled {
		t.Fatalf("task was reopened: %+v %v", task, err)
	}
}
