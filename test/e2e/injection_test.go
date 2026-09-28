//go:build integration

package e2e

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Covalane/turnyard/internal/contracts"
)

const witnessCheck = `package main
import("os";"strings")
func main(){data,err:=os.ReadFile("/workspace/code/witness.txt");if err!=nil{panic(err)};if strings.TrimSpace(string(data))!="MCP_WITNESS:alpha"{panic("wrong MCP witness")}}
`

type nativeEvent struct {
	Tool   string `json:"tool"`
	Status string `json:"status"`
}

func collectEvents(t *testing.T, path string) []nativeEvent {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 4096), 8<<20)
	var events []nativeEvent
	for scan.Scan() {
		var e map[string]any
		if json.Unmarshal(scan.Bytes(), &e) != nil {
			continue
		}
		switch str(e, "type") {
		case "tool_use":
			events = append(events, nativeEvent{str(e, "part", "tool"), str(e, "part", "state", "status")})
		case "assistant":
			parts, _ := value(e, "message", "content").([]any)
			for _, v := range parts {
				p, _ := v.(map[string]any)
				if str(p, "type") == "tool_use" {
					events = append(events, nativeEvent{str(p, "name"), "called"})
				}
			}
		case "item.completed":
			item, _ := value(e, "item").(map[string]any)
			if str(item, "type") == "mcp_tool_call" {
				events = append(events, nativeEvent{"mcp__" + str(item, "server") + "__" + str(item, "tool"), str(item, "status")})
			}
			if str(item, "type") == "command_execution" && str(item, "status") == "completed" && strings.Contains(str(item, "command"), "/state/home/.agents/skills/witness/SKILL.md") {
				events = append(events, nativeEvent{"skill", "read"})
			}
		}
		if str(e, "role") == "assistant" {
			calls, _ := value(e, "tool_calls").([]any)
			for _, v := range calls {
				call, _ := v.(map[string]any)
				name := str(call, "function", "name")
				events = append(events, nativeEvent{name, "called"})
				if strings.EqualFold(name, "read") && strings.Contains(str(call, "function", "arguments"), "skills/witness/SKILL.md") {
					events = append(events, nativeEvent{"skill", "read"})
				}
			}
		}
	}
	if err := scan.Err(); err != nil {
		t.Fatal(err)
	}
	return events
}

// TestSkillMCPInjection requires native logs to prove actual skill and MCP use.
func TestSkillMCPInjection(t *testing.T) {
	if os.Getenv("TURNYARD_INJECTION_E2E") != "1" {
		t.Skip("set TURNYARD_INJECTION_E2E=1 for a real model run")
	}
	requireCredential(t)
	h := newHarness(t, "injection-go")
	h.configEvidence()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	source, sha := h.source("code", map[string]string{"README.md": "# Witness\n", "verify.go": witnessCheck})
	env := h.environment([]contracts.CheckSpec{{ID: "witness-check", Argv: []string{"env", "GOTMPDIR=/state", "go", "run", "/workspace/code/verify.go"}, Repositories: []string{"code"}, TimeoutSeconds: 90}})
	env.Agents[0].Skills = []string{"witness"}
	env.Agents[0].Tools = []string{"witness"}
	env.Skills = []contracts.BundleSpec{{ID: "witness", Path: filepath.Join(repoRoot, "examples", "skills", "witness")}}
	env.Tools = []contracts.ToolSpec{{ID: "witness", Kind: "mcp", Path: filepath.Join(repoRoot, "examples", "mcp-witness"), Argv: []string{"env", "GOTMPDIR=/state", "go", "run", "{toolDir}/server.go"}}}
	h.save("environment.json", env)
	session := contracts.SessionSpec{SchemaVersion: "turnyard.session/v1", IdempotencyKey: "injection-workflow", Repositories: []contracts.RepositorySpec{{ID: "code", Type: "local-git", Path: source, Commit: sha}}, Environment: "environment.json", PrimaryAgent: "lead"}
	sid := required(t, h.invoke("session", "create", "--file", h.save("session.json", session)), "sessionId")
	h.evidence["sessionId"] = sid
	result := h.submit(sid, "witness-alpha", "先读取 witness skill，再通过 find_tools 找到 witness 工具，使用 call_tool 调用它，参数 value 为 alpha。把工具返回的 token 原样写入 code/witness.txt，不要自行构造 token。", []string{"witness-check"}, "code")
	h.verified("task", result, "code")
	sessionRow := h.invoke("session", "show", sid)
	workspace := required(t, sessionRow, "session", "workspace")
	h.assertFixture(workspace, "code", "verify.go", []byte(witnessCheck))
	inv, _ := value(result, "invocations").([]any)
	last, _ := inv[len(inv)-1].(map[string]any)
	events := collectEvents(t, required(t, last, "log_path"))
	h.evidence["toolEvents"] = events
	skill, mcp := false, false
	for _, e := range events {
		if strings.EqualFold(e.Tool, "skill") {
			skill = true
		}
		if strings.Contains(strings.ToLower(e.Tool), "call_tool") {
			mcp = true
		}
	}
	if env.Sandbox.Backend == "docker" {
		gatewayLog := string(readFile(t, required(t, last, "log_path")+".gateway.log"))
		mcp = mcp && strings.Contains(gatewayLog, "tool=witness/stamp")
		h.evidence["gatewayLog"] = gatewayLog
	}
	h.evidence["skillCalled"] = skill
	h.evidence["mcpCalled"] = mcp
	if !skill || !mcp {
		t.Fatalf("native skill=%v MCP=%v events=%v", skill, mcp, events)
	}
	if strings.TrimSpace(string(readFile(t, filepath.Join(workspace, "code", "witness.txt")))) != "MCP_WITNESS:alpha" {
		t.Fatal("wrong witness artifact")
	}
}

