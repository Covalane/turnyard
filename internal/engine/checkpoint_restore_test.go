package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/gitstate"
)

func TestRestoreRejectsHistoricalCheckpointBeforeExtraction(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	row, err := f.service.Store.Session(ctx, f.sid)
	if err != nil {
		t.Fatal(err)
	}
	session, _, err := parseSessionRow(row)
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(filepath.Dir(row.Workspace), gitstate.AgentStateDirName)
	first, err := f.service.saveCheckpoint(ctx, f.sid, "", session, row.Workspace, state, "")
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(row.Workspace, "api", "latest.txt")
	if err := os.WriteFile(marker, []byte("latest"), 0600); err != nil {
		t.Fatal(err)
	}
	latest, err := f.service.saveCheckpoint(ctx, f.sid, "", session, row.Workspace, state, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Restore(ctx, first); fault.CodeOf(err) != fault.CodeCheckpointNotCurrent {
		t.Fatalf("historical checkpoint did not fail before filesystem extraction: %v", err)
	}
	if body, err := os.ReadFile(marker); err != nil || string(body) != "latest" {
		t.Fatalf("old restore changed workspace: %q %v", body, err)
	}
	if err := os.RemoveAll(row.Workspace); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(state); err != nil {
		t.Fatal(err)
	}
	release, err := f.service.reserveSession(f.sid)
	if err != nil {
		t.Fatal(err)
	}
	_, busyErr := f.service.Restore(ctx, latest)
	release()
	if fault.CodeOf(busyErr) != fault.CodeConcurrentRun {
		t.Fatalf("restore ran concurrently: %v", busyErr)
	}
	result, err := f.service.Restore(ctx, latest)
	if err != nil || !result.Restored {
		t.Fatalf("latest restore: %+v %v", result, err)
	}
	if err := f.service.verifyCheckpointHead(ctx, f.sid, session, row.Workspace); err != nil {
		t.Fatalf("restored latest cannot resume: %v", err)
	}
	if body, err := os.ReadFile(marker); err != nil || string(body) != "latest" {
		t.Fatalf("latest content missing: %q %v", body, err)
	}
}
