//go:build integration

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Covalane/turnyard/internal/contracts"
)

// TestTextOutputManifest exercises the agent-written claims file through the
// real CLI, sandbox, model, candidate, and session completion paths.
func TestTextOutputManifest(t *testing.T) {
	if os.Getenv("TURNYARD_OUTPUT_E2E") != "1" {
		t.Skip("set TURNYARD_OUTPUT_E2E=1 for a real model run")
	}
	requireCredential(t)
	h := newHarness(t, "output-go")
	h.configEvidence()
	source, sha := h.source("code", map[string]string{"README.md": "# Output fixture\n"})
	env := h.environment([]contracts.CheckSpec{{ID: "note", Argv: []string{"test", "-s", "/workspace/code/note.txt"}, Repositories: []string{"code"}}})
	h.save("environment.json", env)
	session := contracts.SessionSpec{SchemaVersion: contracts.SessionVersion, IdempotencyKey: "output-manifest",
		Repositories: []contracts.RepositorySpec{{ID: "code", Type: "local-git", Path: source, Commit: sha}},
		Environment:  "environment.json", PrimaryAgent: "lead"}
	sid := required(t, h.invoke("session", "create", "--file", h.save("session.json", session)), "session_id")
	result := h.submitDeclared(sid, "output", "在 code/note.txt 写入一句中文说明，并按产出声明格式提交 summary 文本，概述这句话的用途。", []string{"note"}, []string{"code"},
		contracts.DeliverableSpec{ID: "note", Kind: contracts.DeliverableFile, Repository: "code", Path: "note.txt"},
		contracts.DeliverableSpec{ID: "summary", Kind: contracts.DeliverableText})
	h.verified("output", result, "code")
	candidate, _ := result["candidate"].(map[string]any)
	items, _ := candidate["deliverables"].([]any)
	if len(items) != 2 {
		t.Fatalf("missing unified outputs: %v", candidate)
	}
	text, _ := items[1].(map[string]any)
	if str(text, "kind") != string(contracts.DeliverableText) || str(text, "status") != "present" || str(text, "text") == "" || str(text, "sha256") == "" {
		t.Fatalf("text output was not captured: %v", text)
	}
	completed := h.invoke("session", "complete", sid)
	if str(completed, "status") != "completed" {
		t.Fatalf("output session did not complete: %v", completed)
	}
	h.evidence["completion"] = completed
}

// TestAttachmentArtifactOutput runs a real cloud model with a read-only task
// attachment and a non-Git file output. It proves the mounted byte path, not
// native image perception, which varies by agent runtime and model.
func TestAttachmentArtifactOutput(t *testing.T) {
	if os.Getenv("TURNYARD_ARTIFACT_E2E") != "1" {
		t.Skip("set TURNYARD_ARTIFACT_E2E=1 for a real model run")
	}
	requireCredential(t)
	h := newHarness(t, "artifact-go")
	h.configEvidence()
	env := h.environment([]contracts.CheckSpec{{ID: "contains-id", Argv: []string{"grep", "-q", "CVL-7421", "/workspace/.turnyard-output/report"}, Repositories: []string{}}})
	env.Git = contracts.GitPolicy{} // A repository-free task needs no Git capability.
	h.save("environment.json", env)
	session := contracts.SessionSpec{SchemaVersion: contracts.SessionVersion, IdempotencyKey: "attachment-artifact",
		Repositories: []contracts.RepositorySpec{}, Environment: "environment.json", PrimaryAgent: "lead"}
	sid := required(t, h.invoke("session", "create", "--file", h.save("session.json", session)), "session_id")
	inputPath := filepath.Join(h.root, "inputs", "brief.txt")
	if err := os.WriteFile(inputPath, []byte("项目编号: CVL-7421\n请简短概括这个任务。\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	work := contracts.WorkSpec{SchemaVersion: contracts.WorkVersion, IdempotencyKey: "report", Objective: "阅读附件，生成一个简短文本文件，其中包含附件里的项目编号和一句中文概括。",
		Acceptance: []string{"结果文件包含附件中的项目编号"}, Checks: []string{"contains-id"},
		Inputs:       []contracts.InputSpec{{ID: "brief", Source: contracts.InputSource{Kind: contracts.InputFile, Path: "brief.txt"}, MediaType: "text/plain"}},
		Deliverables: []contracts.DeliverableSpec{{ID: "report", Kind: contracts.DeliverableFile, Path: "report.txt", MediaType: "text/plain", Destination: &contracts.DestinationSpec{Kind: contracts.DestinationLocal}}}}
	work.Scope.Repositories = []contracts.ScopeRepo{}
	added := h.invoke("task", "add", sid, "--file", h.save("work.json", work))
	tid := required(t, added, "task_id")
	h.invoke("task", "run", tid)
	result := h.invoke("task", "wait", tid)
	h.verified("attachment", result)
	items, _ := value(result, "candidate", "deliverables").([]any)
	if len(items) != 1 {
		t.Fatalf("expected one local artifact: %v", items)
	}
	item, _ := items[0].(map[string]any)
	output := required(t, item, "local_path")
	if str(item, "status") != "present" || !strings.Contains(string(readFile(t, output)), "CVL-7421") {
		t.Fatalf("agent did not derive output from attachment: %v", item)
	}
	h.evidence["artifact"] = item
	completed := h.invoke("session", "complete", sid)
	if str(completed, "status") != "completed" {
		t.Fatalf("session did not complete: %v", completed)
	}
	h.evidence["completion"] = completed
}
