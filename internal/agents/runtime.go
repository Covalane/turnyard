package agents

import (
	"context"
	"os"
	"strings"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/sandbox"
)

// Each CLI owns its command and evidence parser; the host owns sandbox execution.
func ResolveInvocation(input AgentInvocation) (contracts.AgentSpec, contracts.ModelBinding, string, error) {
	agent, binding, err := SelectAgent(input.Environment, input.AgentID)
	if err != nil {
		return agent, binding, "", err
	}
	credential := os.Getenv(binding.CredentialEnv)
	if credential == "" {
		return agent, binding, "", fault.New(fault.CodeAuthUnavailable, "%s is not set", binding.CredentialEnv)
	}
	return agent, binding, credential, nil
}
func ReadOnlyScope(scope []contracts.ScopeRepo) []contracts.ScopeRepo {
	out := make([]contracts.ScopeRepo, len(scope))
	for i, r := range scope {
		out[i] = contracts.ScopeRepo{ID: r.ID, Mode: contracts.ScopeRead}
	}
	return out
}
func RunMain(ctx context.Context, input AgentInvocation, wire ModelWire, modelCredential, entryPoint string, command []string, environment, credentials map[string]string) (sandbox.SandboxResult, error) {
	agent, binding, err := SelectAgent(input.Environment, input.AgentID)
	if err != nil {
		return sandbox.SandboxResult{}, err
	}
	var gateway *sandbox.ModelGateway
	if input.Environment.Sandbox.Network == contracts.SandboxNetworkModelOnly {
		_, endpoint, err := ProviderFor(binding, wire)
		if err != nil {
			return sandbox.SandboxResult{}, err
		}
		if modelCredential == "" {
			return sandbox.SandboxResult{}, fault.New(fault.CodeAuthUnavailable, "model credential is unavailable")
		}
		for _, value := range credentials {
			if value != modelCredential {
				return sandbox.SandboxResult{}, fault.New(fault.CodeCapabilityMissing, "model-only cannot expose tool credentials to an agent")
			}
		}
		gateway = &sandbox.ModelGateway{Endpoint: endpoint, Credential: modelCredential}
		credentials = cloneEnvironment(credentials)
		for name := range credentials {
			credentials[name] = sandbox.ModelCredentialPlaceholder
		}
	}
	toolGateway, err := ToolGatewayFor(input, agent)
	if err != nil {
		return sandbox.SandboxResult{}, err
	}
	credentials, err = RuntimeCredentials(agent, input.Environment, credentials)
	if err != nil {
		return sandbox.SandboxResult{}, err
	}
	if input.ControlDir != "" {
		environment = cloneEnvironment(environment)
		environment["TURNYARD_DELEGATION_INVOCATION"] = input.InvocationID
	}
	var catalogDir string
	if toolGateway == nil && (len(agent.Tools) > 0 || len(agent.Delegates) > 0) {
		catalogDir = ToolCatalogDir(input.State)
	}
	return input.Sandbox.Run(ctx, sandbox.SandboxRun{Name: "ty-" + input.InvocationID, Sandbox: input.Environment.Sandbox, Workspace: input.Workspace, Scope: input.Scope, State: input.State, CatalogDir: catalogDir, ArtifactDir: input.ArtifactDir, InputDir: input.InputDir, ControlDir: input.ControlDir, EntryPoint: entryPoint, Command: command, Environment: environment, Credentials: credentials, ModelGateway: gateway, ToolGateway: toolGateway, Timeout: input.Timeout, LogPath: input.LogPath})
}

func cloneEnvironment(source map[string]string) map[string]string {
	result := make(map[string]string, len(source)+1)
	for key, value := range source {
		result[key] = value
	}
	return result
}
func MainRunError(run sandbox.SandboxResult, runtime string) error {
	if run.TimedOut {
		return fault.New(fault.CodeAgentTimeout, "%s invocation timed out", runtime)
	}
	if run.ExitCode == 0 {
		return nil
	}
	output := run.Output + run.Stderr
	if len(output) > 700 {
		output = output[len(output)-700:]
	}
	return fault.New(fault.CodeAgentFailed, "%s exited %d: %s", runtime, run.ExitCode, strings.ReplaceAll(output, "\n", " "))
}
