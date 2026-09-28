package stateops

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Covalane/turnyard/internal/store"
)

func TestBackupVerifyRestoreAndDrift(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	home := filepath.Join(root, "state")
	if err := os.MkdirAll(filepath.Join(home, "sessions", "ses_1"), 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := store.OpenStore(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateSession(ctx, store.CreateSessionInput{ID: "ses_1", SpecJSON: "{}", EnvironmentJSON: "{}", EnvironmentDigest: "digest", Workspace: filepath.Join(home, "sessions", "ses_1", "workspace")}); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{"sessions/ses_1/result.txt": "candidate"} {
		if err := os.WriteFile(filepath.Join(home, path), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "sessions", "ses_1", "run.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("result.txt", filepath.Join(home, "sessions", "ses_1", "link")); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(root, "backup")
	manifest, err := Backup(ctx, home, backup)
	if err != nil || len(manifest.Entries) == 0 {
		t.Fatalf("backup: %+v %v", manifest, err)
	}
	if err := VerifyCurrent(ctx, home, backup); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(root, "restored")
	if err := Restore(ctx, backup, restored); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(filepath.Join(restored, "sessions", "ses_1", "result.txt")); err != nil || string(body) != "candidate" {
		t.Fatalf("restored result: %q %v", body, err)
	}
	if info, err := os.Stat(filepath.Join(restored, "sessions", "ses_1", "run.sh")); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("executable mode lost: %v %v", info, err)
	}
	reopened, err := store.OpenStore(ctx, restored)
	if err != nil {
		t.Fatal(err)
	}
	row, err := reopened.Session(ctx, "ses_1")
	_ = reopened.Close()
	if err != nil || row.Workspace != filepath.Join(restored, "sessions", "ses_1", "workspace") {
		t.Fatalf("restored state paths were not relocated: %+v %v", row, err)
	}
	if err := os.WriteFile(filepath.Join(home, "sessions", "ses_1", "result.txt"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCurrent(ctx, home, backup); err == nil {
		t.Fatal("state changed after backup but prune precondition passed")
	}
	if err := os.WriteFile(filepath.Join(backup, "turnyard.sqlite3"), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(ctx, backup); err == nil {
		t.Fatal("tampered backup passed verification")
	}
}

func TestBackupRejectsEscapingSymlinkAndNestedDestination(t *testing.T) {
	ctx := context.Background()
	home := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "turnyard.sqlite3"), []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Backup(ctx, home, filepath.Join(home, "nested")); err == nil {
		t.Fatal("nested backup accepted")
	}
	if err := os.Symlink("../../outside", filepath.Join(home, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := Backup(ctx, home, filepath.Join(filepath.Dir(home), "backup")); err == nil {
		t.Fatal("escaping symlink accepted")
	}
}