// TestExecutableToolInjection proves that one arbitrary executable bundle is
// exposed through the common bridge and really called by a cloud model.
func TestExecutableToolInjection(t *testing.T) {
	if os.Getenv("TURNYARD_EXECUTABLE_E2E") != "1" {
		t.Skip("set TURNYARD_EXECUTABLE_E2E=1 for a real model run")
	}
	requireCredential(t)
	t.Setenv("TURNYARD_EXEC_WITNESS_MODE", "enabled")
	h := newHarness(t, "executable-go")
	h.configEvidence()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	source, sha := h.source("code", map[string]string{"README.md": "# Executable fixture\n"})
	env := h.environment([]contracts.CheckSpec{{ID: "stamp-check", Argv: []string{"grep", "-qx", "EXEC_WITNESS:alpha", "/workspace/code/witness.txt"}, Repositories: []string{"code"}}})
	env.Agents[0].Tools = []string{"stamp_cli"}
	modelSearch := os.Getenv("TURNYARD_TOOL_SEARCH_E2E") == "1"
	if modelSearch {
		env.ToolSearch = &contracts.ToolSearchSpec{Mode: contracts.ToolSearchLLMRerank, ModelBinding: "cloud"}
	}
	env.Tools = []contracts.ToolSpec{{ID: "stamp_cli", Kind: contracts.ToolKindExecutable,
		Description: "Generate a witness token from one value using the configured executable.",
		Path:        filepath.Join(repoRoot, "examples", "executable-witness"),
		Argv:        []string{"{toolDir}/stamp.sh"}, PassEnv: []string{"TURNYARD_EXEC_WITNESS_MODE"}}}
	h.save("environment.json", env)
	session := contracts.SessionSpec{SchemaVersion: contracts.SessionVersion, IdempotencyKey: "executable-workflow",
		Repositories: []contracts.RepositorySpec{{ID: "code", Type: "local-git", Path: source, Commit: sha}},
		Environment:  "environment.json", PrimaryAgent: "lead"}
	sid := required(t, h.invoke("session", "create", "--file", h.save("session.json", session)), "sessionId")
	objective := "先用 find_tools 找到生成 witness token 的工具，再用 call_tool 调用它，传入 args 数组 [\"alpha\"]。把返回的 token 原样写入 code/witness.txt，不要自己构造 token。"
	if modelSearch {
		objective = "先用 find_tools 查询精确的中文短语『请生成一个见证令牌』，再用 call_tool 调用找到的工具，传入 args 数组 [\"alpha\"]。把返回的 token 原样写入 code/witness.txt，不要自己构造 token。"
	}
	result := h.submit(sid, "executable-alpha", objective, []string{"stamp-check"}, "code")
	h.verified("task", result, "code")
	inv, _ := value(result, "invocations").([]any)
	last, _ := inv[len(inv)-1].(map[string]any)
	events := collectEvents(t, required(t, last, "log_path"))
	called := false
	for _, event := range events {
		if strings.Contains(strings.ToLower(event.Tool), "call_tool") {
			called = true
		}
	}
	if env.Sandbox.Backend == "docker" {
		gatewayLog := string(readFile(t, required(t, last, "log_path")+".gateway.log"))
		called = called && strings.Contains(gatewayLog, "tool=stamp_cli/run")
		if modelSearch && !strings.Contains(gatewayLog, "tool model search") {
			t.Fatal("model-assisted tool search was not observed")
		}
		h.evidence["gatewayLog"] = gatewayLog
	}
	h.evidence["executableCalled"] = called
	h.evidence["toolEvents"] = events
	if !called {
		t.Fatalf("tool gateway did not record the executable call: %+v", events)
	}
	workspace := required(t, h.invoke("session", "show", sid), "session", "workspace")
	if strings.TrimSpace(string(readFile(t, filepath.Join(workspace, "code", "witness.txt")))) != "EXEC_WITNESS:alpha" {
		t.Fatal("executable result was not used")
	}
}
