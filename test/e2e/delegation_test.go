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

// TestManagedDelegation proves a real primary agent can launch a different
// runtime, wait for its verified candidate, and integrate its isolated result.
func TestManagedDelegation(t *testing.T) {
	if os.Getenv("TURNYARD_DELEGATION_E2E") != "1" {
		t.Skip("set TURNYARD_DELEGATION_E2E=1 for a real model run")
	}
	requireCredential(t)
	h := newHarness(t, "delegation-go")
	h.configEvidence()
	childRuntime := option("TURNYARD_DELEGATION_CHILD_RUNTIME", "kimi")
	h.evidence["child_runtime"] = childRuntime
	source, sha := h.source("code", map[string]string{"README.md": "# Delegation witness\n"})
	env := h.environment([]contracts.CheckSpec{{ID: "witness", Argv: []string{"grep", "-qx", "DELEGATED_WITNESS:alpha", "/workspace/code/witness.txt"}, Repositories: []string{"code"}}})
	env.Agents[0].Delegates = []string{"helper"}
	childBinding := env.ModelBindings[0]
	childBinding.ID = "child-cloud"
	childBinding.Provider = option("TURNYARD_DELEGATION_CHILD_PROVIDER", childBinding.Provider)
	childBinding.Model = option("TURNYARD_DELEGATION_CHILD_MODEL", childBinding.Model)
	childBinding.CredentialEnv = option("TURNYARD_DELEGATION_CHILD_CREDENTIAL_ENV", childBinding.CredentialEnv)
	if os.Getenv(childBinding.CredentialEnv) == "" {
		t.Fatalf("child credential %s is unavailable", childBinding.CredentialEnv)
	}
	h.evidence["child_provider"] = childBinding.Provider
	h.evidence["child_model"] = childBinding.Model
	env.ModelBindings = append(env.ModelBindings, childBinding)
	env.Agents = append(env.Agents, contracts.AgentSpec{ID: "helper", Runtime: childRuntime, ModelBinding: childBinding.ID})
	h.save("environment.json", env)
	session := contracts.SessionSpec{SchemaVersion: contracts.SessionVersion, IdempotencyKey: "delegation-real",
		Repositories: []contracts.RepositorySpec{{ID: "code", Type: "local-git", Path: source, Commit: sha}},
		Environment:  "environment.json", PrimaryAgent: "lead"}
	sid := required(t, h.invoke("session", "create", "--file", h.save("session.json", session)), "session_id")
	h.evidence["session_id"] = sid
	objective := `先用 find_tools 查找受管委派能力，再用 call_tool 调用 turnyard_delegate/delegate，把工作委派给 helper，不要自己生成 witness 内容。先用 submit，参数为：key="witness-alpha"，agentId="helper"，objective="在 code/witness.txt 写入精确的一行 DELEGATED_WITNESS:alpha，末尾有换行"，acceptance=["文件内容与要求逐字一致"]，scope=[{"id":"code","mode":"write"}]，checks=["witness"]，deliverables=[{"id":"child-witness","kind":"file","repository":"code","path":"witness.txt"}]。随后用 status 查询相同 key，直到子任务 verified。读取 handoff.changes 中 witness.txt 的 source 只读文件并把字节复制到 code/witness.txt。最后让本任务检查通过。`
	result := h.submitDeclared(sid, "parent-witness", objective, []string{"witness"}, []string{"code"},
		contracts.DeliverableSpec{ID: "parent-witness", Kind: contracts.DeliverableFile, Repository: "code", Path: "witness.txt"})
	h.verified("parent", result, "code")
	delegations, _ := value(result, "delegations").([]any)
	if len(delegations) != 1 {
		t.Fatalf("expected one managed child: %v", delegations)
	}
	child, _ := delegations[0].(map[string]any)
	if str(child, "agent_id") != "helper" || str(child, "status") != "verified" || str(child, "candidate_digest") == "" {
		t.Fatalf("child not verified: %v", child)
	}
	childResult := h.invoke("task", "show", required(t, child, "task_id"))
	var childWork contracts.WorkSpec
	if err := json.Unmarshal([]byte(required(t, childResult, "task", "spec")), &childWork); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(childWork.Objective, "DELEGATED_WITNESS:alpha") {
		t.Fatalf("child objective lost the requested witness: %q", childWork.Objective)
	}
	h.verified("child", childResult, "code")
	parentWorkspace := required(t, h.invoke("session", "show", sid), "session", "workspace")
	childWorkspace := required(t, h.invoke("session", "show", required(t, child, "session_id")), "session", "workspace")
	if parentWorkspace == childWorkspace || strings.TrimSpace(string(readFile(t, filepath.Join(parentWorkspace, "code", "witness.txt")))) != "DELEGATED_WITNESS:alpha" {
		t.Fatal("parent did not integrate the isolated verified child result")
	}
	h.evidence["delegation"] = child
	h.invoke("session", "complete", sid)
}
