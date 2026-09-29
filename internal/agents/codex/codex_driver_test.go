package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/toolgateway"
)

func TestDelegationForwardsInvocationToMCP(t *testing.T) {
	state := t.TempDir()
	binding := contracts.ModelBinding{ID: "cloud", Provider: "deepseek", Model: "deepseek-v4-flash", CredentialEnv: "MODEL_API_KEY"}
	agent := contracts.AgentSpec{ID: "lead", Runtime: "codex", ModelBinding: "cloud", Delegates: []string{"helper"}}
	env := contracts.EnvironmentSpec{Agents: []contracts.AgentSpec{agent}, ModelBindings: []contracts.ModelBinding{binding}}
	if err := prepareCodex(binding, agent, env, state); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(state, "codex", "config.toml"))
	if err != nil || !strings.Contains(string(body), `[mcp_servers."turnyard_tools"]`) {
		t.Fatalf("Codex tool gateway config missing: %v %s", err, body)
	}
	configBody, err := os.ReadFile(filepath.Join(state, "tool-gateway", "lead.json"))
	var config toolgateway.Config
	if err != nil || json.Unmarshal(configBody, &config) != nil || len(config.Backends) != 2 || len(config.Backends[0].PassEnv) != 1 || config.Backends[0].PassEnv[0] != "TURNYARD_DELEGATION_INVOCATION" || config.Backends[1].ID != contracts.HumanInputToolID {
		t.Fatalf("delegation invocation not passed to gateway backend: %v %s", err, configBody)
	}
}

func TestCodexNoToolSessionGainsInputCapability(t *testing.T) {
	state := t.TempDir()
	binding := contracts.ModelBinding{Provider: "deepseek", Model: "deepseek-v4-flash", CredentialEnv: "MODEL_API_KEY"}
	agent := contracts.AgentSpec{ID: "lead"}
	env := contracts.EnvironmentSpec{}
	if err := prepareCodex(binding, agent, env, state); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(state, "codex", "config.toml")
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	index := strings.Index(string(current), "[mcp_servers.")
	if index < 0 {
		t.Fatal("built-in gateway was not injected")
	}
	if err := os.WriteFile(path, current[:index], 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareCodex(binding, agent, env, state); err != nil {
		t.Fatalf("additive configuration upgrade failed: %v", err)
	}
	upgraded, err := os.ReadFile(path)
	if err != nil || string(upgraded) != string(current) {
		t.Fatalf("Codex configuration drifted: %v", err)
	}
}
