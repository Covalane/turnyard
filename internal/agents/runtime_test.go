package agents

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/sandbox"
)

type capturedRun struct {
	sandbox.SandboxBackend
	input sandbox.SandboxRun
}

func (b *capturedRun) Run(_ context.Context, input sandbox.SandboxRun) (sandbox.SandboxResult, error) {
	b.input = input
	return sandbox.SandboxResult{}, nil
}

func TestBuiltinInputGatewayAlwaysMountsLocalCatalog(t *testing.T) {
	for _, backend := range []string{sandbox.BackendDocker, sandbox.BackendPodman, sandbox.BackendAppleContainer} {
		t.Run(backend, func(t *testing.T) {
			network := contracts.SandboxNetworkDefault
			if backend == sandbox.BackendDocker {
				network = contracts.SandboxNetworkNone
			}
			state := filepath.Join(t.TempDir(), "agent-state")
			agent := contracts.AgentSpec{ID: "lead", ModelBinding: "main"}
			env := contracts.EnvironmentSpec{
				Agents:        []contracts.AgentSpec{agent},
				ModelBindings: []contracts.ModelBinding{{ID: "main", Provider: "ollama-cloud", Model: "test", CredentialEnv: "MODEL_KEY"}},
				Sandbox:       contracts.SandboxSpec{Backend: backend, Network: network},
			}
			launches, err := PrepareTools(agent, env, state)
			if err != nil || len(launches) != 1 {
				t.Fatalf("prepare tools: %v %v", launches, err)
			}
			runner := &capturedRun{}
			_, err = RunMain(context.Background(), AgentInvocation{Environment: env, AgentID: agent.ID, State: state, Sandbox: runner},
				WireOpenAIChat, "", "node", []string{"--version"}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if runner.input.ToolGateway != nil || runner.input.CatalogDir != ToolCatalogDir(state) {
				t.Fatalf("built-in tool has no local catalog mount: %+v", runner.input)
			}
		})
	}
}
