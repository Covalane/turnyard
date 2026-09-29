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
		`{"type":"tool_use","sessionID":"ses_1","part":{"type":"tool","tool":"skill","state":{"status":"completed","input":{"name":"review"}}}}`
	result, err := parseOpenCodeEvents(raw)
	if err != nil || result.NativeID != "ses_1" || len(result.Events) != 2 ||
		!reflect.DeepEqual(result.CalledTools, []string{"skill"}) ||
		!reflect.DeepEqual(result.LoadedSkills, []string{"review"}) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	_, err = parseOpenCodeEvents(raw + "\n" + `{"type":"text","sessionID":"ses_2"}`)
	if fault.CodeOf(err) != fault.CodeNativeSessionMismatch {
		t.Fatalf("multiple sessions accepted: %v", err)
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
