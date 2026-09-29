//go:build integration

package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Covalane/turnyard/internal/contracts"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

type harness struct {
	t         *testing.T
	root, cli string
	evidence  map[string]any
	ctx       context.Context
}

func option(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
func value(m map[string]any, keys ...string) any {
	var v any = m
	for _, k := range keys {
		row, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = row[k]
	}
	return v
}
func str(m map[string]any, keys ...string) string { v, _ := value(m, keys...).(string); return v }
func required(t *testing.T, m map[string]any, keys ...string) string {
	t.Helper()
	s := str(m, keys...)
	if s == "" {
		t.Fatalf("missing %s in result: %v", strings.Join(keys, "."), m)
	}
	return s
}
func jsonFile(t *testing.T, path string, v any) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newHarness(t *testing.T, prefix string) *harness {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(repoRoot, ".turnyard", prefix+"-"+time.Now().Format("20060102-150405")+"-"+strings.TrimPrefix(contracts.NewID(""), "_"))
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	h := &harness{t: t, root: root, ctx: ctx, evidence: map[string]any{"root": root, "started_at": time.Now().Format(time.RFC3339Nano), "status": "running"}}
	h.cli = os.Getenv("TURNYARD_BIN")
	if h.cli == "" {
		h.cli = filepath.Join(root, "turnyard")
		cmd := exec.CommandContext(ctx, "go", "build", "-o", h.cli, "./cmd/turnyard")
		cmd.Dir = repoRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build CLI: %v: %s", err, out)
		}
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = exec.CommandContext(stopCtx, h.cli, "--home", filepath.Join(root, "state"), "daemon", "stop").Run()
		h.evidence["finished_at"] = time.Now().Format(time.RFC3339Nano)
		if t.Failed() {
			h.evidence["status"] = "failed"
		} else {
			h.evidence["status"] = "passed"
		}
		jsonFile(t, filepath.Join(root, "evidence.json"), h.evidence)
		t.Logf("E2E_EVIDENCE=%s", filepath.Join(root, "evidence.json"))
	})
	return h
}
func (h *harness) invoke(args ...string) map[string]any {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(h.ctx, 25*time.Minute)
	defer cancel()
	argv := append([]string{"--home", filepath.Join(h.root, "state")}, args...)
	cmd := exec.CommandContext(ctx, h.cli, argv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		h.t.Fatalf("turnyard %s: %v: %s", strings.Join(args, " "), err, tail(out, 1200))
	}
	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		h.t.Fatalf("decode turnyard %s: %v: %s", strings.Join(args, " "), err, tail(out, 1200))
	}
	return result
}
func tail(b []byte, n int) string {
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return string(b)
}
func (h *harness) save(name string, v any) string {
	return jsonFile(h.t, filepath.Join(h.root, "inputs", name), v)
}
func (h *harness) source(name string, files map[string]string) (string, string) {
	h.t.Helper()
	path := filepath.Join(h.root, "sources", name)
	if err := os.MkdirAll(path, 0700); err != nil {
		h.t.Fatal(err)
	}
	repo, err := git.PlainInit(path, false)
	if err != nil {
		h.t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		h.t.Fatal(err)
	}
	for name, content := range files {
		p := filepath.Join(path, name)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			h.t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			h.t.Fatal(err)
		}
		if _, err := wt.Add(name); err != nil {
			h.t.Fatal(err)
		}
	}
	head, err := wt.Commit("Initial acceptance fixture", &git.CommitOptions{Author: &object.Signature{Name: "Turnyard E2E", Email: "e2e@localhost", When: time.Now()}})
	if err != nil {
		h.t.Fatal(err)
	}
	return path, head.String()
}
func (h *harness) assertFixture(workspace, repo, file string, original []byte) {
	h.t.Helper()
	got := readFile(h.t, filepath.Join(workspace, repo, file))
	if sha256.Sum256(got) != sha256.Sum256(original) {
		h.t.Fatalf("agent modified trusted checker %s/%s", repo, file)
	}
}
func (h *harness) verified(stage string, result map[string]any, repos ...string) string {
	h.t.Helper()
	status := str(result, "task", "status")
	candidate, _ := value(result, "candidate").(map[string]any)
	h.evidence[stage] = map[string]any{"task_id": str(result, "task", "id"), "status": status, "error_code": value(result, "task", "error_code"), "candidate": candidate}
	if status != "verified" {
		h.t.Fatalf("%s status=%s error=%v", stage, status, value(result, "task", "error_code"))
	}
	if candidate == nil || str(candidate, "status") != "verified" {
		h.t.Fatalf("%s has no verified candidate", stage)
	}
	var vector map[string]map[string]string
	encodedVector, err := json.Marshal(candidate["vector"])
	if err != nil {
		h.t.Fatal(err)
	}
	if err := json.Unmarshal(encodedVector, &vector); err != nil {
		h.t.Fatal(err)
	}
	for _, repo := range repos {
		entry := vector[repo]
		if entry["head"] == "" || entry["head"] == entry["base"] || entry["branch"] != "feature/"+str(result, "task", "id") {
			h.t.Fatalf("%s missing %s feature commit: %v", stage, repo, entry)
		}
		path := filepath.Join(h.root, "state", "sessions", required(h.t, result, "task", "session_id"), "workspace", repo)
		local, err := git.PlainOpen(path)
		if err != nil {
			h.t.Fatalf("open %s feature repository: %v", repo, err)
		}
		ref, err := local.Reference(plumbing.NewBranchReferenceName(entry["branch"]), true)
		if err != nil || ref.Hash().String() != entry["head"] {
			h.t.Fatalf("%s feature branch does not point to candidate head: ref=%v err=%v", repo, ref, err)
		}
	}
	inv, _ := value(result, "invocations").([]any)
	if len(inv) == 0 {
		h.t.Fatalf("%s has no agent invocation", stage)
	}
	last, _ := inv[len(inv)-1].(map[string]any)
	return required(h.t, last, "native_id")
}
func (h *harness) submit(sid, key, objective string, checks []string, repos ...string) map[string]any {
	return h.submitDeclared(sid, key, objective, checks, repos)
}

