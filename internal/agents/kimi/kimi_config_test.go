package kimi

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
)

func TestKimiConfigPreservesNativeStepBudget(t *testing.T) {
	state := t.TempDir()
	agent := contracts.AgentSpec{ID: "lead", Runtime: "kimi", ModelBinding: "model"}
	binding := contracts.ModelBinding{ID: "model", Provider: "ollama-cloud", Model: "test-model", CredentialEnv: "TEST_MODEL_KEY"}
	env := contracts.EnvironmentSpec{Sandbox: contracts.SandboxSpec{Network: contracts.SandboxNetworkModelOnly}}
	if err := kimiConfig(binding, agent, env, state); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(state, "kimi-code", "config.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	config := string(data)
	if strings.Contains(config, "max_steps_per_turn") || strings.Contains(config, "[loop_control]") {
		t.Fatal("adapter overrides the native step budget")
	}
	if !strings.Contains(config, "http://turnyard-model:8080/v1") || !strings.Contains(config, `api_key_env = "TEST_MODEL_KEY"`) {
		t.Fatal("model-only credential and network binding changed")
	}
	if !strings.Contains(config, `pattern = "Bash(git push*)"`) {
		t.Fatal("existing permission restriction changed")
	}

	// Upgrade the one known previous representation, retaining the model and
	// permission fields rather than allowing arbitrary native-state drift.
	legacy := strings.Replace(config, "[background]\n", "[loop_control]\nmax_steps_per_turn = 24\n[background]\n", 1)
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := kimiConfig(binding, agent, env, state); err != nil {
		t.Fatal(err)
	}
	upgraded, err := os.ReadFile(path)
	if err != nil || string(upgraded) != config {
		t.Fatalf("upgrade did not preserve the expected configuration: %v", err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(config, `model = "test-model"`, `model = "other-model"`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := kimiConfig(binding, agent, env, state); fault.CodeOf(err) != fault.CodeEnvironmentDrift {
		t.Fatalf("unexpected model drift result: %v", err)
	}
}

// This opt-in test runs the real CLI against a deterministic local model. Each
// response requests one distinct Bash action, forcing 25 separate model steps.
func TestKimiNativeExceedsLegacyStepCap(t *testing.T) {
	image := os.Getenv("TURNYARD_KIMI_NATIVE_TEST_IMAGE")
	if image == "" {
		t.Skip("set TURNYARD_KIMI_NATIVE_TEST_IMAGE to exercise the installed CLI")
	}
	state := t.TempDir()
	agent := contracts.AgentSpec{ID: "lead", Runtime: "kimi", ModelBinding: "model"}
	binding := contracts.ModelBinding{ID: "model", Provider: "ollama-cloud", Model: "test-model", CredentialEnv: "TEST_MODEL_KEY"}
	if err := kimiConfig(binding, agent, contracts.EnvironmentSpec{}, state); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(state, "kimi-code", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	// Only replace the upstream endpoint in this offline fixture. The emitted
	// loop settings, model and permission rules remain exactly as prepared.
	config := strings.Replace(string(data), "https://ollama.com/v1", "http://127.0.0.1:32119/v1", 1)
	if err := os.WriteFile(filepath.Join(state, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	const server = `import http from 'node:http';
let step = 0;
http.createServer((req, res) => {
  let body = '';
  req.on('data', chunk => { body += chunk; });
  req.on('end', () => {
    const input = JSON.parse(body);
    const n = ++step;
    const delta = n <= 25
      ? { tool_calls: [{ index: 0, id: 'call_' + n, type: 'function',
          function: { name: 'Bash', arguments: JSON.stringify({command: 'printf step-' + n}) } }] }
      : { content: 'NATIVE_STEP_25_COMPLETED' };
    const reason = n <= 25 ? 'tool_calls' : 'stop';
    const base = {id:'step_'+n, object:'chat.completion.chunk', created:1, model:'test-model'};
    if (input.stream) {
      res.writeHead(200, {'Content-Type':'text/event-stream'});
      res.write('data: ' + JSON.stringify({...base, choices:[{index:0, delta, finish_reason:null}]}) + '\n\n');
      res.write('data: ' + JSON.stringify({...base, choices:[{index:0, delta:{}, finish_reason:reason}],
        usage:{prompt_tokens:10, completion_tokens:10, total_tokens:20}}) + '\n\n');
      res.end('data: [DONE]\n\n');
    } else {
      const message = n <= 25 ? {role:'assistant',content:null,tool_calls:delta.tool_calls} : {role:'assistant',content:delta.content};
      res.writeHead(200, {'Content-Type':'application/json'});
      res.end(JSON.stringify({...base,object:'chat.completion',choices:[{index:0,message,finish_reason:reason}],
        usage:{prompt_tokens:10,completion_tokens:10,total_tokens:20}}));
    }
  });
}).listen(32119, '127.0.0.1');
`
	if err := os.WriteFile(filepath.Join(state, "model.mjs"), []byte(server), 0o600); err != nil {
		t.Fatal(err)
	}
	const script = `mkdir -p /state/kimi-code
cp /fixture/config.toml /state/kimi-code/config.toml
node /fixture/model.mjs &
model_pid=$!
trap 'kill "$model_pid"' EXIT
sleep 0.3
KIMI_CODE_HOME=/state/kimi-code TEST_MODEL_KEY=fixture kimi -m turnyard-model -p 'Execute each requested Bash action and finish with the supplied marker.' --output-format stream-json`
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "none", "--user", "0", "--entrypoint", "sh", "-v", state+":/fixture:ro", image, "-ec", script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("native CLI did not complete 25 steps: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "NATIVE_STEP_25_COMPLETED") || !strings.Contains(string(output), "step-25") {
		t.Fatalf("native CLI completion evidence missing:\n%s", output)
	}

	// Prove the fixture crosses the actual former boundary rather than merely
	// checking that a configuration key disappeared.
	legacy := strings.Replace(config, "[background]\n", "[loop_control]\nmax_steps_per_turn = 24\n[background]\n", 1)
	if err := os.WriteFile(filepath.Join(state, "config.toml"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	legacyCmd := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "none", "--user", "0", "--entrypoint", "sh", "-v", state+":/fixture:ro", image, "-ec", script)
	legacyOutput, legacyErr := legacyCmd.CombinedOutput()
	if legacyErr == nil || !strings.Contains(string(legacyOutput), "maxSteps=24") || strings.Contains(string(legacyOutput), "NATIVE_STEP_25_COMPLETED") {
		t.Fatalf("legacy boundary was not reproduced: %v\n%s", legacyErr, legacyOutput)
	}
}
