package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Covalane/turnyard/internal/artifacts"
	"github.com/Covalane/turnyard/internal/contracts"
)

func deliveryWork(t *testing.T, f *fixture, key string, outputs []contracts.DeliverableSpec) string {
	t.Helper()
	work := WorkSpec{SchemaVersion: "turnyard.work/v1", IdempotencyKey: key, Objective: "Produce the declared files",
		Acceptance: []string{"Declared files are committed"}, Checks: []string{"cross"}, Deliverables: outputs}
	work.Scope.Repositories = []ScopeRepo{{ID: "api", Mode: "write"}, {ID: "web", Mode: "write"}}
	path := filepath.Join(f.root, "inputs", key+".json")
	writeJSON(t, path, work)
	return path
}

func TestTextAndImageOutputsEnterCompletionManifest(t *testing.T) {
	f := newFixture(t)
	f.driver.run = func(input AgentInvocation) (AgentResult, error) {
		if err := os.MkdirAll(input.ArtifactDir, 0o700); err != nil {
			return AgentResult{}, err
		}
		claim := `{"schemaVersion":"turnyard.artifact-claims/v1","artifacts":[{"id":"summary","text":"实现说明：已完成。"}]}`
		if err := os.WriteFile(filepath.Join(input.ArtifactDir, "artifacts.json"), []byte(claim), 0o600); err != nil {
			return AgentResult{}, err
		}
		file, err := os.Create(filepath.Join(input.Workspace, "api", "diagram.png"))
		if err != nil {
			return AgentResult{}, err
		}
		img := image.NewRGBA(image.Rect(0, 0, 1, 1))
		img.Set(0, 0, color.RGBA{R: 255, A: 255})
		err = png.Encode(file, img)
		closeErr := file.Close()
		if err != nil {
			return AgentResult{}, err
		}
		if closeErr != nil {
			return AgentResult{}, closeErr
		}
		return AgentResult{NativeID: "ses_native", ActualProvider: "ollama-cloud", ActualModel: "glm-5.3-flash"}, nil
	}
	path := deliveryWork(t, f, "mixed", []contracts.DeliverableSpec{
		{ID: "summary", Kind: contracts.DeliverableText},
		{ID: "diagram", Kind: contracts.DeliverableImage, Repository: "api", Path: "diagram.png", MediaType: "image/png"},
	})
	added, err := f.service.AddTask(context.Background(), f.sid, path)
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.service.RunTask(context.Background(), added.TaskID, "", false, time.Minute)
	if err != nil || out.Status != "verified" {
		t.Fatalf("mixed outputs: %+v %v", out, err)
	}
	if len(out.Deliverables) != 2 || out.Deliverables[0].Text != "实现说明：已完成。" || out.Deliverables[0].SHA256 == "" || out.Deliverables[1].MediaType != "image/png" {
		t.Fatalf("mixed output evidence: %+v", out.Deliverables)
	}
	completed, err := f.service.CompleteSession(context.Background(), f.sid)
	if err != nil || len(completed.Deliveries) != 1 || completed.Deliveries[0].Deliverables == nil || len(*completed.Deliveries[0].Deliverables) != 2 {
		t.Fatalf("completion manifest: %+v %v", completed, err)
	}
}

