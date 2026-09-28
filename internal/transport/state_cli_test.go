package transport

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/store"
)

func TestOfflineBackupPruneAndRestore(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	home := filepath.Join(root, "state")
	database, err := store.OpenStore(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	sid := "ses_retention"
	workspace := filepath.Join(home, "sessions", sid, "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "result.txt"), []byte("result"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateSession(ctx, store.CreateSessionInput{ID: sid, SpecJSON: "{}", EnvironmentJSON: "{}", EnvironmentDigest: "digest", Workspace: workspace}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.CancelSession(ctx, sid, "done"); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(root, "backup")
	if _, err := runCLI(ctx, []string{"--home", home, "state", "backup", "--out", backup}); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLI(ctx, []string{"--home", home, "session", "prune", sid, "--backup", backup}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "sessions", sid)); !os.IsNotExist(err) {
		t.Fatalf("pruned session files remain: %v", err)
	}
	database, err = store.OpenStore(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Session(ctx, sid); fault.CodeOf(err) != fault.CodeNotFound {
		t.Fatalf("pruned session metadata remains: %v", err)
	}
	_ = database.Close()
	restored := filepath.Join(root, "restored")
	if _, err := runCLI(ctx, []string{"--home", home, "state", "restore", "--from", backup, "--to", restored}); err != nil {
		t.Fatal(err)
	}
	database, err = store.OpenStore(ctx, restored)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	row, err := database.Session(ctx, sid)
	if err != nil || row.Workspace != filepath.Join(restored, "sessions", sid, "workspace") {
		t.Fatalf("restored session: %+v %v", row, err)
	}
}
