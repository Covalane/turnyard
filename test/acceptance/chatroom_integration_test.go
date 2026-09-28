//go:build integration

package acceptance

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/engine"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
}
func fixture(t *testing.T, root string) (string, string, map[string][32]byte) {
	t.Helper()
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir("testdata/chatroom")
	if err != nil {
		t.Fatal(err)
	}
	hashes := map[string][32]byte{}
	for _, file := range files {
		body, err := os.ReadFile(filepath.Join("testdata/chatroom", file.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(source, file.Name()), body, 0644); err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(file.Name(), "_test.go") {
			hashes[file.Name()] = sha256.Sum256(body)
		}
	}
	repo, err := git.PlainInit(source, false)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if _, err := tree.Add(file.Name()); err != nil {
			t.Fatal(err)
		}
	}
	head, err := tree.Commit("Chatroom acceptance fixture", &git.CommitOptions{Author: &object.Signature{
		Name: "Turnyard Acceptance", Email: "acceptance@localhost", When: time.Now()}})
	if err != nil {
		t.Fatal(err)
	}
	return source, head.String(), hashes
}
func unchanged(t *testing.T, workspace string, hashes map[string][32]byte) {
	t.Helper()
	for name, want := range hashes {
		body, err := os.ReadFile(filepath.Join(workspace, "chat", name))
		if err != nil {
			t.Fatal(err)
		}
		if sha256.Sum256(body) != want {
			t.Fatalf("agent modified acceptance test %s", name)
		}
	}
}

// TestChatroomWorkflow 使用真实代理验证连续两轮聊天室任务。
func TestChatroomWorkflow(t *testing.T) {
	if os.Getenv("TURNYARD_CHAT_E2E") != "1" {
		t.Skip("set TURNYARD_CHAT_E2E=1 for a real model run")
	}
	if os.Getenv("OLLAMA_API_KEY") == "" {
		t.Fatal("OLLAMA_API_KEY is required")
	}
	ctx := context.Background()
	root, err := filepath.Abs(filepath.Join("..", "..", ".turnyard", "chat-e2e-"+time.Now().Format("20060102-150405")))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	evidence := map[string]any{"root": root, "started_at": time.Now().Format(time.RFC3339Nano), "status": "running"}
	defer func() {
		evidence["finished_at"] = time.Now().Format(time.RFC3339Nano)
		writeJSON(t, filepath.Join(root, "evidence.json"), evidence)
	}()
	source, head, hashes := fixture(t, root)
	backend := os.Getenv("TURNYARD_CHAT_BACKEND")
	if backend == "" {
		backend = "apple-container"
	}
	image := os.Getenv("TURNYARD_CHAT_IMAGE")
	if image == "" {
		image = "turnyard-agent:dev"
	}
	model := os.Getenv("TURNYARD_CHAT_MODEL")
	if model == "" {
		model = "glm-5.3-flash"
	}
	env := contracts.EnvironmentSpec{
		SchemaVersion: "turnyard.environment/v1",
		Sandbox:       contracts.SandboxSpec{Backend: backend, Image: image, CPUs: 2, MemoryMB: 2048},
		Agents:        []contracts.AgentSpec{{ID: "lead", Runtime: "opencode", ModelBinding: "cloud"}},
		ModelBindings: []contracts.ModelBinding{{ID: "cloud", Provider: "ollama-cloud", Model: model, CredentialEnv: "OLLAMA_API_KEY"}},
		Git:           contracts.GitPolicy{LocalCommits: true, RemoteWrites: "none"},
		Checks: []contracts.CheckSpec{
			{ID: "public-chat", Argv: []string{"go", "-C", "/workspace/chat", "test", "-run", "TestPublicChat", "./..."}, Repositories: []string{"chat"}, TimeoutSeconds: 90},
			{ID: "private-chat", Argv: []string{"go", "-C", "/workspace/chat", "test", "-tags", "private", "./..."}, Repositories: []string{"chat"}, TimeoutSeconds: 90},
		},
	}
	session := contracts.SessionSpec{SchemaVersion: "turnyard.session/v1", IdempotencyKey: "chatroom-acceptance",
		Repositories: []contracts.RepositorySpec{{ID: "chat", Type: "local-git", Path: source, Commit: head}},
		Environment:  "environment.json", PrimaryAgent: "lead"}
	inputs := filepath.Join(root, "inputs")
	writeJSON(t, filepath.Join(inputs, "environment.json"), env)
	writeJSON(t, filepath.Join(inputs, "session.json"), session)
	service, err := engine.NewService(ctx, filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	created, err := service.CreateSession(ctx, filepath.Join(inputs, "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	sessionID := created.SessionID
	workspace := created.Workspace
	evidence["session_id"] = sessionID
	makeWork := func(key, objective, check string) string {
		work := contracts.WorkSpec{SchemaVersion: "turnyard.work/v1", IdempotencyKey: key, Objective: objective,
			Acceptance: []string{"聊天室行为符合现有验收测试", "不持久保存聊天记录"},
			Checks:     []string{check}}
		work.Scope.Repositories = []contracts.ScopeRepo{{ID: "chat", Mode: "write"}}
		path := filepath.Join(inputs, key+".json")
		writeJSON(t, path, work)
		return path
	}
	task1 := makeWork("group-chat",
		"请在现有 Go 项目中完成一个简单的多人聊天室。用户可以加入聊天室并实时交流；消息只在运行期间保留，重新启动后不显示历史记录。"+
			"这一轮先完成公开聊天，不包含私聊。请保留仓库内的验收测试。", "public-chat")
	task2 := makeWork("private-chat",
		"基于上一轮已完成的聊天室，增加用户间的私聊。指定收件人能看到私聊内容，其他人不能看到；公开聊天仍能正常使用，"+
			"重新启动后也不显示历史记录。请保留仓库内的验收测试。", "private-chat")
	run := func(stage, path string) (string, string) {
		t.Helper()
		added, err := service.AddTask(ctx, sessionID, path)
		if err != nil {
			t.Fatal(err)
		}
		taskID := added.TaskID
		result, err := service.RunTask(ctx, taskID, "", false, 6*time.Minute)
		if err != nil {
			info, _ := service.TaskResult(ctx, taskID)
			evidence[stage] = map[string]any{"task_id": taskID, "status": "failed", "error": err.Error(), "task": info}
			t.Fatalf("%s execution: %v", stage, err)
		}
		status := result.Status
		evidence[stage] = map[string]any{"task_id": taskID, "status": status, "candidate_id": result.CandidateID, "checkpoint_id": result.CheckpointID}
		if status != "verified" {
			t.Fatalf("%s status %s", stage, status)
		}
		unchanged(t, workspace, hashes)
		invocations, err := service.Store.Invocations(ctx, taskID)
		if err != nil || len(invocations) != 1 {
			t.Fatalf("%s invocations: %v", stage, err)
		}
		if !invocations[0].NativeID.Present {
			t.Fatalf("%s native session missing", stage)
		}
		return taskID, invocations[0].NativeID.Value
	}
	firstID, firstNative := run("groupChat", task1)
	secondID, secondNative := run("privateChat", task2)
	if firstNative != secondNative {
		t.Fatalf("native session changed: %s -> %s", firstNative, secondNative)
	}
	evidence["native_session_id"] = firstNative
	evidence["task_ids"] = []string{firstID, secondID}
	evidence["status"] = "passed"
	fmt.Printf("CHAT_ACCEPTANCE_EVIDENCE=%s\n", filepath.Join(root, "evidence.json"))
}