func TestPullRequestClaimCannotCompleteWithoutProviderReadback(t *testing.T) {
	f := newFixture(t)
	f.service.ArtifactVerifier = nil
	const prURL = "https://github.com/example/app/pull/7"
	f.driver.run = func(input AgentInvocation) (AgentResult, error) {
		if err := os.MkdirAll(input.ArtifactDir, 0o700); err != nil {
			return AgentResult{}, err
		}
		claim := `{"schemaVersion":"turnyard.artifact-claims/v1","artifacts":[{"id":"review","url":"` + prURL + `"}]}`
		if err := os.WriteFile(filepath.Join(input.ArtifactDir, "artifacts.json"), []byte(claim), 0o600); err != nil {
			return AgentResult{}, err
		}
		return AgentResult{NativeID: "ses_native", ActualProvider: "ollama-cloud", ActualModel: "glm-5.3-flash"}, nil
	}
	path := deliveryWork(t, f, "review", []contracts.DeliverableSpec{{ID: "review", Kind: contracts.DeliverablePullRequest, Repository: "api", Base: "main"}})
	added, err := f.service.AddTask(context.Background(), f.sid, path)
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.service.RunTask(context.Background(), added.TaskID, "", false, time.Minute)
	if err != nil || out.Status != "failed" || out.Deliverables[0].Status != artifacts.StatusUnverified {
		t.Fatalf("unverified PR claim: %+v %v", out, err)
	}
	if _, err := f.service.CompleteSession(context.Background(), f.sid); ErrorCode(err) != "INVALID_TRANSITION" {
		t.Fatalf("unverified PR completed session: %v", err)
	}
	f.service.ArtifactVerifier = testPRVerifier{artifacts.PullRequestSnapshot{URL: prURL, Base: "main", Head: out.CandidateDigest, Open: true}}
	// The candidate digest is deliberately not a commit SHA. A verifier must
	// return the repository head, or the recheck must continue to fail.
	recheck, err := f.service.VerifyCandidate(context.Background(), added.TaskID)
	if err != nil || recheck.Status != "failed" || recheck.Deliverables[0].Status != artifacts.StatusMismatch {
		t.Fatalf("wrong PR head passed: %+v %v", recheck, err)
	}
	f.service.ArtifactVerifier = testPRVerifier{artifacts.PullRequestSnapshot{URL: prURL, Base: "main", Head: recheckHead(t, f, added.TaskID), Open: true}}
	recheck, err = f.service.VerifyCandidate(context.Background(), added.TaskID)
	if err != nil || recheck.Status != "verified" {
		t.Fatalf("matching PR failed: %+v %v", recheck, err)
	}
	if _, err := f.service.CompleteSession(context.Background(), f.sid); err != nil {
		t.Fatalf("verified PR completion: %v", err)
	}
}

func TestImageOutputRejectsNonImageBytes(t *testing.T) {
	f := newFixture(t)
	f.driver.run = func(input AgentInvocation) (AgentResult, error) {
		if err := os.MkdirAll(input.State, 0o700); err != nil {
			return AgentResult{}, err
		}
		if err := os.WriteFile(filepath.Join(input.Workspace, "api", "fake.png"), []byte("plain text"), 0o600); err != nil {
			return AgentResult{}, err
		}
		return AgentResult{NativeID: "ses_native", ActualProvider: "ollama-cloud", ActualModel: "glm-5.3-flash"}, nil
	}
	path := deliveryWork(t, f, "fake-image", []contracts.DeliverableSpec{{ID: "figure", Kind: contracts.DeliverableImage, Repository: "api", Path: "fake.png", MediaType: "image/png"}})
	added, err := f.service.AddTask(context.Background(), f.sid, path)
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.service.RunTask(context.Background(), added.TaskID, "", false, time.Minute)
	if err != nil || out.Status != "failed" || out.Deliverables[0].Status != artifacts.StatusInvalid {
		t.Fatalf("non-image bytes passed: %+v %v", out, err)
	}
}

type testPRVerifier struct{ snapshot artifacts.PullRequestSnapshot }

func (v testPRVerifier) ReadPullRequest(context.Context, contracts.RepositorySpec, string) (artifacts.PullRequestSnapshot, error) {
	return v.snapshot, nil
}

