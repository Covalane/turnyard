package agents

import (
	"testing"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
)

func TestProviderOriginControlsEndpointPolicy(t *testing.T) {
	tests := []struct {
		name     string
		binding  contracts.ModelBinding
		wire     ModelWire
		endpoint string
		wantCode fault.Code
	}{
		{name: "built-in", binding: contracts.ModelBinding{Provider: "deepseek", Model: "example", CredentialEnv: "MODEL_API_KEY"},
			wire: WireAnthropic, endpoint: "https://api.deepseek.com/anthropic"},
		{name: "built-in with deployment key name", binding: contracts.ModelBinding{Provider: "deepseek", Model: "example", CredentialEnv: "TEAM_MODEL_KEY"},
			wire: WireAnthropic, endpoint: "https://api.deepseek.com/anthropic"},
		{name: "invalid credential name", binding: contracts.ModelBinding{Provider: "deepseek", Model: "example", CredentialEnv: "BAD-KEY"},
			wire: WireAnthropic, wantCode: fault.CodeModelUnavailable},
		{name: "built-in override", binding: contracts.ModelBinding{Provider: "deepseek", Model: "example", CredentialEnv: "MODEL_API_KEY",
			Endpoints: contracts.ModelEndpoints{Anthropic: "https://other.example/v1"}}, wire: WireAnthropic, wantCode: fault.CodeModelUnavailable},
		{name: "custom", binding: contracts.ModelBinding{Provider: "example", Model: "example", CredentialEnv: "EXAMPLE_API_KEY",
			Endpoints: contracts.ModelEndpoints{Responses: "https://models.example.test/v1"}}, wire: WireResponses, endpoint: "https://models.example.test/v1"},
		{name: "custom without wire", binding: contracts.ModelBinding{Provider: "example", Model: "example", CredentialEnv: "EXAMPLE_API_KEY",
			Endpoints: contracts.ModelEndpoints{OpenAIChat: "https://models.example.test/v1"}}, wire: WireResponses, wantCode: fault.CodeModelUnavailable},
		{name: "custom insecure", binding: contracts.ModelBinding{Provider: "example", Model: "example", CredentialEnv: "EXAMPLE_API_KEY",
			Endpoints: contracts.ModelEndpoints{Responses: "http://models.example.test/v1"}}, wire: WireResponses, wantCode: fault.CodeModelUnavailable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, endpoint, err := ProviderFor(tc.binding, tc.wire)
			if tc.wantCode != "" {
				if got := fault.CodeOf(err); got != tc.wantCode {
					t.Fatalf("error code %q, want %q: %v", got, tc.wantCode, err)
				}
				return
			}
			if err != nil || endpoint != tc.endpoint {
				t.Fatalf("endpoint %q, want %q: %v", endpoint, tc.endpoint, err)
			}
		})
	}
}

func TestInvocationReadsConfiguredCredentialVariable(t *testing.T) {
	t.Setenv("TEAM_MODEL_KEY", "test-secret")
	env := contracts.EnvironmentSpec{
		Agents:        []contracts.AgentSpec{{ID: "lead", ModelBinding: "model"}},
		ModelBindings: []contracts.ModelBinding{{ID: "model", Provider: "deepseek", Model: "example", CredentialEnv: "TEAM_MODEL_KEY"}},
	}
	_, binding, credential, err := ResolveInvocation(AgentInvocation{Environment: env, AgentID: "lead"})
	if err != nil || binding.CredentialEnv != "TEAM_MODEL_KEY" || credential != "test-secret" {
		t.Fatalf("configured credential was not selected: binding=%+v err=%v", binding, err)
	}
	t.Setenv("TEAM_MODEL_KEY", "")
	if _, _, _, err := ResolveInvocation(AgentInvocation{Environment: env, AgentID: "lead"}); fault.CodeOf(err) != fault.CodeAuthUnavailable {
		t.Fatalf("missing configured credential was accepted: %v", err)
	}
}
