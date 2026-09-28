package gitstate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func TestExportChangesSeparatesRepositoryFromArtifactNamespace(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "source")
	repo, err := git.PlainInit(repoPath, false)
	if err != nil {
		t.Fatal(err)
	}
	worktree, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	commit := func(name, body string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repoPath, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := worktree.Add(name); err != nil {
			t.Fatal(err)
		}
		hash, err := worktree.Commit(name, &git.CommitOptions{Author: &object.Signature{Name: "Test", Email: "test@example.invalid", When: time.Now()}})
		if err != nil {
			t.Fatal(err)
		}
		return hash.String()
	}
	base := commit("README", "base\n")
	head := commit("report", "verified Git bytes\n")
	destination := filepath.Join(root, "handoff", "child")
	if err := os.MkdirAll(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	changes, err := ExportChanges(context.Background(), repoPath, "artifacts", base, head, destination)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || !strings.HasSuffix(changes[0].Source, "/repositories/artifacts/report") {
		t.Fatalf("Git handoff was not namespaced: %+v", changes)
	}
	gitPath := filepath.Join(destination, "repositories", "artifacts", "report")
	artifactPath := filepath.Join(destination, "artifacts", "report")
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifactPath, []byte("different sealed output\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(gitPath)
	if err != nil || string(content) != "verified Git bytes\n" {
		t.Fatalf("artifact overwrote Git handoff: %q, %v", content, err)
	}
}
