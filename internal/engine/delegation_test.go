package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/lifecycle"
	"github.com/Covalane/turnyard/internal/store"
)

func TestDelegationRequestRootRejectsEscapingSymlink(t *testing.T) {
	state := t.TempDir()
	outside := t.TempDir()
	if err := os.Chmod(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(state, "delegation-requests")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := openDelegationRequestRoot(state, "inv_aaaaaaaaaaaaaaaa"); err == nil {
		t.Fatal("agent-controlled symlink was accepted")
	}
	info, err := os.Stat(outside)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("outside directory permissions changed: %v, %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(outside, "inv_aaaaaaaaaaaaaaaa")); !os.IsNotExist(err) {
		t.Fatalf("request directory escaped state: %v", err)
	}
}

func TestParentRepositoryScopeAllowsReadDowngrade(t *testing.T) {
	parent := parentRepositoryScope{"read": contracts.ScopeRead, "write": contracts.ScopeWrite, "invalid": "invalid"}
	for _, item := range []contracts.ScopeRepo{{ID: "read", Mode: contracts.ScopeRead}, {ID: "write", Mode: contracts.ScopeRead}, {ID: "write", Mode: contracts.ScopeWrite}} {
		if !parent.allows(item) {
			t.Fatalf("valid child scope rejected: %+v", item)
		}
	}
	for _, item := range []contracts.ScopeRepo{{ID: "read", Mode: contracts.ScopeWrite}, {ID: "missing", Mode: contracts.ScopeRead}, {ID: "write", Mode: "invalid"}, {ID: "invalid", Mode: contracts.ScopeRead}} {
		if parent.allows(item) {
			t.Fatalf("invalid child scope accepted: %+v", item)
		}
	}
}

func TestDelegationRequestFIFOIsRejectedWithoutBlocking(t *testing.T) {
	requestDir := t.TempDir()
	root, err := os.OpenRoot(requestDir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	name := strings.Repeat("a", 32) + ".json"
	if err := syscall.Mkfifo(filepath.Join(requestDir, name), 0o600); err != nil {
		t.Fatal(err)
	}
	controlDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(controlDir, "responses"), 0o700); err != nil {
		t.Fatal(err)
	}
	server := &delegationServer{requestRoot: root, controlDir: controlDir}
	done := make(chan struct{})
	go func() { server.processRequest(name); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("FIFO request blocked the delegation server")
	}
	body, err := os.ReadFile(filepath.Join(controlDir, "responses", name))
	if err != nil || !strings.Contains(string(body), "invalid delegation request file") {
		t.Fatalf("FIFO rejection missing: %s, %v", body, err)
	}
}

func TestDelegationRequestRejectsInvalidTimeout(t *testing.T) {
	server := &delegationServer{}
	_, err := server.dispatch(context.Background(), delegationRequest{Action: "status", Key: "valid-key", TimeoutSeconds: 7201})
	if fault.CodeOf(err) != fault.CodeInvalidRequest {
		t.Fatalf("invalid timeout was accepted: %v", err)
	}
}

func delegationCall(t *testing.T, control string, request delegationRequest) (map[string]any, string) {
	t.Helper()
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	name := hex.EncodeToString(nonce[:]) + ".json"
	requestDir := filepath.Join(filepath.Dir(filepath.Dir(control)), "agent-state", "delegation-requests", filepath.Base(control))
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(requestDir, name), body, 0o600); err != nil {
		t.Fatal(err)
	}
	var response struct {
		OK     bool           `json:"ok"`
		Result map[string]any `json:"result"`
		Error  string         `json:"error"`
	}
	responsePath := filepath.Join(control, "responses", name)
	deadline := time.Now().Add(5 * time.Second)
	for {
		body, err := os.ReadFile(responsePath)
		if err == nil {
			if err := json.Unmarshal(body, &response); err != nil {
				t.Fatal(err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("delegation response unavailable: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return response.Result, response.Error
}

func TestManagedDelegationCreatesIsolatedVerifiedChildAndHandoff(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	repo := fixtureRepo(t, root, "api")
	inputs := filepath.Join(root, "inputs")
	session := SessionSpec{SchemaVersion: contracts.SessionVersion, IdempotencyKey: "parent", Repositories: []RepositorySpec{repo}, Environment: "environment.json", PrimaryAgent: "lead"}
	env := EnvironmentSpec{SchemaVersion: contracts.EnvironmentVersion,
		Sandbox: SandboxSpec{Backend: "docker", Image: "test:latest"},
		Agents: []AgentSpec{{ID: "lead", Runtime: "opencode", ModelBinding: "model", Delegates: []string{"helper"}},
			{ID: "helper", Runtime: "kimi", ModelBinding: "model"}},
		ModelBindings: []ModelBinding{{ID: "model", Provider: "ollama-cloud", Model: "test", CredentialEnv: "OLLAMA_API_KEY"}},
		Git:           GitPolicy{LocalCommits: true, RemoteWrites: "none"},
		Checks:        []CheckSpec{{ID: "build", Argv: []string{"go", "version"}, Repositories: []string{"api"}}}}
	writeJSON(t, filepath.Join(inputs, "environment.json"), env)
	writeJSON(t, filepath.Join(inputs, "session.json"), session)
	service, err := NewService(ctx, filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	sb := &testSandbox{}
	service.BackendFactory = func(string) (SandboxBackend, error) { return sb, nil }
	service.DriverFactory = func(runtime string) (AgentDriver, error) {
		return &testDriver{runtime: runtime, run: func(input AgentInvocation) (AgentResult, error) {
			if input.AgentID == "helper" {
				if err := os.WriteFile(filepath.Join(input.Workspace, "api", "child.txt"), []byte("verified child output\n"), 0o600); err != nil {
					return AgentResult{}, err
				}
				return AgentResult{NativeID: "helper-native"}, nil
			}
			base := delegationRequest{Action: "submit", Key: "feature-a", AgentID: "helper", Objective: "实现一个文本文件",
				Acceptance: []string{"文件内容正确"}, Scope: []ScopeRepo{{ID: "api", Mode: contracts.ScopeWrite}},
				Deliverables: []contracts.DeliverableSpec{{ID: "child-file", Kind: contracts.DeliverableFile, Repository: "api", Path: "child.txt"}}}
			placeholder := base
			placeholder.Key, placeholder.Objective, placeholder.Acceptance = "placeholder", "x", []string{"y"}
			if _, rejection := delegationCall(t, input.ControlDir, placeholder); !strings.Contains(rejection, "concrete objective") {
				return AgentResult{}, fault.New(fault.CodeInternalError, "placeholder delegation was accepted: %s", rejection)
			}
			unauthorized := base
			unauthorized.Key, unauthorized.AgentID = "unauthorized", "lead"
			if _, rejection := delegationCall(t, input.ControlDir, unauthorized); !strings.Contains(rejection, "cannot delegate") {
				return AgentResult{}, fault.New(fault.CodeInternalError, "unapproved child was accepted: %s", rejection)
			}
			dirty := filepath.Join(input.Workspace, "api", "uncommitted.txt")
			if err := os.WriteFile(dirty, []byte("not in HEAD"), 0o600); err != nil {
				return AgentResult{}, err
			}
			if _, rejection := delegationCall(t, input.ControlDir, base); !strings.Contains(rejection, "commit or discard parent edits") {
				return AgentResult{}, fault.New(fault.CodeInternalError, "dirty parent snapshot was accepted: %s", rejection)
			}
			if err := os.Remove(dirty); err != nil {
				return AgentResult{}, err
			}
			if _, rejection := delegationCall(t, input.ControlDir, base); rejection != "" {
				return AgentResult{}, fault.New(fault.CodeInternalError, "submit failed: %s", rejection)
			}
			var status map[string]any
			deadline := time.Now().Add(8 * time.Second)
			for time.Now().Before(deadline) {
				var rejection string
				status, rejection = delegationCall(t, input.ControlDir, delegationRequest{Action: "status", Key: base.Key})
				if rejection != "" {
					return AgentResult{}, fault.New(fault.CodeInternalError, "status failed: %s", rejection)
				}
				if status["status"] == lifecycle.Verified && status["handoff"] != nil {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if status["status"] != lifecycle.Verified || status["handoff"] == nil {
				return AgentResult{}, fault.New(fault.CodeInternalError, "child not verified: %v", status)
			}
			if _, rejection := delegationCall(t, input.ControlDir, base); rejection != "" {
				return AgentResult{}, fault.New(fault.CodeInternalError, "idempotent submit failed: %s", rejection)
			}
			changed := base
			changed.Objective = "different objective"
			if _, rejection := delegationCall(t, input.ControlDir, changed); !strings.Contains(rejection, "different parent or agent") {
				return AgentResult{}, fault.New(fault.CodeInternalError, "changed delegation replay accepted: %s", rejection)
			}
			handoff := status["handoff"].(map[string]any)
			change := handoff["changes"].([]any)[0].(map[string]any)
			source := filepath.Join(input.ControlDir, strings.TrimPrefix(change["source"].(string), "/turnyard-control/"))
			body, err := os.ReadFile(source)
			if err != nil {
				return AgentResult{}, err
			}
			if err := os.WriteFile(filepath.Join(input.Workspace, "api", "child.txt"), body, 0o600); err != nil {
				return AgentResult{}, err
			}
			if _, rejection := delegationCall(t, input.ControlDir, base); rejection != "" {
				return AgentResult{}, fault.New(fault.CodeInternalError, "replay after parent integration failed: %s", rejection)
			}
			return AgentResult{NativeID: "lead-native"}, nil
		}}, nil
	}
	created, err := service.CreateSession(ctx, filepath.Join(inputs, "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	work := WorkSpec{SchemaVersion: contracts.WorkVersion, IdempotencyKey: "parent-work", Objective: "使用子代理完成文件",
		Acceptance: []string{"文件存在"}, Checks: []string{"build"}, Deliverables: []contracts.DeliverableSpec{{ID: "parent-file", Kind: contracts.DeliverableFile, Repository: "api", Path: "child.txt"}}}
	work.Scope.Repositories = []ScopeRepo{{ID: "api", Mode: contracts.ScopeWrite}}
	writeJSON(t, filepath.Join(inputs, "work.json"), work)
	added, err := service.AddTask(ctx, created.SessionID, filepath.Join(inputs, "work.json"))
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.RunTask(ctx, added.TaskID, "", false, 20*time.Second)
	if err != nil || run.Status != lifecycle.Verified {
		t.Fatalf("parent run: %+v %v", run, err)
	}
	result, err := service.TaskResult(ctx, added.TaskID)
	if err != nil || len(result.Delegations) != 1 || result.Delegations[0].Status != lifecycle.Verified || result.Delegations[0].CandidateDigest == "" {
		t.Fatalf("delegation not first-class in result: %+v %v", result.Delegations, err)
	}
	if _, err := service.CompleteSession(ctx, created.SessionID); err != nil {
		t.Fatal(err)
	}
}

func TestDelegationHandoffRecovery(t *testing.T) {
	for _, failure := range []string{"deterministic", "transient"} {
		t.Run(failure, func(t *testing.T) {
			ctx := context.Background()
			f := artifactFixture(t, false)
			output := contracts.DeliverableSpec{ID: "report", Kind: contracts.DeliverableFile, Path: "report.txt",
				Destination: &contracts.DestinationSpec{Kind: contracts.DestinationLocal}}
			parentWork := artifactWork(t, f, output, nil)
			parentAdded, err := f.service.AddTask(ctx, f.sid, parentWork)
			if err != nil {
				t.Fatal(err)
			}
			parentTask, err := f.service.Store.Task(ctx, parentAdded.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			parentSession, err := f.service.Store.Session(ctx, f.sid)
			if err != nil {
				t.Fatal(err)
			}
			childSpec, childEnv, err := parseSessionRow(parentSession)
			if err != nil {
				t.Fatal(err)
			}
			key := "delegate:" + parentTask.ID + ":report"
			childSpec.IdempotencyKey = key
			child, err := f.service.createSession(ctx, childSpec, childEnv, store.CreateSessionInput{
				ParentSessionID: f.sid, ParentTaskID: parentTask.ID, ParentInvocationID: "inv_parent",
				DelegateAgentID: "lead", DelegationRequestDigest: "request-digest"})
			if err != nil {
				t.Fatal(err)
			}
			childWork := WorkSpec{SchemaVersion: contracts.WorkVersion, IdempotencyKey: key,
				Objective: "生成报告", Acceptance: []string{"报告文件存在"}, Checks: []string{},
				Deliverables: []contracts.DeliverableSpec{output}}
			childWork.Scope.Repositories = []ScopeRepo{}
			added, err := f.service.addTask(ctx, child.SessionID, childWork, parentWork)
			if err != nil {
				t.Fatal(err)
			}
			f.driver.run = func(input AgentInvocation) (AgentResult, error) {
				if err := os.MkdirAll(filepath.Join(input.ArtifactDir, "files"), 0o700); err != nil {
					return AgentResult{}, err
				}
				if err := os.MkdirAll(input.State, 0o700); err != nil {
					return AgentResult{}, err
				}
				if err := os.WriteFile(filepath.Join(input.ArtifactDir, "files", "report.txt"), []byte("verified output\n"), 0o600); err != nil {
					return AgentResult{}, err
				}
				return AgentResult{NativeID: "child-native"}, nil
			}
			first, err := f.service.RunTask(ctx, added.TaskID, "", false, time.Minute)
			if err != nil || first.Status != lifecycle.Verified {
				t.Fatalf("child first run: %+v %v", first, err)
			}
			control := t.TempDir()
			server := &delegationServer{service: f.service, parent: turnExecution{task: parentTask},
				controlDir: control, handoffs: map[string]delegationHandoff{}}
			if failure == "deterministic" {
				candidate, err := f.service.TaskResult(ctx, added.TaskID)
				if err != nil {
					t.Fatal(err)
				}
				path := candidate.Candidate.Deliverables[0].LocalPath
				if err := os.WriteFile(path, []byte("tampered\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(filepath.Join(control, "delegations"), []byte("block directory"), 0o600); err != nil {
				t.Fatal(err)
			}
			response, err := server.status(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			status := response.(map[string]any)
			if failure == "deterministic" {
				if status["status"] != lifecycle.Failed || status["error_code"] != fault.CodeHandoffUnavailable {
					t.Fatalf("deterministic handoff failure was not retryable: %v", status)
				}
				if _, err := f.service.CompleteSession(ctx, child.SessionID); fault.CodeOf(err) != fault.CodeInvalidTransition {
					t.Fatalf("managed child completed without handoff: %v", err)
				}
				retried, err := f.service.RunTask(ctx, added.TaskID, "", true, time.Minute)
				if err != nil || retried.Status != lifecycle.Verified || retried.CandidateID == first.CandidateID {
					t.Fatalf("same child did not produce a new candidate: %+v %v", retried, err)
				}
			} else {
				if status["status"] != "handoff_unavailable" {
					t.Fatalf("transient handoff error was not preserved: %v", status)
				}
				if err := os.Remove(filepath.Join(control, "delegations")); err != nil {
					t.Fatal(err)
				}
			}
			response, err = server.status(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			status = response.(map[string]any)
			if status["status"] != lifecycle.Verified || status["handoff"] == nil {
				t.Fatalf("handoff did not recover: %v", status)
			}
			childRow, err := f.service.Store.Session(ctx, child.SessionID)
			if err != nil || childRow.Status != lifecycle.Completed {
				t.Fatalf("child not completed after handoff: %+v %v", childRow, err)
			}
			invocations, err := f.service.Store.Invocations(ctx, added.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if failure == "deterministic" {
				want = 2
			}
			if len(invocations) != want {
				t.Fatalf("invocations=%d, want %d", len(invocations), want)
			}
		})
	}
}
