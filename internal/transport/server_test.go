package transport

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/store"
)

func TestSupervisorPrunesOrphanedInputBatchOnStartup(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	state, err := store.OpenStore(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.CreateSession(ctx, store.CreateSessionInput{ID: "ses_kept", SpecJSON: "{}",
		EnvironmentJSON: "{}", EnvironmentDigest: "digest", Workspace: filepath.Join(root, "sessions", "ses_kept", "workspace")}); err != nil {
		t.Fatal(err)
	}
	committedID := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	work := contracts.WorkSpec{Inputs: []contracts.InputSpec{{ID: "image", Source: contracts.InputSource{
		Kind: contracts.InputStaged, Path: committedID + "/image",
	}}}}
	if _, err := state.AddTask(ctx, store.AddTaskInput{SessionID: "ses_kept", Key: "work", SpecJSON: contracts.JSONText(work), Digest: "digest"}); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	committed := filepath.Join(root, "sessions", "ses_kept", "inputs", committedID)
	if err := os.MkdirAll(committed, 0o700); err != nil {
		t.Fatal(err)
	}
	batch := filepath.Join(root, "sessions", "ses_crashed", "inputs",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err := os.MkdirAll(batch, 0o700); err != nil {
		t.Fatal(err)
	}
	s, err := NewSupervisor(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Service.Close()
	if _, err := os.Stat(batch); !os.IsNotExist(err) {
		t.Fatalf("orphaned batch remains after restart: %v", err)
	}
	if _, err := os.Stat(committed); err != nil {
		t.Fatalf("committed batch was removed: %v", err)
	}
}

func TestSupervisorStopRejectsLaterRequests(t *testing.T) {
	s, err := NewSupervisor(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Service.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.listener = listener
	defer listener.Close()
	// Dispatch marks the stop before the listener closes.
	if _, err := s.Dispatch(context.Background(), Request{Action: actionDaemonStop}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Dispatch(context.Background(), Request{Action: actionSessionCreate}); fault.CodeOf(err) != fault.CodeDaemonUnavailable {
		t.Fatalf("request admitted after stop: %v", err)
	}
}
