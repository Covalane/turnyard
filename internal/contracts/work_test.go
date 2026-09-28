package contracts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateWorkRejectsUnsafeCommitSubjects(t *testing.T) {
	session := SessionSpec{Repositories: []RepositorySpec{{ID: "app"}}}
	env := EnvironmentSpec{Checks: []CheckSpec{{ID: "go-test"}}}
	work := WorkSpec{SchemaVersion: WorkVersion, IdempotencyKey: "key", Objective: "Build app", Acceptance: []string{"works"}, Checks: []string{"go-test"}}
	work.Scope.Repositories = []ScopeRepo{{ID: "app", Mode: ScopeWrite}}
	path := filepath.Join(t.TempDir(), "work.json")
	for _, subject := range []string{"feat: safe title\nCo-authored-by: attacker", " leading whitespace", strings.Repeat("a", 81)} {
		work.CommitMessage = subject
		body, err := json.Marshal(work)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ValidateWork(path, session, env); err == nil {
			t.Fatalf("unsafe commit subject accepted: %q", subject)
		}
	}
}

func TestValidateWorkOutputKinds(t *testing.T) {
	session := SessionSpec{Repositories: []RepositorySpec{{ID: "app"}}}
	env := EnvironmentSpec{Checks: []CheckSpec{{ID: "go-test"}}}
	work := WorkSpec{SchemaVersion: WorkVersion, IdempotencyKey: "outputs", Objective: "Produce outputs", Acceptance: []string{"outputs available"}, Checks: []string{"go-test"}}
	work.Scope.Repositories = []ScopeRepo{{ID: "app", Mode: ScopeWrite}}
	path := filepath.Join(t.TempDir(), "work.json")
	tests := []struct {
		name string
		spec DeliverableSpec
		pass bool
	}{
		{"file", DeliverableSpec{ID: "out", Kind: DeliverableFile, Repository: "app", Path: "README.md"}, true},
		{"image", DeliverableSpec{ID: "out", Kind: DeliverableImage, Repository: "app", Path: "art.png", MediaType: "image/png"}, true},
		{"text", DeliverableSpec{ID: "out", Kind: DeliverableText}, true},
		{"pr", DeliverableSpec{ID: "out", Kind: DeliverablePullRequest, Repository: "app", Base: "main"}, true},
		{"text with path", DeliverableSpec{ID: "out", Kind: DeliverableText, Path: "result.txt"}, false},
		{"pr without base", DeliverableSpec{ID: "out", Kind: DeliverablePullRequest, Repository: "app"}, false},
		{"pr unsafe base", DeliverableSpec{ID: "out", Kind: DeliverablePullRequest, Repository: "app", Base: "../main"}, false},
		{"image wrong MIME", DeliverableSpec{ID: "out", Kind: DeliverableImage, Repository: "app", Path: "art.png", MediaType: "text/plain"}, false},
		{"file with declared MIME", DeliverableSpec{ID: "out", Kind: DeliverableFile, Repository: "app", Path: "README.md", MediaType: "text/plain"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			work.Deliverables = []DeliverableSpec{tt.spec}
			body, err := json.Marshal(work)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, body, 0o600); err != nil {
				t.Fatal(err)
			}
			_, err = ValidateWork(path, session, env)
			if (err == nil) != tt.pass {
				t.Fatalf("accepted=%t, want %t: %v", err == nil, tt.pass, err)
			}
		})
	}
}

func TestValidateWorkRejectsUnobservableWork(t *testing.T) {
	work := WorkSpec{SchemaVersion: WorkVersion, IdempotencyKey: "empty", Objective: "Do work", Acceptance: []string{"done"}}
	path := filepath.Join(t.TempDir(), "work.json")
	body, err := json.Marshal(work)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateWork(path, SessionSpec{}, EnvironmentSpec{}); err == nil {
		t.Fatal("work without checks or deliverables was accepted")
	}
}
