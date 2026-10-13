package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
)

func TestParseOpenCodeEvents(t *testing.T) {
	raw := `{"type":"text","sessionID":"ses_1","part":{"type":"text","text":"done"}}` + "\n" +
		`{"type":"tool_use","sessionID":"ses_1","part":{"type":"tool","tool":"skill","state":{"status":"completed","input":{"name":"review"}}}}` + "\n" +
		`{"type":"step_finish","sessionID":"ses_1","part":{"reason":"stop"}}`
	result, err := parseOpenCodeEvents(raw)
	if err != nil || result.NativeID != "ses_1" || len(result.Events) != 3 ||
		!reflect.DeepEqual(result.CalledTools, []string{"skill"}) ||
		!reflect.DeepEqual(result.LoadedSkills, []string{"review"}) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	_, err = parseOpenCodeEvents(raw + "\n" + `{"type":"text","sessionID":"ses_2"}`)
	if fault.CodeOf(err) != fault.CodeNativeSessionMismatch {
		t.Fatalf("multiple sessions accepted: %v", err)
	}
}

func TestOpenCodeRequiresCompletedTurn(t *testing.T) {
	for _, raw := range []string{
		`{"type":"text","sessionID":"ses_1","part":{"text":"I will implement it"}}`,
		`{"type":"tool_use","sessionID":"ses_1","part":{"tool":"bash","state":{"status":"error"}}}` + "\n" + `{"type":"step_finish","sessionID":"ses_1","part":{"reason":"tool-calls"}}`,
		`{"type":"step_finish","sessionID":"ses_1","part":{"reason":"length"}}`,
	} {
		if _, err := parseOpenCodeEvents(raw); fault.CodeOf(err) != fault.CodeAgentIncomplete {
			t.Fatalf("incomplete stream accepted: %s, %v", raw, err)
		}
	}
	// A recoverable tool error does not invalidate a later completed turn.
	raw := `{"type":"tool_use","sessionID":"ses_1","part":{"tool":"bash","state":{"status":"error"}}}` + "\n" + `{"type":"step_finish","sessionID":"ses_1","part":{"reason":"tool-calls"}}` + "\n" + `{"type":"step_finish","sessionID":"ses_1","part":{"reason":"stop"}}`
	if _, err := parseOpenCodeEvents(raw); err != nil {
		t.Fatalf("recovered stream rejected: %v", err)
	}
}

func TestOpenCodeHeadlessPermissionUsesSandboxBoundary(t *testing.T) {
	for _, delegates := range [][]string{nil, {"helper"}} {
		state := t.TempDir()
		binding := contracts.ModelBinding{Provider: "ollama-cloud", Model: "glm-5.3-flash", CredentialEnv: "MODEL_API_KEY"}
		if _, err := prepareOpenCode(contracts.EnvironmentSpec{}, contracts.AgentSpec{ID: "lead", Delegates: delegates}, binding, state); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(filepath.Join(state, "config", "opencode", "opencode.json"))
		if err != nil {
			t.Fatal(err)
		}
		var config struct {
			Permission struct {
				All      string            `json:"*"`
				External string            `json:"external_directory"`
				Edit     map[string]string `json:"edit"`
			} `json:"permission"`
		}
		if err := json.Unmarshal(body, &config); err != nil {
			t.Fatal(err)
		}
		if config.Permission.All != "" || config.Permission.External != "allow" {
			t.Fatalf("headless image toolchains would require approval: %s", body)
		}
		if len(delegates) > 0 && config.Permission.Edit["/turnyard-control/delegations/**"] != "deny" {
			t.Fatal("delegation handoff edits were not denied")
		}
	}
}

func TestOpenCodeNoToolSessionGainsInputCapability(t *testing.T) {
	state := t.TempDir()
	binding := contracts.ModelBinding{Provider: "ollama-cloud", Model: "glm-5.3-flash", CredentialEnv: "MODEL_API_KEY"}
	agent := contracts.AgentSpec{ID: "lead"}
	if _, err := prepareOpenCode(contracts.EnvironmentSpec{}, agent, binding, state); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(state, "config", "opencode", "opencode.json")
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var previous map[string]any
	if err := json.Unmarshal(current, &previous); err != nil {
		t.Fatal(err)
	}
	delete(previous, "mcp")
	previousJSON, err := contracts.JSONText(previous)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(previousJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareOpenCode(contracts.EnvironmentSpec{}, agent, binding, state); err != nil {
		t.Fatalf("additive configuration upgrade failed: %v", err)
	}
	upgraded, err := os.ReadFile(path)
	if err != nil || string(upgraded) != string(current) {
		t.Fatalf("OpenCode configuration drifted: %v", err)
	}
}
