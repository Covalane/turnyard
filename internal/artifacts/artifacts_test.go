package artifacts

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/gitstate"
)

func TestClaimsAreInvocationScopedAndRejectEscapes(t *testing.T) {
	state := t.TempDir()
	path := filepath.Join(state, "artifacts.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":"turnyard.artifact-claims/v1","artifacts":[{"id":"answer","text":"hello"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	claims, err := ReadClaims(state)
	if err != nil || claims["answer"].Text != "hello" {
		t.Fatalf("claims: %+v %v", claims, err)
	}
	other, err := ReadClaims(t.TempDir())
	if err != nil || len(other) != 0 {
		t.Fatalf("stale invocation claims: %+v %v", other, err)
	}
	missing, err := ReadClaims(filepath.Join(t.TempDir(), "not-created"))
	if err != nil || len(missing) != 0 {
		t.Fatalf("missing claim directory: %+v %v", missing, err)
	}
	if err := os.WriteFile(path, []byte(`{"schema_version":"turnyard.artifact-claims/v1","artifacts":[{"id":"a","text":"x"},{"id":"a","text":"y"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadClaims(state); err == nil {
		t.Fatal("duplicate claim was accepted")
	}
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadClaims(state); err == nil {
		t.Fatal("claim symlink escaped state root")
	}
}

func TestAgentSpecialFilesCannotBlockArtifactInspection(t *testing.T) {
	root := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(root, "artifacts.json"), 0o600); err != nil {
		t.Skipf("FIFO unavailable: %v", err)
	}
	if _, err := ReadClaims(root); err == nil {
		t.Fatal("FIFO claims should be rejected without opening")
	}
	if err := os.Mkdir(filepath.Join(root, "files"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(root, "files", "report.txt"), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := []contracts.DeliverableSpec{{ID: "report", Kind: contracts.DeliverableFile, Path: "report.txt"}}
	paths, err := SealOutputs(context.Background(), root, t.TempDir(), t.TempDir(), "cand_test", nil, spec)
	if err != nil {
		t.Fatal(err)
	}
	result, err := inspectLocalOutput(context.Background(), spec[0], paths["report"])
	if err != nil || result.Status != StatusMissing {
		t.Fatalf("FIFO output should not be accepted: %+v %v", result, err)
	}
}

type fixedPR struct{ snapshot PullRequestSnapshot }

func (f fixedPR) ReadPullRequest(context.Context, contracts.RepositorySpec, string) (PullRequestSnapshot, error) {
	return f.snapshot, nil
}

func TestPullRequestRequiresMatchingReadback(t *testing.T) {
	spec := contracts.DeliverableSpec{ID: "review", Kind: contracts.DeliverablePullRequest, Repository: "app", Base: "main"}
	version := gitstate.RepoVersion{Head: "0123456789012345678901234567890123456789"}
	claim := Claim{ID: "review", URL: "https://github.com/example/app/pull/1"}
	missing := inspectPullRequest(context.Background(), spec, claim, contracts.RepositorySpec{}, version, nil)
	if missing.Status != StatusUnverified {
		t.Fatalf("unverified claim passed: %+v", missing)
	}
	wrong := inspectPullRequest(context.Background(), spec, claim, contracts.RepositorySpec{}, version,
		fixedPR{PullRequestSnapshot{URL: claim.URL, Base: "main", Head: "other", Open: true}})
	if wrong.Status != StatusMismatch {
		t.Fatalf("wrong head passed: %+v", wrong)
	}
	good := inspectPullRequest(context.Background(), spec, claim, contracts.RepositorySpec{}, version,
		fixedPR{PullRequestSnapshot{URL: claim.URL, Base: "main", Head: version.Head, Open: true}})
	if good.Status != StatusPresent || !Pass([]Result{good}) {
		t.Fatalf("matching PR did not pass: %+v", good)
	}
}

func TestTextClaimHasBoundedContentAndDigest(t *testing.T) {
	spec := contracts.DeliverableSpec{ID: "summary", Kind: contracts.DeliverableText}
	if result := inspectText(spec, Claim{}); result.Status != StatusMissing {
		t.Fatalf("missing text passed: %+v", result)
	}
	result := inspectText(spec, Claim{ID: "summary", Text: "中文结论"})
	if result.Status != StatusPresent || result.SHA256 == "" || result.Bytes != int64(len("中文结论")) {
		t.Fatalf("text not recorded: %+v", result)
	}
	spec.ExpectedSHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	if result := inspectText(spec, Claim{ID: "summary", Text: "中文结论"}); result.Status != StatusMismatch {
		t.Fatalf("wrong text digest passed: %+v", result)
	}
}

func TestGitHubRepositoryURLs(t *testing.T) {
	for _, raw := range []string{"https://github.com/example/app.git", "git@github.com:example/app.git", "ssh://git@github.com/example/app.git"} {
		got, err := githubRepository(raw)
		if err != nil || got != "example/app" {
			t.Fatalf("%q => %q, %v", raw, got, err)
		}
	}
	for _, raw := range []string{"https://evil.example/example/app.git", "https://github.com/example/app/other.git", "https://user:token@github.com/example/app.git"} {
		if _, err := githubRepository(raw); err == nil {
			t.Fatalf("accepted unsafe repository %q", raw)
		}
	}
}

func TestPrivateGitHubPRReadback(t *testing.T) {
	if os.Getenv("TURNYARD_GITHUB_PR_READBACK") != "1" {
		t.Skip("requires authorized GitHub CLI and private validation repository")
	}
	const rawURL = "https://github.com/rwasayc/turnyard-auth/pull/1"
	snapshot, err := (GitHubCLI{}).ReadPullRequest(context.Background(),
		contracts.RepositorySpec{URL: "https://github.com/rwasayc/turnyard-auth.git"}, rawURL)
	if err != nil || snapshot.URL != rawURL || snapshot.Base != "main" || len(snapshot.Head) != 40 || !snapshot.Open {
		t.Fatalf("private PR readback: %+v %v", snapshot, err)
	}
}
