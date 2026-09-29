package agents

import (
	"net/url"
	"regexp"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
)

const modelGatewayHost = "turnyard-model:8080"

func GatewayEndpoint(endpoint string, network contracts.SandboxNetworkPolicy) (string, error) {
	if network != contracts.SandboxNetworkModelOnly {
		return endpoint, nil
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fault.New(fault.CodeModelUnavailable, "model-only requires a plain HTTPS provider endpoint")
	}
	u.Scheme, u.Host = "http", modelGatewayHost
	return u.String(), nil
}

// Model providers describe API transports independently of agent runtimes.
// A runtime still has to support the selected wire protocol and verify its
// native model evidence; a provider entry alone does not establish that.
type ModelWire string

const (
	WireOpenAIChat ModelWire = "openai-chat"
	WireAnthropic  ModelWire = "anthropic"
	WireResponses  ModelWire = "responses"
)

type ModelProvider struct {
	Name      string
	Endpoints map[ModelWire]string
}

// A model binding chooses the host variable; provider profiles never pin a
// deployment's credential names. Keep this in sync with environment.json.
var modelCredentialEnvPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,79}$`)

// ModelProviderCatalog owns lookup and built-in versus custom resolution.
type ModelProviderCatalog map[string]ModelProvider

var modelProviders = ModelProviderCatalog{
	"ollama-cloud": {
		Name:      "Ollama Cloud",
		Endpoints: map[ModelWire]string{WireOpenAIChat: "https://ollama.com/v1"},
	},
	"deepseek": {
		Name: "DeepSeek",
		Endpoints: map[ModelWire]string{
			WireOpenAIChat: "https://api.deepseek.com", WireAnthropic: "https://api.deepseek.com/anthropic",
			WireResponses: "https://api.deepseek.com/",
		},
	},
	"openai": {
		Name:      "OpenAI",
		Endpoints: map[ModelWire]string{WireOpenAIChat: "https://api.openai.com/v1"},
	},
}

type providerOrigin uint8

const (
	providerBuiltIn providerOrigin = iota
	providerCustom
)

// resolvedProvider keeps the provider's policy alongside its API profile.
// Custom providers accept validated endpoint bindings; built-in providers do not.
type resolvedProvider struct {
	profile ModelProvider
	origin  providerOrigin
}

func (catalog ModelProviderCatalog) builtIn(name string) (ModelProvider, bool) {
	profile, exists := catalog[name]
	return profile, exists
}

func (catalog ModelProviderCatalog) resolve(binding contracts.ModelBinding) resolvedProvider {
	if profile, exists := catalog.builtIn(binding.Provider); exists {
		return resolvedProvider{profile: profile, origin: providerBuiltIn}
	}
	return resolvedProvider{origin: providerCustom, profile: ModelProvider{
		Name: binding.Provider, Endpoints: map[ModelWire]string{
			WireOpenAIChat: binding.Endpoints.OpenAIChat, WireAnthropic: binding.Endpoints.Anthropic,
			WireResponses: binding.Endpoints.Responses,
		},
	}}
}

func (provider resolvedProvider) endpointFor(binding contracts.ModelBinding, wire ModelWire) (string, error) {
	if provider.origin == providerBuiltIn && binding.Endpoints != (contracts.ModelEndpoints{}) {
		return "", fault.New(fault.CodeModelUnavailable, "built-in provider %s does not accept endpoint overrides", binding.Provider)
	}
	endpoint := provider.profile.Endpoints[wire]
	if endpoint == "" {
		return "", fault.New(fault.CodeModelUnavailable, "provider %s does not support %s", binding.Provider, wire)
	}
	if provider.origin == providerCustom && !contracts.ValidModelEndpoint(endpoint) {
		return "", fault.New(fault.CodeModelUnavailable, "custom provider %s requires a plain HTTPS endpoint", binding.Provider)
	}
	return endpoint, nil
}

func ProviderFor(binding contracts.ModelBinding, wire ModelWire) (ModelProvider, string, error) {
	if binding.Model == "" || !modelCredentialEnvPattern.MatchString(binding.CredentialEnv) {
		return ModelProvider{}, "", fault.New(fault.CodeModelUnavailable, "provider %s has no valid model and credential binding", binding.Provider)
	}
	provider := modelProviders.resolve(binding)
	endpoint, err := provider.endpointFor(binding, wire)
	if err != nil {
		return ModelProvider{}, "", err
	}
	return provider.profile, endpoint, nil
}
