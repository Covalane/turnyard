package registry

import (
	"testing"

	"github.com/Covalane/turnyard/internal/agents/codex"
	"github.com/Covalane/turnyard/internal/agents/opencode"

	"github.com/Covalane/turnyard/internal/contracts"
)

func TestProviderTransportIsIndependentOfRuntime(t *testing.T) {
	tests := []struct {
		runtime  string
		provider string
		key      string
		allowed  bool
	}{
		{"opencode", "ollama-cloud", "MODEL_API_KEY", true},
		{"opencode", "ollama-cloud", "ALTERNATE_MODEL_KEY", true},
		{"opencode", "deepseek", "MODEL_API_KEY", true},
		{"kimi", "openai", "MODEL_API_KEY", true},
		{"claude", "deepseek", "MODEL_API_KEY", true},
		{"codex", "deepseek", "MODEL_API_KEY", true},
		{"codex", "ollama-cloud", "MODEL_API_KEY", false},
		{"claude", "openai", "MODEL_API_KEY", false},
		{"opencode", "deepseek", "TEAM_MODEL_KEY", true},
	}
	for _, test := range tests {
		driver, err := Driver(test.runtime)
		if err != nil {
			t.Fatal(err)
		}
		err = driver.ValidateBinding(contracts.ModelBinding{Provider: test.provider, CredentialEnv: test.key, Model: "example"})
		if (err == nil) != test.allowed {
			t.Errorf("%s + %s allowed=%t: %v", test.runtime, test.provider, test.allowed, err)
		}
	}
}

func TestCustomProviderRequiresExplicitHTTPSWireEndpoint(t *testing.T) {
	binding := contracts.ModelBinding{Provider: "custom", CredentialEnv: "CUSTOM_API_KEY", Model: "custom-model",
		Endpoints: contracts.ModelEndpoints{OpenAIChat: "https://models.example.test/v1"}}
	if err := (opencode.Driver{}).ValidateBinding(binding); err != nil {
		t.Fatalf("custom OpenAI-compatible provider was rejected: %v", err)
	}
	if err := (codex.Driver{}).ValidateBinding(binding); err == nil {
		t.Fatal("custom provider without Responses endpoint was accepted by Codex")
	}
	binding.Endpoints.OpenAIChat = "http://models.example.test/v1"
	if err := (opencode.Driver{}).ValidateBinding(binding); err == nil {
		t.Fatal("plain HTTP model endpoint was accepted")
	}
	binding.Endpoints.OpenAIChat = "https://user:password@models.example.test/v1"
	if err := (opencode.Driver{}).ValidateBinding(binding); err == nil {
		t.Fatal("model endpoint with embedded credentials was accepted")
	}
}
