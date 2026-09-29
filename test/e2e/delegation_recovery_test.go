//go:build integration

package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Covalane/turnyard/internal/contracts"
)

// TestDelegationCrashRecovery interrupts both invocations after the child
// starts. Neither task is replayed until an operator reconciles it explicitly.
func TestDelegationCrashRecovery(t *testing.T) {
	if os.Getenv("TURNYARD_DELEGATION_RECOVERY_E2E") != "1" {
		t.Skip("set TURNYARD_DELEGATION_RECOVERY_E2E=1 for a real crash/recovery run")
	}
	requireCredential(t)
	childCredentialEnv := os.Getenv("TURNYARD_DELEGATION_CHILD_CREDENTIAL_ENV")
	if childCredentialEnv == "" || os.Getenv(childCredentialEnv) == "" {
		t.Fatal("TURNYARD_DELEGATION_CHILD_CREDENTIAL_ENV must name an available child model credential")
	}
	childProvider, childModel := os.Getenv("TURNYARD_DELEGATION_CHILD_PROVIDER"), os.Getenv("TURNYARD_DELEGATION_CHILD_MODEL")
	if childProvider == "" || childModel == "" {
		t.Fatal("TURNYARD_DELEGATION_CHILD_PROVIDER and TURNYARD_DELEGATION_CHILD_MODEL are required")
	}
	h := newHarness(t, "delegation-recovery")
	h.configEvidence()
	source, sha := h.source("code", map[string]string{"README.md": "# Recovery witness\n"})
	env := h.environment([]contracts.CheckSpec{{ID: "witness", Argv: []string{"grep", "-qx", "RECOVERED_DELEGATION:alpha", "/workspace/code/witness.txt"}, Repositories: []string{"code"}}})
	env.Agents[0].Delegates = []string{"helper"}
	env.Agents = append(env.Agents, contracts.AgentSpec{ID: "helper", Runtime: "codex", ModelBinding: "child-cloud"})
	env.ModelBindings = append(env.ModelBindings, contracts.ModelBinding{ID: "child-cloud",
		Provider: childProvider, Model: childModel, CredentialEnv: childCredentialEnv})
	h.save("environment.json", env)
	session := contracts.SessionSpec{SchemaVersion: contracts.SessionVersion, IdempotencyKey: "delegation-recovery",
		Repositories: []contracts.RepositorySpec{{ID: "code", Type: "local-git", Path: source, Commit: sha}},
		Environment:  "environment.json", PrimaryAgent: "lead"}
	sid := required(t, h.invoke("session", "create", "--file", h.save("session.json", session)), "session_id")
	h.evidence["session_id"] = sid
	objective := `先用 find_tools 查找受管委派能力，再用 call_tool 调用 turnyard_delegate/delegate，把工作交给 helper，不要自己生成 witness 内容。submit 参数：key="recovery-alpha"，agentId="helper"，objective="在 code/witness.txt 写入精确的一行 RECOVERED_DELEGATION:alpha，末尾有换行"，acceptance=["文件内容逐字一致"]，scope=[{"id":"code","mode":"write"}]，checks=["witness"]，deliverables=[{"id":"child-witness","kind":"file","repository":"code","path":"witness.txt"}]。用 status 等到子任务 verified，从 handoff 复制文件字节，再让父任务检查通过。`
	work := contracts.WorkSpec{SchemaVersion: contracts.WorkVersion, IdempotencyKey: "parent-recovery", Objective: objective,
		Acceptance: []string{"交付物来自受管子任务"}, Checks: []string{"witness"},
		Deliverables: []contracts.DeliverableSpec{{ID: "parent-witness", Kind: contracts.DeliverableFile, Repository: "code", Path: "witness.txt"}}}
	work.Scope.Repositories = []contracts.ScopeRepo{{ID: "code", Mode: contracts.ScopeWrite}}
	tid := required(t, h.invoke("task", "add", sid, "--file", h.save("work.json", work)), "task_id")
	h.invoke("task", "run", tid)
	var childTaskID string
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		shown := h.invoke("task", "show", tid)
		items, _ := value(shown, "delegations").([]any)
		if len(items) == 1 {
			child, _ := items[0].(map[string]any)
			if str(child, "status") == "running" {
				childTaskID = required(t, child, "task_id")
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if childTaskID == "" {
		t.Fatal("child did not reach running state before crash deadline")
	}
	pid := supervisorPID(t, filepath.Join(h.root, "state", "daemon.log"))
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Kill(); err != nil {
		t.Fatal(err)
	}
	h.evidence["crashedWhileChildRunning"] = true
	// The next command starts a new supervisor. Its recovery pass marks every
	// interrupted invocation unknown and removes its orphaned containers.
	parent := h.invoke("task", "show", tid)
	child := h.invoke("task", "show", childTaskID)
	if str(parent, "task", "status") != "unknown" || str(child, "task", "status") != "unknown" {
		t.Fatalf("recovery must not infer success: parent=%v child=%v", value(parent, "task", "status"), value(child, "task", "status"))
	}
	for _, probe := range [][]string{{"network", "ls", "--format", "{{.Name}}"}, {"ps", "-a", "--format", "{{.Names}}"}} {
		output, err := exec.Command("docker", probe...).Output()
		if err != nil || strings.Contains(string(output), "ty-net-") || strings.Contains(string(output), "ty-model-") || strings.Contains(string(output), "ty-tool-") {
			t.Fatalf("orphaned Docker resource after restart: %v %s", err, output)
		}
	}
	h.evidence["orphanedDockerResources"] = false
	h.evidence["interruptedParent"] = parent
	h.evidence["interruptedChild"] = child
	h.invoke("task", "reconcile", childTaskID)
	h.invoke("task", "reconcile", tid)
	h.invoke("task", "retry", childTaskID)
	child = h.invoke("task", "wait", childTaskID)
	h.verified("recoveredChild", child, "code")
	h.invoke("task", "retry", tid)
	parent = h.invoke("task", "wait", tid)
	h.verified("recoveredParent", parent, "code")
	h.invoke("session", "complete", sid)
}

func supervisorPID(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		var entry struct {
			Msg string `json:"msg"`
			PID int    `json:"pid"`
		}
		if json.Unmarshal([]byte(line), &entry) == nil && entry.Msg == "supervisor listening" && entry.PID > 0 {
			return entry.PID
		}
	}
	t.Fatal("supervisor PID is absent from daemon log")
	return 0
}
