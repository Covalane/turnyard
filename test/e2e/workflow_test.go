//go:build integration

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Covalane/turnyard/internal/contracts"
)

const greetingCheck = `package main
import("encoding/json";"fmt";"os";"strings")
func main(){
 data,err:=os.ReadFile("/workspace/api/message.json");if err!=nil{panic(err)}
 var message struct{ Greeting string ` + "`json:\"greeting\"`" + ` };if err=json.Unmarshal(data,&message);err!=nil{panic(err)}
 web,err:=os.ReadFile("/workspace/web/message.txt");if err!=nil{panic(err)}
 if message.Greeting!=strings.TrimSpace(string(web)){panic(fmt.Sprintf("greeting mismatch: %q",message.Greeting))}
 if len(os.Args)>1&&os.Args[1]=="hola"&&message.Greeting!="hola"{panic("expected hola")}
}
`

// TestMultiRepoWorkflow covers the CLI supervisor, two Git branches per task,
// native continuation, checkpoint recovery, and a human input gate.
func TestMultiRepoWorkflow(t *testing.T) {
	if os.Getenv("TURNYARD_E2E") != "1" {
		t.Skip("set TURNYARD_E2E=1 for a real model run")
	}
	requireCredential(t)
	h := newHarness(t, "e2e-go")
	h.configEvidence()
	api, apiSHA := h.source("api", map[string]string{"README.md": "# API\n", "verify.go": greetingCheck})
	web, webSHA := h.source("web", map[string]string{"README.md": "# Web\n"})
	env := h.environment([]contracts.CheckSpec{
		{ID: "matching", Argv: []string{"env", "GOTMPDIR=/state", "go", "run", "/workspace/api/verify.go"}, Repositories: []string{"api", "web"}, TimeoutSeconds: 90},
		{ID: "hola", Argv: []string{"env", "GOTMPDIR=/state", "go", "run", "/workspace/api/verify.go", "hola"}, Repositories: []string{"api", "web"}, TimeoutSeconds: 90},
	})
	h.save("environment.json", env)
	session := contracts.SessionSpec{SchemaVersion: "turnyard.session/v1", IdempotencyKey: "multi-repo-workflow", Repositories: []contracts.RepositorySpec{{ID: "api", Type: "local-git", Path: api, Commit: apiSHA}, {ID: "web", Type: "local-git", Path: web, Commit: webSHA}}, Environment: "environment.json", PrimaryAgent: "lead"}
	sid := required(t, h.invoke("session", "create", "--file", h.save("session.json", session)), "session_id")
	h.evidence["session_id"] = sid
	first := h.submitDeclared(sid, "hello", "在 api/message.json 中创建只包含 greeting 为 hello 的 JSON 对象；在 web/message.txt 中写入 hello。两个仓库的内容需要一致。", []string{"matching"}, []string{"api", "web"},
		contracts.DeliverableSpec{ID: "api-message", Repository: "api", Path: "message.json", Kind: "file"},
		contracts.DeliverableSpec{ID: "web-message", Repository: "web", Path: "message.txt", Kind: "file"})
	native := h.verified("first", first, "api", "web")
	h.assertDeliverables(first, "api-message", "web-message")
	second := h.submitDeclared(sid, "hola", "继续上一轮任务，把两个仓库中的问候语都改成 hola。", []string{"matching", "hola"}, []string{"api", "web"},
		contracts.DeliverableSpec{ID: "api-message", Repository: "api", Path: "message.json", Kind: "file"},
		contracts.DeliverableSpec{ID: "web-message", Repository: "web", Path: "message.txt", Kind: "file"})
	if got := h.verified("second", second, "api", "web"); got != native {
		t.Fatalf("native session changed: %q -> %q", native, got)
	}
	h.assertDeliverables(second, "api-message", "web-message")
	show := h.invoke("session", "show", sid)
	workspace := required(t, show, "session", "workspace")
	h.assertFixture(workspace, "api", "verify.go", []byte(greetingCheck))
	events, _ := value(h.invoke("session", "events", sid), "events").([]any)
	checkpoint := ""
	for _, v := range events {
		e, _ := v.(map[string]any)
		if str(e, "type") != "checkpoint.saved" {
			continue
		}
		var p map[string]any
		if json.Unmarshal([]byte(str(e, "payload")), &p) == nil {
			checkpoint = str(p, "checkpoint_id")
		}
	}
	if checkpoint == "" {
		t.Fatal("no saved checkpoint")
	}
	if err := os.RemoveAll(workspace); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(filepath.Dir(workspace), "agent-state")); err != nil {
		t.Fatal(err)
	}
	restored := h.invoke("checkpoint", "restore", checkpoint)
	if restored["restored"] != true {
		t.Fatalf("restore failed: %v", restored)
	}
	h.evidence["restore"] = restored
	if got := h.readGreeting(workspace); got != "hola" {
		t.Fatalf("restored greeting=%q", got)
	}
	h.assertFixture(workspace, "api", "verify.go", []byte(greetingCheck))
	third := h.submitDeclared(sid, "after-restore", "恢复后，在 api 和 web 仓库各添加 recovered.txt，内容为 recovered。保持已有问候语不变。", []string{"matching", "hola"}, []string{"api", "web"},
		contracts.DeliverableSpec{ID: "api-recovered", Repository: "api", Path: "recovered.txt", Kind: "file"},
		contracts.DeliverableSpec{ID: "web-recovered", Repository: "web", Path: "recovered.txt", Kind: "file"})
	if got := h.verified("third", third, "api", "web"); got != native {
		t.Fatalf("post-restore native session changed: %q -> %q", native, got)
	}
	h.assertDeliverables(third, "api-recovered", "web-recovered")
	for _, repo := range []string{"api", "web"} {
		if strings.TrimSpace(string(readFile(t, filepath.Join(workspace, repo, "recovered.txt")))) != "recovered" {
			t.Fatalf("%s recovery output missing", repo)
		}
	}
	gate := contracts.WorkSpec{SchemaVersion: "turnyard.work/v1", IdempotencyKey: "human-gate", Objective: "暂时不要修改文件。请先通过 Turnyard 工具 request_input 询问人类决定两个仓库的问候语。工具调用成功后结束这一轮，收到回复再更新 api/message.json 和 web/message.txt。", Acceptance: []string{"人工回复后两个仓库的问候语一致"}, Checks: []string{"matching"}}
	gate.Scope.Repositories = []contracts.ScopeRepo{{ID: "api", Mode: "write"}, {ID: "web", Mode: "write"}}
	gate.Deliverables = []contracts.DeliverableSpec{{ID: "api-message", Repository: "api", Path: "message.json", Kind: "file"},
		{ID: "web-message", Repository: "web", Path: "message.txt", Kind: "file"}}
	tid := required(t, h.invoke("task", "add", sid, "--file", h.save("human-gate.json", gate)), "task_id")
	h.invoke("task", "run", tid)
	waiting := h.invoke("task", "wait", tid)
	if str(waiting, "task", "status") != "needs_input" {
		t.Fatalf("human gate status=%q", str(waiting, "task", "status"))
	}
	h.invoke("task", "reply", tid, "--text", "Use hallo. Update both greeting files now; do not ask again.")
	completed := h.invoke("task", "wait", tid)
	if got := h.verified("human_gate", completed, "api", "web"); got != native {
		t.Fatalf("human gate native session changed: %q -> %q", native, got)
	}
	h.assertDeliverables(completed, "api-message", "web-message")
	if got := h.readGreeting(workspace); got != "hallo" {
		t.Fatalf("human reply greeting=%q", got)
	}
	h.assertFixture(workspace, "api", "verify.go", []byte(greetingCheck))
	h.evidence["native_session_id"] = native
	closed := h.invoke("session", "complete", sid)
	if str(closed, "schema_version") != "turnyard.session-completion/v1" || str(closed, "status") != "completed" {
		t.Fatalf("session did not complete: %v", closed)
	}
	deliveries, _ := closed["deliveries"].([]any)
	if len(deliveries) != 4 {
		t.Fatalf("completion manifest has %d tasks: %v", len(deliveries), closed)
	}
	for _, entry := range deliveries {
		item, _ := entry.(map[string]any)
		if str(item, "status") != "verified" || str(item, "candidate_digest") == "" {
			t.Fatalf("unverified completion entry: %v", item)
		}
		outputs, _ := item["deliverables"].([]any)
		if len(outputs) != 2 {
			t.Fatalf("completion output missing: %v", item)
		}
	}
	h.evidence["completion"] = closed
}

