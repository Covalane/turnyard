package agents_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Covalane/turnyard/internal/agents"
	"github.com/Covalane/turnyard/internal/agents/registry"
	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/toolgateway"
)

func TestExecutableBundleIsPinnedAndPreparedOnceForEveryDriver(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(source, "run.sh")
	if err := os.WriteFile(program, []byte("#!/bin/sh\nprintf 'ok\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	digest, err := contracts.BundleDigest(source)
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(root, "state")
	agent := contracts.AgentSpec{ID: "lead", Tools: []string{"helper"}}
	env := contracts.EnvironmentSpec{Tools: []contracts.ToolSpec{{ID: "helper", Kind: contracts.ToolKindExecutable,
		Description: "Run a helper", Path: source, SourceDigest: digest,
		Argv: []string{"{toolDir}/run.sh", "fixed"}, TimeoutSeconds: 10}}}
	for _, runtime := range registry.Runtimes() {
		driver, err := registry.Driver(runtime)
		if err != nil || driver.ValidateEnvironment(agent, env) != nil {
			t.Fatalf("%s rejected executable tool: %v", runtime, err)
		}
		launches, err := agents.PrepareTools(agent, env, state)
		if err != nil || len(launches) != 1 || launches[0].ID != toolgateway.ServerID || launches[0].Argv[0] != "/usr/local/bin/turnyard-tool-gateway" {
			t.Fatalf("%s tool launch: %+v %v", runtime, launches, err)
		}
	}
	info, err := os.Stat(filepath.Join(state, "tools", "helper", "run.sh"))
	if err != nil || info.Mode()&0o111 == 0 {
		t.Fatalf("copied executable lost its execute bit: %v %v", info, err)
	}
	body, err := os.ReadFile(filepath.Join(state, "tool-config", "helper.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct{ Argv []string }
	if err := json.Unmarshal(body, &config); err != nil || config.Argv[0] != "/state/tools/helper/run.sh" {
		t.Fatalf("bridge config did not pin the installed bundle: %s %v", body, err)
	}
	if err := os.WriteFile(program, []byte("#!/bin/sh\nprintf 'changed\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := agents.PrepareTools(agent, env, state); fault.CodeOf(err) != fault.CodeEnvironmentDrift {
		t.Fatalf("source drift was accepted: %v", err)
	}
}

func TestToolCatalogLockSurvivesAgentStateRestore(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "agent-state")
	agent := contracts.AgentSpec{ID: "lead", Tools: []string{"helper"}}
	env := contracts.EnvironmentSpec{Tools: []contracts.ToolSpec{{ID: "helper", Kind: contracts.ToolKindExecutable,
		Description: "Helper", Argv: []string{"/bin/true"}}}}
	if _, err := agents.PrepareTools(agent, env, state); err != nil {
		t.Fatal(err)
	}
	configBytes, err := os.ReadFile(filepath.Join(state, "tool-gateway", "lead.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config toolgateway.Config
	if err := json.Unmarshal(configBytes, &config); err != nil || config.CatalogLock != "/turnyard-catalog/lead.sha256" {
		t.Fatalf("catalog lock is not in the durable mount: %+v %v", config, err)
	}
	lock := filepath.Join(agents.ToolCatalogDir(state), "lead.sha256")
	if err := os.WriteFile(lock, []byte("locked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(state); err != nil {
		t.Fatal(err)
	}
	if _, err := agents.PrepareTools(agent, env, state); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(lock); err != nil || string(body) != "locked\n" {
		t.Fatalf("restoring agent state lost the tool catalog lock: %q %v", body, err)
	}
}

func TestHumanInputToolIsAlwaysAvailable(t *testing.T) {
	agent := contracts.AgentSpec{ID: "lead"}
	launches, err := agents.PrepareTools(agent, contracts.EnvironmentSpec{}, t.TempDir())
	if err != nil || len(launches) != 1 || launches[0].ID != toolgateway.ServerID {
		t.Fatalf("built-in tool gateway: %+v %v", launches, err)
	}
}

func TestExistingToolGatewayGainsOnlyBuiltInInputTool(t *testing.T) {
	state := t.TempDir()
	agent := contracts.AgentSpec{ID: "lead", Tools: []string{"helper"}}
	env := contracts.EnvironmentSpec{Tools: []contracts.ToolSpec{{ID: "helper", Kind: contracts.ToolKindExecutable,
		Description: "Helper", Argv: []string{"/bin/true"}}}}
	if _, err := agents.PrepareTools(agent, env, state); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(state, "tool-gateway", "lead.json")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var current toolgateway.Config
	if err := json.Unmarshal(body, &current); err != nil || len(current.Backends) != 2 {
		t.Fatalf("current gateway config: %s %v", body, err)
	}
	current.Backends = current.Backends[:1]
	previousJSON, err := contracts.JSONText(current)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(previousJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := agents.PrepareTools(agent, env, state); err != nil {
		t.Fatalf("additive built-in migration failed: %v", err)
	}
	updated, err := os.ReadFile(path)
	if err != nil || string(updated) != string(body) {
		t.Fatalf("gateway migration changed unrelated config: %v %s", err, updated)
	}
	if err := os.WriteFile(path, []byte(`{"backends":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := agents.PrepareTools(agent, env, state); fault.CodeOf(err) != fault.CodeEnvironmentDrift {
		t.Fatalf("unrelated drift accepted: %v", err)
	}
}

func TestToolCredentialsMustBePresent(t *testing.T) {
	name := "TURNYARD_TOOL_TEST_CREDENTIAL"
	t.Setenv(name, "value")
	agent := contracts.AgentSpec{Tools: []string{"external"}}
	env := contracts.EnvironmentSpec{Tools: []contracts.ToolSpec{{ID: "external", PassEnv: []string{name}}}}
	credentials, err := agents.RuntimeCredentials(agent, env, map[string]string{"MODEL_API_KEY": "model"})
	if err != nil || credentials[name] != "value" || credentials["MODEL_API_KEY"] != "model" {
		t.Fatalf("credentials missing: %+v %v", credentials, err)
	}
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
	if _, err := agents.RuntimeCredentials(agent, env, nil); fault.CodeOf(err) != fault.CodeAuthUnavailable || !strings.Contains(err.Error(), name) {
		t.Fatalf("missing credential did not fail before execution: %v", err)
	}
}
