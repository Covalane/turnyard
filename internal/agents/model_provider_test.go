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
		{name: "built-in", binding: contracts.ModelBinding{Provider: "deepseek", Model: "example", CredentialEnv: "DEEPSEEK_API_KEY"},
			wire: WireAnthropic, endpoint: "https://api.deepseek.com/anthropic"},
		{name: "built-in override", binding: contracts.ModelBinding{Provider: "deepseek", Model: "example", CredentialEnv: "DEEPSEEK_API_KEY",
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
