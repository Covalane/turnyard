package agents

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/sandbox"
	"github.com/Covalane/turnyard/internal/toolgateway"
)

const executableBridge = "/usr/local/bin/turnyard-tool-bridge"
const gatewayBinary = "/usr/local/bin/turnyard-tool-gateway"
const DelegationToolID = contracts.DelegationToolID

// ToolCatalogDir lives beside agent-state so restoring a checkpoint cannot
// erase the first observed tool catalog for this session.
func ToolCatalogDir(state string) string {
	return filepath.Join(filepath.Dir(state), "tool-catalog")
}

// ToolLaunch is the runtime-independent launch description. Each driver only
// translates it into its native local MCP configuration.
type ToolLaunch struct {
	ID      string
	Argv    []string
	EnvVars []string
}

func ValidateTools(agent contracts.AgentSpec, env contracts.EnvironmentSpec) error {
	if env.ToolSearch != nil && env.ToolSearch.Mode == contracts.ToolSearchLLMRerank && env.Sandbox.Network == contracts.SandboxNetworkNone {
		return fault.New(fault.CodeCapabilityMissing, "model-assisted tool search requires network access")
	}
	for _, id := range agent.Tools {
		tool, err := selectedTool(env, id)
		if err != nil {
			return err
		}
		if tool.Kind != contracts.ToolKindMCP && tool.Kind != contracts.ToolKindExecutable {
			return fault.New(fault.CodeCapabilityMissing, "agent %s cannot inject tool kind %s", agent.ID, tool.Kind)
		}
		if env.Sandbox.Network == contracts.SandboxNetworkModelOnly && env.Sandbox.Backend != sandbox.BackendDocker && len(tool.PassEnv) > 0 {
			return fault.New(fault.CodeCapabilityMissing, "model-only cannot expose tool %s credentials to the agent", id)
		}
	}
	return nil
}

func PrepareTools(agent contracts.AgentSpec, env contracts.EnvironmentSpec, state string) ([]ToolLaunch, error) {
	backends := make([]toolgateway.Backend, 0, len(agent.Tools)+1)
	for _, id := range agent.Tools {
		tool, err := selectedTool(env, id)
		if err != nil {
			return nil, err
		}
		if tool.Path != "" {
			if err := InstallPinnedBundle(state, tool.Path, tool.SourceDigest, filepath.Join(state, "tools", id), "tool "+id); err != nil {
				return nil, err
			}
		}
		argv := make([]string, len(tool.Argv))
		for i, arg := range tool.Argv {
			argv[i] = strings.ReplaceAll(arg, "{toolDir}", "/state/tools/"+id)
		}
		if len(argv) == 0 || argv[0] == "" {
			return nil, fault.New(fault.CodeInvalidSpec, "tool %s has no command", id)
		}
		switch tool.Kind {
		case contracts.ToolKindMCP:
			backends = append(backends, toolgateway.Backend{ID: id, Argv: argv, PassEnv: tool.PassEnv, TimeoutSeconds: tool.TimeoutSeconds})
		case contracts.ToolKindExecutable:
			configPath := filepath.Join(state, "tool-config", id+".json")
			if err := EnsureStateDirectory(state, filepath.Dir(configPath)); err != nil {
				return nil, err
			}
			config := contracts.JSONText(struct {
				Description    string   `json:"description"`
				Argv           []string `json:"argv"`
				PassEnv        []string `json:"passEnv,omitempty"`
				TimeoutSeconds int      `json:"timeoutSeconds,omitempty"`
			}{tool.Description, argv, tool.PassEnv, tool.TimeoutSeconds})
			if err := WritePinnedConfiguration(state, configPath, []byte(config), "executable tool "+id); err != nil {
				return nil, err
			}
			backends = append(backends, toolgateway.Backend{ID: id, Argv: []string{executableBridge, "--config", "/state/tool-config/" + id + ".json"}, PassEnv: tool.PassEnv, TimeoutSeconds: tool.TimeoutSeconds})
		default:
			return nil, fault.New(fault.CodeCapabilityMissing, "tool %s has unsupported kind %s", id, tool.Kind)
		}
	}
	if len(agent.Delegates) > 0 {
		backends = append(backends, toolgateway.Backend{ID: DelegationToolID, Argv: []string{executableBridge, "--delegate"}, PassEnv: []string{"TURNYARD_DELEGATION_INVOCATION"}})
	}
	if len(backends) == 0 {
		return nil, nil
	}
	root := filepath.Join(state, "tool-gateway")
	if err := EnsureStateDirectory(state, root); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(ToolCatalogDir(state), 0o700); err != nil {
		return nil, err
	}
	config := toolgateway.Config{Backends: backends, CatalogLock: "/turnyard-catalog/" + agent.ID + ".sha256"}
	if env.ToolSearch != nil && env.ToolSearch.Mode == contracts.ToolSearchLLMRerank {
		matcher, err := toolSearchMatcher(env)
		if err != nil {
			return nil, err
		}
		config.Matcher = matcher
	}
	path := filepath.Join(root, agent.ID+".json")
	if err := WritePinnedConfiguration(state, path, []byte(contracts.JSONText(config)), "tool gateway configuration"); err != nil {
		return nil, err
	}
	argv := []string{gatewayBinary, "local", "--config", "/state/tool-gateway/" + agent.ID + ".json"}
	if usesToolSidecar(env) {
		argv = []string{gatewayBinary, "proxy", "--address", "turnyard-tools:8081"}
	}
	return []ToolLaunch{{ID: toolgateway.ServerID, Argv: argv}}, nil
}

