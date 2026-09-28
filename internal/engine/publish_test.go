package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Covalane/turnyard/internal/fault"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

func TestCompletedSessionPublishesTwoRepositories(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	inputs := filepath.Join(f.root, "inputs")
	var session SessionSpec
	var env EnvironmentSpec
	if err := ReadJSON(filepath.Join(inputs, "session.json"), "session", &session); err != nil {
		t.Fatal(err)
	}
	if err := ReadJSON(filepath.Join(inputs, "environment.json"), "environment", &env); err != nil {
		t.Fatal(err)
	}
	session.IdempotencyKey = "publish-two-repos"
	env.Git.RemoteWrites = "push"
	remotes := map[string]*git.Repository{}
	for i := range session.Repositories {
		repo := &session.Repositories[i]
		path := filepath.Join(f.root, repo.ID+".git")
		bare, err := git.PlainInit(path, true)
		if err != nil {
			t.Fatal(err)
		}
		repo.PushURL = "file://" + path
		remotes[repo.ID] = bare
	}
	writeJSON(t, filepath.Join(inputs, "publish-session.json"), session)
	writeJSON(t, filepath.Join(inputs, "environment.json"), env)
	created, err := f.service.CreateSession(ctx, filepath.Join(inputs, "publish-session.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.PublishSession(ctx, created.SessionID); fault.CodeOf(err) != fault.CodeInvalidTransition {
		t.Fatalf("premature publish: %v", err)
	}
	calls := 0
	f.driver.run = func(input AgentInvocation) (AgentResult, error) {
		calls++
		if calls == 2 {
			if err := os.WriteFile(filepath.Join(input.Workspace, "api", "second.txt"), []byte("extended"), 0o600); err != nil {
				return AgentResult{}, err
			}
			return AgentResult{NativeID: "native-publish", ActualProvider: "ollama-cloud", ActualModel: "glm-5.3-flash"}, nil
		}
		for _, id := range []string{"api", "web"} {
			if err := os.WriteFile(filepath.Join(input.Workspace, id, "feature.txt"), []byte("implemented"), 0o600); err != nil {
				return AgentResult{}, err
			}
		}
		if err := os.MkdirAll(input.State, 0o700); err != nil {
			return AgentResult{}, err
		}
		return AgentResult{NativeID: "native-publish", ActualProvider: "ollama-cloud", ActualModel: "glm-5.3-flash"}, nil
	}
	work := WorkSpec{SchemaVersion: "turnyard.work/v1", IdempotencyKey: "publish-work", Objective: "Update both repositories", Acceptance: []string{"Both updated"}, Checks: []string{"cross"}}
	work.Scope.Repositories = []ScopeRepo{{ID: "api", Mode: "write"}, {ID: "web", Mode: "write"}}
	workPath := filepath.Join(inputs, "publish-work.json")
	writeJSON(t, workPath, work)
	added, err := f.service.AddTask(ctx, created.SessionID, workPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.RunTask(ctx, added.TaskID, "", false, time.Minute); err != nil {
		t.Fatal(err)
	}
	secondWork := WorkSpec{SchemaVersion: "turnyard.work/v1", IdempotencyKey: "publish-second-work", Objective: "Extend API", Acceptance: []string{"API extended"}, Checks: []string{"cross"}}
	secondWork.Scope.Repositories = []ScopeRepo{{ID: "api", Mode: "write"}, {ID: "web", Mode: "read"}}
	secondPath := filepath.Join(inputs, "publish-second-work.json")
	writeJSON(t, secondPath, secondWork)
	secondTask, err := f.service.AddTask(ctx, created.SessionID, secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.RunTask(ctx, secondTask.TaskID, "", false, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CompleteSession(ctx, created.SessionID); err != nil {
		t.Fatal(err)
	}
	first, err := f.service.PublishSession(ctx, created.SessionID)
	if err != nil || first.Status != "published" || len(first.Features) != 3 {
		t.Fatalf("first publish: %+v %v", first, err)
	}
	for _, item := range first.Features {
		if item.Status != "published" {
			t.Fatalf("feature not published: %+v", item)
		}
		ref, err := remotes[item.Repository].Reference(plumbing.NewBranchReferenceName(item.Branch), true)
		if err != nil || ref.Hash().String() != item.Head {
			t.Fatalf("remote ref mismatch for %+v: %v", item, err)
		}
	}
	second, err := f.service.PublishSession(ctx, created.SessionID)
	if err != nil || second.Status != "published" || len(second.Features) != 3 {
		t.Fatalf("repeat publish: %+v %v", second, err)
	}
	for _, item := range second.Features {
		if item.Status != "already_published" {
			t.Fatalf("repeat publish was not idempotent: %+v", item)
		}
	}
	branch := plumbing.NewBranchReferenceName(second.Features[0].Branch)
	if err := remotes["api"].Storer.SetReference(plumbing.NewHashReference(branch, plumbing.NewHash(session.Repositories[0].Commit))); err != nil {
		t.Fatal(err)
	}
	partial, err := f.service.PublishSession(ctx, created.SessionID)
	if err != nil || partial.Status != "partial" || partial.Features[0].Status != "failed" || partial.Features[0].ErrorCode != string(fault.CodeBranchDrift) || partial.Features[1].Status != "already_published" || partial.Features[2].Status != "already_published" {
		t.Fatalf("partial publication did not preserve per-repo outcomes: %+v %v", partial, err)
	}
}
