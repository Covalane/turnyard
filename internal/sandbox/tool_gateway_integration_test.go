//go:build integration

package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Covalane/turnyard/internal/contracts"
)

func TestToolGatewayKeepsCredentialOutsideAgent(t *testing.T) {
	if os.Getenv("TURNYARD_NETWORK_E2E") != "1" {
		t.Skip("set TURNYARD_NETWORK_E2E=1 for Docker tool gateway validation")
	}
	root := t.TempDir()
	state := filepath.Join(root, "state")
	catalogDir := filepath.Join(root, "tool-catalog")
	if err := os.Mkdir(catalogDir, 0o700); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(filepath.Join(workspace, "code", ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tool-config", "tool-gateway", "tools/secret_tool"} {
		if err := os.MkdirAll(filepath.Join(state, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(state, "native-secret"), []byte("agent-only"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nif [ -e /state/native-secret ]; then exit 8; fi\nif [ \"$TOOL_SECRET\" = \"docker-test-secret\" ]; then printf 'SECRET_OK\\n'; else exit 7; fi\n"
	if err := os.WriteFile(filepath.Join(state, "tools", "secret_tool", "check.sh"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	bridgeConfig := `{"description":"Verify private tool credential","argv":["/state/tools/secret_tool/check.sh"],"pass_env":["TOOL_SECRET"]}`
	if err := os.WriteFile(filepath.Join(state, "tool-config", "secret_tool.json"), []byte(bridgeConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	gatewayConfig := `{"backends":[{"id":"secret_tool","argv":["/usr/local/bin/turnyard-tool-bridge","--config","/state/tool-config/secret_tool.json"],"pass_env":["TOOL_SECRET"]}],"catalog_lock":"/turnyard-catalog/lead.sha256"}`
	configPath := filepath.Join(state, "tool-gateway", "lead.json")
	if err := os.WriteFile(configPath, []byte(gatewayConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	// This code runs inside the agent container. It verifies the actual MCP
	// handshake, discovery, denied call, allowed call, and network boundary.
	agentScript := `const {spawn} = require('node:child_process');
if (require('node:fs').existsSync('/turnyard-catalog')) throw Error('catalog lock reached agent');
if (process.env.TOOL_SECRET) throw Error('tool credential reached agent');
const child = spawn('/usr/local/bin/turnyard-tool-gateway', ['proxy', '--address', 'turnyard-tools:8081']);
let next = 1, pending = new Map(), buffer = '';
child.stdout.on('data', chunk => {
  buffer += chunk;
  while (buffer.includes('\n')) {
    const index = buffer.indexOf('\n');
    const line = buffer.slice(0, index); buffer = buffer.slice(index + 1);
    if (!line) continue;
    const message = JSON.parse(line);
    const resolve = pending.get(message.id);
    if (resolve) { pending.delete(message.id); resolve(message); }
  }
});
function rpc(method, params) {
  const id = next++;
  return new Promise(resolve => { pending.set(id, resolve); child.stdin.write(JSON.stringify({jsonrpc:'2.0',id,method,params})+'\n'); });
}
(async () => {
  const init = await rpc('initialize', {protocolVersion:'2025-11-25',capabilities:{},clientInfo:{name:'test-agent',version:'1'}});
  if (!init.result) throw Error('MCP initialization failed');
  child.stdin.write(JSON.stringify({jsonrpc:'2.0',method:'notifications/initialized'})+'\n');
  const list = await rpc('tools/list', {});
  const names = list.result.tools.map(tool=>tool.name).sort().join(',');
  if (names !== 'call_tool,find_tools') throw Error('unexpected tool surface '+names);
  const found = await rpc('tools/call', {name:'find_tools',arguments:{query:'private credential'}});
  if (!JSON.stringify(found).includes('secret_tool/run')) throw Error('authorized tool not found');
  const denied = await rpc('tools/call', {name:'call_tool',arguments:{toolId:'other/run',arguments:{}}});
  if (!JSON.stringify(denied).includes('not granted')) throw Error('ungranted call was not denied');
  const allowed = await rpc('tools/call', {name:'call_tool',arguments:{toolId:'secret_tool/run',arguments:{args:[]}}});
  if (!JSON.stringify(allowed).includes('SECRET_OK')) throw Error('credentialed tool failed');
  try { await fetch('https://api.github.com', {signal:AbortSignal.timeout(2000)}); throw Error('direct egress succeeded'); }
  catch (error) { if (error.message === 'direct egress succeeded') throw error; }
  child.kill();
  console.log('TOOL_GATEWAY_BOUNDARY_OK');
})().catch(error => { console.error(error); child.kill(); process.exit(2); });`
	image := os.Getenv("TURNYARD_E2E_IMAGE")
	if image == "" {
		image = "turnyard-agent:dev"
	}
	backend := NewOCIBackend(DockerDialect{})
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	result, err := backend.Run(ctx, SandboxRun{Name: "ty-tool-gateway-test", Sandbox: contracts.SandboxSpec{
		Backend: BackendDocker, Image: image, Network: contracts.SandboxNetworkModelOnly, Isolation: contracts.SandboxIsolationProfile(os.Getenv("TURNYARD_E2E_ISOLATION"))},
		State: state, Workspace: workspace, Scope: []contracts.ScopeRepo{{ID: "code", Mode: contracts.ScopeWrite}}, ArtifactDir: filepath.Join(root, "artifacts"), EntryPoint: "node", Command: []string{"-e", agentScript},
		ToolGateway:  &ToolGateway{ConfigPath: configPath, CatalogDir: catalogDir, Credentials: map[string]string{"TOOL_SECRET": "docker-test-secret"}, InvocationID: "test"},
		ModelGateway: &ModelGateway{Endpoint: "https://api.deepseek.com/", Credential: "model-test-secret"},
		Timeout:      60 * time.Second, LogPath: filepath.Join(root, "agent.log")})
	if err != nil || result.ExitCode != 0 || !strings.Contains(result.Output, "TOOL_GATEWAY_BOUNDARY_OK") {
		t.Fatalf("tool gateway boundary: result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(filepath.Join(catalogDir, "lead.sha256")); err != nil {
		t.Fatalf("durable tool catalog lock was not written: %v", err)
	}
}