func TestSessionCancellation(t *testing.T) {
	if os.Getenv("TURNYARD_E2E") != "1" {
		t.Skip("set TURNYARD_E2E=1 for a real sandbox run")
	}
	h := newHarness(t, "cancel-go")
	code, sha := h.source("code", map[string]string{"README.md": "# Cancel fixture\n"})
	env := h.environment([]contracts.CheckSpec{{ID: "readme", Argv: []string{"test", "-f", "/workspace/code/README.md"}, Repositories: []string{"code"}}})
	h.save("environment.json", env)
	session := contracts.SessionSpec{SchemaVersion: "turnyard.session/v1", IdempotencyKey: "cancel-workflow", Repositories: []contracts.RepositorySpec{{ID: "code", Type: "local-git", Path: code, Commit: sha}}, Environment: "environment.json", PrimaryAgent: "lead"}
	sid := required(t, h.invoke("session", "create", "--file", h.save("session.json", session)), "session_id")
	work := contracts.WorkSpec{SchemaVersion: "turnyard.work/v1", IdempotencyKey: "withdrawn", Objective: "Add a feature", Acceptance: []string{"Feature exists"}, Checks: []string{"readme"}}
	work.Scope.Repositories = []contracts.ScopeRepo{{ID: "code", Mode: "write"}}
	tid := required(t, h.invoke("task", "add", sid, "--file", h.save("work.json", work)), "task_id")
	closed := h.invoke("session", "cancel", sid, "--reason", "requirement withdrawn")
	if str(closed, "status") != "cancelled" || str(closed, "reason") != "requirement withdrawn" {
		t.Fatalf("cancellation response: %v", closed)
	}
	show := h.invoke("session", "show", sid)
	if str(show, "session", "status") != "cancelled" || str(show, "session", "cancel_reason") != "requirement withdrawn" {
		t.Fatalf("persisted cancellation: %v", show)
	}
	if str(h.invoke("task", "show", tid), "task", "status") != "cancelled" {
		t.Fatal("unfinished task was not cancelled")
	}
	h.evidence["cancellation"] = closed
}
