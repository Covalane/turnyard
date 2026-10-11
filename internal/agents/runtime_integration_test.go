//go:build integration

package agents_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Covalane/turnyard/internal/agents"
	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/humaninput"
	sandboxregistry "github.com/Covalane/turnyard/internal/sandbox/registry"
)

func TestBuiltinInputLocalGateway(t *testing.T) {
	if os.Getenv("TURNYARD_NETWORK_E2E") != "1" {
		t.Skip("set TURNYARD_NETWORK_E2E=1 to run a real local gateway")
	}
	backendName := os.Getenv("TURNYARD_INTEGRATION_BACKEND")
	if backendName == "" {
		backendName = "docker"
	}
	backend, err := sandboxregistry.Backend(backendName)
	if err != nil {
		t.Fatal(err)
	}
	image := os.Getenv("TURNYARD_E2E_IMAGE")
	if image == "" {
		t.Fatal("TURNYARD_E2E_IMAGE is required")
	}
	root := t.TempDir()
	state, workspace, outputs := filepath.Join(root, "agent-state"), filepath.Join(root, "workspace"), filepath.Join(root, "outputs")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	agent := contracts.AgentSpec{ID: "lead", ModelBinding: "main"}
	env := contracts.EnvironmentSpec{
		Sandbox:       contracts.SandboxSpec{Backend: backendName, Image: image, Network: contracts.SandboxNetworkNone},
		Agents:        []contracts.AgentSpec{agent},
		ModelBindings: []contracts.ModelBinding{{ID: "main", Provider: "ollama-cloud", Model: "test", CredentialEnv: "MODEL_KEY"}},
	}
	if _, err := agents.PrepareTools(agent, env, state); err != nil {
		t.Fatal(err)
	}
	script := `const {spawn}=require('node:child_process');
const child=spawn('/usr/local/bin/turnyard-tool-gateway',['local','--config','/state/tool-gateway/lead.json']);
let next=1,pending=new Map(),buffer='';
child.stderr.pipe(process.stderr);
child.on('error',error=>{console.error(error);process.exit(2);});
child.on('exit',code=>{if(pending.size){console.error('gateway exited with pending calls: '+code);process.exit(2);}});
child.stdout.on('data',chunk=>{
 buffer+=chunk; while(buffer.includes('\n')){
  const index=buffer.indexOf('\n'),line=buffer.slice(0,index);buffer=buffer.slice(index+1);
  if(!line)continue;const msg=JSON.parse(line),resolve=pending.get(msg.id);
  if(resolve){pending.delete(msg.id);resolve(msg);}
 }
});
function rpc(method,params){const id=next++;return new Promise(resolve=>{pending.set(id,resolve);child.stdin.write(JSON.stringify({jsonrpc:'2.0',id,method,params})+'\n');});}
(async()=>{
 const init=await rpc('initialize',{protocolVersion:'2025-11-25',capabilities:{},clientInfo:{name:'test',version:'1'}});
 if(!init.result)throw Error('initialize failed');
 child.stdin.write(JSON.stringify({jsonrpc:'2.0',method:'notifications/initialized'})+'\n');
 const found=await rpc('tools/call',{name:'find_tools',arguments:{query:'human decision'}});
 if(!JSON.stringify(found).includes('turnyard_input/request_input'))throw Error('input tool not found');
 const called=await rpc('tools/call',{name:'call_tool',arguments:{tool_id:'turnyard_input/request_input',arguments:{question:'请确认是否继续？'}}});
 if(called.error||called.result?.isError)throw Error('input call failed '+JSON.stringify(called));
 child.kill();console.log('BUILTIN_INPUT_OK');
})().catch(error=>{console.error(error);child.kill();process.exit(2);});`
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	result, err := agents.RunMain(ctx, agents.AgentInvocation{Environment: env, AgentID: agent.ID, State: state,
		Workspace: workspace, ArtifactDir: outputs, Sandbox: backend, InvocationID: "inv_aaaaaaaaaaaaaaaa", Timeout: 60 * time.Second, LogPath: filepath.Join(root, "run.log")},
		agents.WireOpenAIChat, "", "node", []string{"-e", script}, nil, nil)
	if err != nil || result.ExitCode != 0 || !strings.Contains(result.Output, "BUILTIN_INPUT_OK") {
		t.Fatalf("local gateway: %+v %v", result, err)
	}
	question, err := humaninput.Read(outputs)
	if err != nil || question != "请确认是否继续？" {
		t.Fatalf("invocation input request=%q %v", question, err)
	}
	if _, err := os.Stat(filepath.Join(agents.ToolCatalogDir(state), "lead.sha256")); err != nil {
		t.Fatalf("catalog lock missing: %v", err)
	}
}
