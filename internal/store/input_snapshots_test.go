package store

import (
	"context"
	"testing"

	"github.com/Covalane/turnyard/internal/contracts"
)

func TestReferencedInputBatchesReadsPersistedTasks(t *testing.T) {
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
	batch := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	work := contracts.WorkSpec{Inputs: []contracts.InputSpec{{ID: "image", Source: contracts.InputSource{
		Kind: contracts.InputStaged, Path: batch + "/image",
	}}}}
	workJSON, err := contracts.JSONText(work)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTask(ctx, AddTaskInput{SessionID: "ses_test", Key: "work", SpecJSON: workJSON, Digest: "digest"}); err != nil {
		t.Fatal(err)
	}
	refs, err := s.ReferencedInputBatches(ctx)
	if err != nil || !refs.Has("ses_test/"+batch) || len(refs) != 1 {
		t.Fatalf("persisted input references: %v %v", refs, err)
	}
}
