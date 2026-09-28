package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Covalane/turnyard/internal/contracts"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func TestGenerateValidInputsWithoutChangingSource(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	repo, err := git.PlainInit(source, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "go.mod"), []byte("module example.org/local\n\ngo 1.27\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tree, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tree.Add("go.mod"); err != nil {
		t.Fatal(err)
	}
	if _, err := tree.Commit("initial", &git.CommitOptions{Author: &object.Signature{Name: "Example", Email: "example@localhost", When: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "inputs")
	if err := generate(source, out, "增加健康检查", "health.go", "docker", "turnyard-agent:0.3.2", "opencode", "ollama-cloud", "glm-5.3-flash", "OLLAMA_API_KEY", "feat: add health check"); err != nil {
		t.Fatal(err)
	}
	session, env, err := contracts.LoadSession(filepath.Join(out, "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	work, err := contracts.ValidateWork(filepath.Join(out, "work.json"), session, env)
	if err != nil {
		t.Fatal(err)
	}
	if len(work.Deliverables) != 1 || work.Deliverables[0].Path != "health.go" {
		t.Fatalf("deliverable not generated: %+v", work.Deliverables)
	}
	if work.CommitMessage != "feat: add health check" {
		t.Fatalf("commit message not generated: %q", work.CommitMessage)
	}
	if err := generate(source, out, "第二次", "", "docker", "turnyard-agent:0.3.2", "opencode", "ollama-cloud", "glm-5.3-flash", "OLLAMA_API_KEY", ""); err == nil {
		t.Fatal("generator overwrote existing inputs")
	}
	bad := filepath.Join(root, "bad-inputs")
	if err := generate(source, bad, "第二次", "", "docker", "turnyard-agent:0.3.2", "opencode", "ollama-cloud", "glm-5.3-flash", "OLLAMA_API_KEY", "feat: unsafe\ntrailer"); err == nil {
		t.Fatal("generator accepted a multiline commit subject")
	}
	if _, err := os.Stat(bad); !os.IsNotExist(err) {
		t.Fatalf("invalid subject created files: %v", err)
	}
	status, err := tree.Status()
	if err != nil || !status.IsClean() {
		t.Fatalf("source changed: %v %+v", err, status)
	}
}
