package agents

import (
	"strings"
	"testing"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/sandbox"
)

func TestGatewayEndpointKeepsProviderPath(t *testing.T) {
	for _, sample := range []struct{ source, want string }{
		{"https://ollama.com/v1", "http://turnyard-model:8080/v1"},
		{"https://api.deepseek.com/anthropic", "http://turnyard-model:8080/anthropic"},
		{"https://api.deepseek.com/", "http://turnyard-model:8080/"},
	} {
		got, err := GatewayEndpoint(sample.source, contracts.SandboxNetworkModelOnly)
		if err != nil || got != sample.want {
			t.Fatalf("gateway endpoint %s: got %s err %v", sample.source, got, err)
		}
	}
	if _, err := GatewayEndpoint("http://example.com/v1", contracts.SandboxNetworkModelOnly); fault.CodeOf(err) != fault.CodeModelUnavailable {
		t.Fatalf("insecure provider endpoint was accepted: %v", err)
	}
}

func TestModelOnlyToolGatewayKeepsCredentialOutsideAgent(t *testing.T) {
	t.Setenv("UPLOAD_TOKEN", "private-test-token")
	env := contracts.EnvironmentSpec{Sandbox: contracts.SandboxSpec{Backend: sandbox.BackendDocker, Network: contracts.SandboxNetworkModelOnly},
		Tools: []contracts.ToolSpec{{ID: "upload", Kind: contracts.ToolKindExecutable, PassEnv: []string{"UPLOAD_TOKEN"}}}}
	agent := contracts.AgentSpec{ID: "lead", Tools: []string{"upload"}}
	if err := ValidateTools(agent, env); err != nil {
		t.Fatalf("Docker tool sidecar was rejected: %v", err)
	}
	gateway, err := ToolGatewayFor(AgentInvocation{Environment: env, State: t.TempDir(), InvocationID: "work"}, agent)
	if err != nil || gateway == nil || gateway.Credentials["UPLOAD_TOKEN"] != "private-test-token" {
		t.Fatalf("gateway did not receive tool credential: %+v %v", gateway, err)
	}
	agentEnv, err := RuntimeCredentials(agent, env, map[string]string{"MODEL_KEY": sandbox.ModelCredentialPlaceholder})
	if err != nil || agentEnv["UPLOAD_TOKEN"] != "" {
		t.Fatalf("agent received tool credential: %+v %v", agentEnv, err)
	}
}

func TestModelOnlyRejectsToolCredentialInjection(t *testing.T) {
	env := contracts.EnvironmentSpec{Sandbox: contracts.SandboxSpec{Network: contracts.SandboxNetworkModelOnly},
		Tools: []contracts.ToolSpec{{ID: "upload", Kind: contracts.ToolKindExecutable, PassEnv: []string{"UPLOAD_TOKEN"}}}}
	agent := contracts.AgentSpec{ID: "lead", Tools: []string{"upload"}}
	if err := ValidateTools(agent, env); fault.CodeOf(err) != fault.CodeCapabilityMissing || !strings.Contains(err.Error(), "upload") {
		t.Fatalf("secret-bearing tool accepted in model-only: %v", err)
	}
}