func toolSearchMatcher(env contracts.EnvironmentSpec) (*toolgateway.MatcherConfig, error) {
	for _, binding := range env.ModelBindings {
		if binding.ID == env.ToolSearch.ModelBinding {
			_, endpoint, err := ProviderFor(binding, WireOpenAIChat)
			if err != nil {
				return nil, err
			}
			return &toolgateway.MatcherConfig{Endpoint: endpoint, Model: binding.Model, CredentialEnv: binding.CredentialEnv}, nil
		}
	}
	return nil, fault.New(fault.CodeInvalidSpec, "tool search model binding is unavailable")
}

func usesToolSidecar(env contracts.EnvironmentSpec) bool {
	return env.Sandbox.Backend == sandbox.BackendDocker && env.Sandbox.Network != contracts.SandboxNetworkNone
}

func requiredCredential(capability, name string) (string, error) {
	value, exists := os.LookupEnv(name)
	if !exists || value == "" {
		return "", fault.New(fault.CodeAuthUnavailable, "%s requires %s", capability, name)
	}
	return value, nil
}

// ToolGatewayFor gives the sandbox the credentialed gateway configuration.
// The agent-side MCP proxy receives neither these values nor backend commands.
func ToolGatewayFor(input AgentInvocation, agent contracts.AgentSpec) (*sandbox.ToolGateway, error) {
	if !usesToolSidecar(input.Environment) || len(agent.Tools) == 0 && len(agent.Delegates) == 0 {
		return nil, nil
	}
	credentials := make(map[string]string)
	for _, id := range agent.Tools {
		tool, err := selectedTool(input.Environment, id)
		if err != nil {
			return nil, err
		}
		for _, name := range tool.PassEnv {
			value, err := requiredCredential("tool "+id, name)
			if err != nil {
				return nil, err
			}
			credentials[name] = value
		}
	}
	if input.Environment.ToolSearch != nil && input.Environment.ToolSearch.Mode == contracts.ToolSearchLLMRerank {
		matcher, err := toolSearchMatcher(input.Environment)
		if err != nil {
			return nil, err
		}
		value, err := requiredCredential("tool search", matcher.CredentialEnv)
		if err != nil {
			return nil, err
		}
		credentials[matcher.CredentialEnv] = value
	}
	return &sandbox.ToolGateway{ConfigPath: filepath.Join(input.State, "tool-gateway", agent.ID+".json"), CatalogDir: ToolCatalogDir(input.State), Credentials: credentials, InvocationID: input.InvocationID}, nil
}

func RuntimeCredentials(agent contracts.AgentSpec, env contracts.EnvironmentSpec, initial map[string]string) (map[string]string, error) {
	result := make(map[string]string, len(initial))
	for name, value := range initial {
		result[name] = value
	}
	for _, id := range agent.Tools {
		if usesToolSidecar(env) {
			continue
		}
		tool, err := selectedTool(env, id)
		if err != nil {
			return nil, err
		}
		for _, name := range tool.PassEnv {
			if env.Sandbox.Network == contracts.SandboxNetworkModelOnly {
				return nil, fault.New(fault.CodeCapabilityMissing, "model-only cannot expose tool %s credentials to the agent", id)
			}
			value, err := requiredCredential("tool "+id, name)
			if err != nil {
				return nil, err
			}
			if existing, ok := result[name]; ok && existing != value {
				return nil, fault.New(fault.CodeInvalidSpec, "tool %s credential conflicts with model binding", id)
			}
			result[name] = value
		}
	}
	if env.ToolSearch != nil && env.ToolSearch.Mode == contracts.ToolSearchLLMRerank && !usesToolSidecar(env) {
		matcher, err := toolSearchMatcher(env)
		if err != nil {
			return nil, err
		}
		value, err := requiredCredential("tool search", matcher.CredentialEnv)
		if err != nil {
			return nil, err
		}
		result[matcher.CredentialEnv] = value
	}
	return result, nil
}
