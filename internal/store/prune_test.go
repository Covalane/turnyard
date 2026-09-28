package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Covalane/turnyard/internal/fault"
)

func TestPruneRequiresTerminalSessionAndRemovesMetadata(t *testing.T) {
	ctx := context.Background()
	s, err := OpenStore(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sid := "ses_prune"
	workspace := filepath.Join(s.Root, "sessions", sid, "workspace")
	if _, err := s.CreateSession(ctx, CreateSessionInput{ID: sid, SpecJSON: "{}", EnvironmentJSON: "{}",
		EnvironmentDigest: "digest", Workspace: workspace}); err != nil {
		t.Fatal(err)
	}
	task, err := s.AddTask(ctx, AddTaskInput{SessionID: sid, Key: "task", SpecJSON: "{}", Digest: "digest"})
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(s.Root, "checkpoints", "cp_prune.tar.gz")
	if err := s.SaveCheckpoint(ctx, CheckpointRecord{ID: "cp_prune", SessionID: sid, TaskID: task.ID, Archive: archive, Metadata: "{}", SHA256: "digest"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PruneTerminalSession(ctx, sid); fault.CodeOf(err) != fault.CodeInvalidTransition {
		t.Fatalf("active session was pruned: %v", err)
	}
	if _, _, err := s.CancelSession(ctx, sid, "finished without delivery"); err != nil {
		t.Fatal(err)
	}
	result, err := s.PruneTerminalSession(ctx, sid)
	if err != nil {
		t.Fatal(err)
	}
	if result.SessionRoot != filepath.Dir(workspace) || len(result.Checkpoints) != 1 || result.Checkpoints[0] != archive {
		t.Fatalf("unexpected artifacts: %+v", result)
	}
	if _, err := s.Session(ctx, sid); fault.CodeOf(err) != fault.CodeNotFound {
		t.Fatalf("pruned session still exists: %v", err)
	}
	if _, err := s.Task(ctx, task.ID); fault.CodeOf(err) != fault.CodeNotFound {
		t.Fatalf("pruned task still exists: %v", err)
	}
	if _, err := s.Checkpoint(ctx, "cp_prune"); fault.CodeOf(err) != fault.CodeNotFound {
		t.Fatalf("pruned checkpoint still exists: %v", err)
	}
}