func recheckHead(t *testing.T, f *fixture, taskID string) string {
	t.Helper()
	result, err := f.service.TaskResult(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	return result.Candidate.Vector["api"].Head
}

func TestDeclaredDeliverablesAndSessionCompletion(t *testing.T) {
	f := newFixture(t)
	content := []byte("verified output\n")
	sum := sha256.Sum256(content)
	f.driver.run = func(input AgentInvocation) (AgentResult, error) {
		if err := os.MkdirAll(input.State, 0o700); err != nil {
			return AgentResult{}, err
		}
		if err := os.WriteFile(filepath.Join(input.Workspace, "api", "result.txt"), content, 0o600); err != nil {
			return AgentResult{}, err
		}
		return AgentResult{NativeID: "ses_native", ActualProvider: "ollama-cloud", ActualModel: "glm-5.3-flash"}, nil
	}
	path := deliveryWork(t, f, "delivery", []contracts.DeliverableSpec{{ID: "result", Repository: "api", Path: "result.txt", Kind: "file", ExpectedSHA256: hex.EncodeToString(sum[:])}})
	added, err := f.service.AddTask(context.Background(), f.sid, path)
	if err != nil {
		t.Fatal(err)
	}
	tid := added.TaskID
	out, err := f.service.RunTask(context.Background(), tid, "", false, time.Minute)
	if err != nil || out.Status != "verified" {
		t.Fatalf("run: %v %v", out, err)
	}
	result, err := f.service.TaskResult(context.Background(), tid)
	if err != nil {
		t.Fatal(err)
	}
	if result.SchemaVersion != "turnyard.task-result/v1" {
		t.Fatalf("result version: %v", result)
	}
	candidate := result.Candidate
	if candidate == nil || len(candidate.Vector) == 0 {
		t.Fatalf("candidate vector is missing: %+v", candidate)
	}
	items := candidate.Deliverables
	if len(items) != 1 || items[0].Status != "present" || items[0].SHA256 != hex.EncodeToString(sum[:]) || items[0].Bytes != int64(len(content)) {
		t.Fatalf("deliverable outcome: %+v", items)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	wireCandidate := wire["candidate"].(map[string]any)
	if _, ok := wireCandidate["deliverables"].([]any); !ok {
		t.Fatalf("deliverables not a JSON array: %T", wireCandidate["deliverables"])
	}
	closed, err := f.service.CompleteSession(context.Background(), f.sid)
	if err != nil || closed.Status != "completed" {
		t.Fatalf("complete: %v %v", closed, err)
	}
	deliveries := closed.Deliveries
	if len(deliveries) != 1 || deliveries[0].CandidateDigest == "" || deliveries[0].CandidateStatus != "verified" {
		t.Fatalf("completion manifest: %+v", deliveries)
	}
	again, err := f.service.CompleteSession(context.Background(), f.sid)
	if err != nil || again.CompletedAt != closed.CompletedAt {
		t.Fatalf("idempotent complete: %v %v", again, err)
	}
	if _, err := f.service.AddTask(context.Background(), f.sid, deliveryWork(t, f, "later", nil)); ErrorCode(err) != "INVALID_TRANSITION" {
		t.Fatalf("completed session accepted another task: %v", err)
	}
	if out.CheckpointID == "" {
		t.Fatal("verified task did not return a checkpoint")
	}
	if _, err := f.service.Restore(context.Background(), out.CheckpointID); ErrorCode(err) != "INVALID_TRANSITION" {
		t.Fatalf("completed session accepted checkpoint restoration: %v", err)
	}
	row, err := f.service.Store.Session(context.Background(), f.sid)
	if err != nil || row.Status != "completed" || !row.CompletedAt.Present {
		t.Fatalf("persisted completion: %+v %v", row, err)
	}
	if _, err := os.Stat(filepath.Join(row.Workspace, "api", "result.txt")); err != nil {
		t.Fatalf("completed delivery not reviewable: %v", err)
	}
}

func TestMissingDeliverableBlocksVerificationAndCompletion(t *testing.T) {
	f := newFixture(t)
	f.driver.run = func(input AgentInvocation) (AgentResult, error) {
		if err := os.MkdirAll(input.State, 0o700); err != nil {
			return AgentResult{}, err
		}
		return AgentResult{NativeID: "ses_native", ActualProvider: "ollama-cloud", ActualModel: "glm-5.3-flash"}, nil
	}
	path := deliveryWork(t, f, "missing", []contracts.DeliverableSpec{{ID: "required", Repository: "api", Path: "missing.txt", Kind: "file"}})
	added, err := f.service.AddTask(context.Background(), f.sid, path)
	if err != nil {
		t.Fatal(err)
	}
	tid := added.TaskID
	out, err := f.service.RunTask(context.Background(), tid, "", false, time.Minute)
	if err != nil || out.Status != "failed" {
		t.Fatalf("missing result: %v %v", out, err)
	}
	result, err := f.service.TaskResult(context.Background(), tid)
	if err != nil {
		t.Fatal(err)
	}
	if result.Task.ErrorCode.Value != "DELIVERABLE_MISSING" {
		t.Fatalf("missing error code: %+v", result.Task)
	}
	items := result.Candidate.Deliverables
	if len(items) != 1 || items[0].Status != "missing" {
		t.Fatalf("missing evidence: %+v", items)
	}
	if _, err := f.service.CompleteSession(context.Background(), f.sid); ErrorCode(err) != "INVALID_TRANSITION" {
		t.Fatalf("unfinished requirement completed: %v", err)
	}
	rechecked, err := f.service.VerifyCandidate(context.Background(), tid)
	if err != nil || rechecked.Status != "failed" {
		t.Fatalf("reverify missing candidate: %v %v", rechecked, err)
	}
}

func TestDeliverableDigestMismatchIsReported(t *testing.T) {
	f := newFixture(t)
	f.driver.run = func(input AgentInvocation) (AgentResult, error) {
		if err := os.MkdirAll(input.State, 0o700); err != nil {
			return AgentResult{}, err
		}
		if err := os.WriteFile(filepath.Join(input.Workspace, "api", "output.txt"), []byte("actual"), 0o600); err != nil {
			return AgentResult{}, err
		}
		return AgentResult{NativeID: "ses_native", ActualProvider: "ollama-cloud", ActualModel: "glm-5.3-flash"}, nil
	}
	want := sha256.Sum256([]byte("expected"))
	path := deliveryWork(t, f, "digest-mismatch", []contracts.DeliverableSpec{{ID: "output", Repository: "api",
		Path: "output.txt", Kind: "file", ExpectedSHA256: hex.EncodeToString(want[:])}})
	added, err := f.service.AddTask(context.Background(), f.sid, path)
	if err != nil {
		t.Fatal(err)
	}
	tid := added.TaskID
	out, err := f.service.RunTask(context.Background(), tid, "", false, time.Minute)
	if err != nil || out.Status != "failed" {
		t.Fatalf("run: %v %v", out, err)
	}
	result, err := f.service.TaskResult(context.Background(), tid)
	if err != nil {
		t.Fatal(err)
	}
	if code := result.Task.ErrorCode.Value; code != "DELIVERABLE_MISMATCH" {
		t.Fatalf("wrong mismatch code %q", code)
	}
	items := result.Candidate.Deliverables
	if len(items) != 1 || items[0].Status != "mismatch" || items[0].SHA256 == items[0].ExpectedSHA256 {
		t.Fatalf("mismatch evidence: %+v", items)
	}
}

func TestRejectUnsafeDeliverableSpecification(t *testing.T) {
	for _, path := range []string{"../secret", "a/../secret", ".git/config", "/tmp/result", "a\\b"} {
		t.Run(path, func(t *testing.T) {
			f := newFixture(t)
			file := deliveryWork(t, f, "unsafe", []contracts.DeliverableSpec{{ID: "bad", Repository: "api", Path: path, Kind: "file"}})
			if _, err := f.service.AddTask(context.Background(), f.sid, file); ErrorCode(err) != "INVALID_SPEC" {
				t.Fatalf("accepted unsafe output %q: %v", path, err)
			}
		})
	}
}

func TestCancelAbandonedSession(t *testing.T) {
	f := newFixture(t)
	path := deliveryWork(t, f, "abandoned", nil)
	added, err := f.service.AddTask(context.Background(), f.sid, path)
	if err != nil {
		t.Fatal(err)
	}
	tid := added.TaskID
	closed, err := f.service.CancelSession(context.Background(), f.sid, "requirement withdrawn")
	if err != nil || closed.Status != "cancelled" {
		t.Fatalf("cancel: %v %v", closed, err)
	}
	again, err := f.service.CancelSession(context.Background(), f.sid, "new reason ignored")
	if err != nil || again.CancelledAt != closed.CancelledAt || again.Reason != "requirement withdrawn" {
		t.Fatalf("idempotent cancellation: %v %v", again, err)
	}
	row, err := f.service.Store.Task(context.Background(), tid)
	if err != nil || row.Status != "cancelled" {
		t.Fatalf("unfinished task not cancelled: %+v %v", row, err)
	}
	if _, err := f.service.AddTask(context.Background(), f.sid, path); ErrorCode(err) != "INVALID_TRANSITION" {
		t.Fatalf("cancelled session accepted new task: %v", err)
	}
	if _, err := f.service.RunTask(context.Background(), tid, "", false, time.Minute); ErrorCode(err) != "INVALID_TRANSITION" {
		t.Fatalf("cancelled task ran: %v", err)
	}
	if _, err := f.service.CompleteSession(context.Background(), f.sid); ErrorCode(err) != "INVALID_TRANSITION" {
		t.Fatalf("cancelled session completed: %v", err)
	}
	result, err := f.service.SessionResult(context.Background(), f.sid)
	if err != nil || result.Session.Status != "cancelled" {
		t.Fatalf("cancelled session unreadable: %v %v", result, err)
	}
}