func (h *harness) submitDeclared(sid, key, objective string, checks, repos []string, outputs ...contracts.DeliverableSpec) map[string]any {
	h.t.Helper()
	work := contracts.WorkSpec{SchemaVersion: "turnyard.work/v1", IdempotencyKey: key, Objective: objective, Acceptance: []string{"各仓库中的数据符合本轮要求"}, Checks: checks, Deliverables: outputs}
	for _, r := range repos {
		work.Scope.Repositories = append(work.Scope.Repositories, contracts.ScopeRepo{ID: r, Mode: "write"})
	}
	path := h.save(key+".json", work)
	added := h.invoke("task", "add", sid, "--file", path)
	repeat := h.invoke("task", "add", sid, "--file", path)
	tid := required(h.t, added, "task_id")
	if repeat["replayed"] != true || str(repeat, "task_id") != tid {
		h.t.Fatal("idempotent add returned a different task")
	}
	if limit := os.Getenv("TURNYARD_E2E_TASK_TIMEOUT"); limit != "" {
		h.invoke("task", "run", tid, "--timeout", limit)
	} else {
		h.invoke("task", "run", tid)
	}
	return h.invoke("task", "wait", tid)
}

func (h *harness) assertDeliverables(result map[string]any, ids ...string) {
	h.t.Helper()
	candidate, _ := result["candidate"].(map[string]any)
	items, _ := candidate["deliverables"].([]any)
	if len(items) != len(ids) {
		h.t.Fatalf("deliverable count=%d want=%d: %v", len(items), len(ids), candidate)
	}
	for i, id := range ids {
		item, _ := items[i].(map[string]any)
		if str(item, "id") != id || str(item, "status") != "present" || str(item, "sha256") == "" || str(item, "commit") == "" {
			h.t.Fatalf("deliverable %s is not confirmed: %v", id, item)
		}
	}
}
func (h *harness) environment(checks []contracts.CheckSpec) contracts.EnvironmentSpec {
	credentialEnv := requiredCredentialEnv(h.t)
	return contracts.EnvironmentSpec{SchemaVersion: "turnyard.environment/v1", Sandbox: contracts.SandboxSpec{Backend: option("TURNYARD_E2E_BACKEND", "apple-container"), Image: option("TURNYARD_E2E_IMAGE", "turnyard-agent:dev"), CPUs: 2, MemoryMB: 2048, Network: contracts.SandboxNetworkPolicy(option("TURNYARD_E2E_NETWORK", "default")), Isolation: contracts.SandboxIsolationProfile(os.Getenv("TURNYARD_E2E_ISOLATION"))}, Agents: []contracts.AgentSpec{{ID: "lead", Runtime: option("TURNYARD_E2E_RUNTIME", "opencode"), ModelBinding: "cloud"}}, ModelBindings: []contracts.ModelBinding{{ID: "cloud", Provider: requiredSetting(h.t, "TURNYARD_E2E_PROVIDER"), Model: requiredSetting(h.t, "TURNYARD_E2E_MODEL"), CredentialEnv: credentialEnv}}, Git: contracts.GitPolicy{LocalCommits: true, RemoteWrites: contracts.GitRemoteWritesNone}, Checks: checks}
}
func requireCredential(t *testing.T) {
	t.Helper()
	name := requiredCredentialEnv(t)
	if os.Getenv(name) == "" {
		t.Fatalf("%s is required", name)
	}
}
func requiredCredentialEnv(t *testing.T) string {
	t.Helper()
	return requiredSetting(t, "TURNYARD_E2E_CREDENTIAL_ENV")
}
func requiredSetting(t *testing.T, name string) string {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		t.Fatalf("%s is required for a real model run", name)
	}
	return value
}
func (h *harness) readGreeting(workspace string) string {
	h.t.Helper()
	var m map[string]string
	if err := json.Unmarshal(readFile(h.t, filepath.Join(workspace, "api", "message.json")), &m); err != nil {
		h.t.Fatal(err)
	}
	web := strings.TrimSpace(string(readFile(h.t, filepath.Join(workspace, "web", "message.txt"))))
	if m["greeting"] != web {
		h.t.Fatalf("greeting mismatch: api=%q web=%q", m["greeting"], web)
	}
	return web
}
func (h *harness) configEvidence() {
	for k, v := range map[string]string{"backend": option("TURNYARD_E2E_BACKEND", "apple-container"), "runtime": option("TURNYARD_E2E_RUNTIME", "opencode"), "provider": option("TURNYARD_E2E_PROVIDER", "ollama-cloud"), "model": option("TURNYARD_E2E_MODEL", "glm-5.3-flash"), "image": option("TURNYARD_E2E_IMAGE", "turnyard-agent:dev")} {
		h.evidence[k] = v
	}
}
