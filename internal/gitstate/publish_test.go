package gitstate

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func TestRemoteCloneAndFeaturePublish(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	sourcePath := filepath.Join(root, "source")
	source, err := git.PlainInit(sourcePath, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourcePath, "README.md"), []byte("source\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sourceWorktree, err := source.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sourceWorktree.Add("README.md"); err != nil {
		t.Fatal(err)
	}
	base, err := sourceWorktree.Commit("seed", &git.CommitOptions{Author: &object.Signature{Name: "Test", Email: "test@example.invalid", When: time.Now()}})
	if err != nil {
		t.Fatal(err)
	}
	barePath := filepath.Join(root, "remote.git")
	bare, err := git.PlainInit(barePath, true)
	if err != nil {
		t.Fatal(err)
	}
	url := "file://" + barePath
	remote := git.NewRemote(source.Storer, &gitconfig.RemoteConfig{Name: "test", URLs: []string{url}})
	if err := remote.PushContext(ctx, &git.PushOptions{RemoteName: "test", RefSpecs: []gitconfig.RefSpec{"refs/heads/master:refs/heads/master"}}); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, "workspace")
	spec := contracts.SessionSpec{Repositories: []contracts.RepositorySpec{{ID: "app", Type: "remote-git", URL: url, Commit: base.String()}}}
	if err := PrepareRepositories(ctx, spec, workspace); err != nil {
		t.Fatal(err)
	}
	clone, worktree, err := OpenRepo(filepath.Join(workspace, "app"))
	if err != nil {
		t.Fatal(err)
	}
	branch := plumbing.NewBranchReferenceName("feature/task_1")
	if err := worktree.Checkout(&git.CheckoutOptions{Branch: branch, Create: true, Hash: base}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "app", "README.md"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.Add("README.md"); err != nil {
		t.Fatal(err)
	}
	head, err := worktree.Commit("feature", &git.CommitOptions{Author: &object.Signature{Name: "Test", Email: "test@example.invalid", When: time.Now()}})
	if err != nil {
		t.Fatal(err)
	}
	commit, err := clone.CommitObject(head)
	if err != nil {
		t.Fatal(err)
	}
	version := RepoVersion{Base: base.String(), Head: head.String(), Tree: commit.TreeHash.String(), Branch: branch.Short()}
	if pushed, err := PublishFeature(ctx, workspace, "app", url, version); err != nil || !pushed {
		t.Fatalf("first push: pushed=%v err=%v", pushed, err)
	}
	if pushed, err := PublishFeature(ctx, workspace, "app", url, version); err != nil || pushed {
		t.Fatalf("repeated push: pushed=%v err=%v", pushed, err)
	}
	ref, err := bare.Reference(branch, true)
	if err != nil || ref.Hash() != head {
		t.Fatalf("remote branch does not match candidate: %v %v", ref, err)
	}
	if err := bare.Storer.SetReference(plumbing.NewHashReference(branch, base)); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishFeature(ctx, workspace, "app", url, version); fault.CodeOf(err) != fault.CodeBranchDrift {
		t.Fatalf("expected remote drift, got %v", err)
	}
	if err := clone.Storer.SetReference(plumbing.NewHashReference(branch, base)); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishFeature(ctx, workspace, "app", url, version); fault.CodeOf(err) != fault.CodeCandidateStale {
		t.Fatalf("expected stale candidate, got %v", err)
	}
}
