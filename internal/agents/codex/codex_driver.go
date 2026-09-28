package codex

import (
	"context"
	"path/filepath"
	"strconv"

	"github.com/Covalane/turnyard/internal/agents"
	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
)

type Driver struct{}

func (Driver) Runtime() string {
	return agents.RuntimeCodex
}
func (Driver) ValidateEnvironment(agent contracts.AgentSpec, env contracts.EnvironmentSpec) error {
	return agents.ValidateTools(agent, env)
}
func (Driver) ValidateBinding(binding contracts.ModelBinding) error {
	_, _, err := agents.ProviderFor(binding, agents.WireResponses)
	return err
}
func prepareCodex(binding contracts.ModelBinding, agent contracts.AgentSpec, env contracts.EnvironmentSpec, state string) error {
	if err := (Driver{}).ValidateBinding(binding); err != nil {
		return err
	}
	root := filepath.Join(state, "codex")
	if err := agents.EnsureStateDirectory(state, root); err != nil {
		return err
	}
	provider, endpoint, err := agents.ProviderFor(binding, agents.WireResponses)
	if err != nil {
		return err
	}
	endpoint, err = agents.GatewayEndpoint(endpoint, env.Sandbox.Network)
	if err != nil {
		return err
	}
	config := "model = " + strconv.Quote(binding.Model) + "\n" + "model_provider = " + strconv.Quote(binding.Provider) + "\npreferred_auth_method = \"apikey\"\n" + "forced_login_method = \"api\"\nweb_search = \"disabled\"\n" + "[model_providers." + binding.Provider + "]\nname = " + strconv.Quote(provider.Name) + "\n" + "base_url = " + strconv.Quote(endpoint) + "\n" + "wire_api = \"responses\"\n" + "env_key = " + strconv.Quote(binding.CredentialEnv) + "\n"
	for _, id := range agent.Skills {
		skill, err := agents.SelectedSkill(env, id)
		if err != nil {
			return err
		}
		if err := agents.InstallPinnedBundle(state, skill.Path, skill.SourceDigest, filepath.Join(state, "home", ".agents", "skills", id), "skill "+id); err != nil {
			return err
		}
	}
	launches, err := agents.PrepareTools(agent, env, state)
	if err != nil {
		return err
	}
	for _, launch := range launches {
		config += "[mcp_servers." + strconv.Quote(launch.ID) + "]\n" + "command = " + strconv.Quote(launch.Argv[0]) + "\nargs = ["
		for i, arg := range launch.Argv[1:] {
			if i > 0 {
				config += ", "
			}
			config += strconv.Quote(arg)
		}
		config += "]\n"
		if len(launch.EnvVars) > 0 {
			config += "env_vars = ["
			for i, name := range launch.EnvVars {
				if i > 0 {
					config += ", "
				}
				config += strconv.Quote(name)
			}
			config += "]\n"
		}
	}
	path := filepath.Join(root, "config.toml")
	return agents.WritePinnedConfiguration(state, path, []byte(config), "Codex configuration")
}
func (Driver) Invoke(ctx context.Context, input agents.AgentInvocation) (agents.AgentResult, error) {
	var result agents.AgentResult
	agent, binding, credential, err := agents.ResolveInvocation(input)
	if err != nil {
		return result, err
	}
	if err := prepareCodex(binding, agent, input.Environment, input.State); err != nil {
		return result, err
	}
	command := []string{"exec"}
	if input.NativeID != "" {
		command = append(command, "resume", "--json", "--dangerously-bypass-approvals-and-sandbox", "--skip-git-repo-check", "-m", binding.Model, input.NativeID, input.Prompt)
	} else {
		command = append(command, "--json", "--dangerously-bypass-approvals-and-sandbox", "--skip-git-repo-check", "-m", binding.Model, "-C", "/workspace", input.Prompt)
	}
	run, err := agents.RunMain(ctx, input, agents.WireResponses, credential, agents.RuntimeCodex, command, map[string]string{"CODEX_HOME": "/state/codex"}, map[string]string{binding.CredentialEnv: credential})
	if err != nil {
		return result, err
	}
	if err := agents.MainRunError(run, "Codex"); err != nil {
		return result, err
	}
	result, err = parseCodexEvents(run.Output)
	if err != nil {
		return result, err
	}
	if input.NativeID != "" && result.NativeID != input.NativeID {
		return agents.AgentResult{}, fault.New(fault.CodeNativeSessionMismatch, "Codex did not resume the pinned native thread")
	}
	if err := verifyCodexModel(input.State, result.NativeID, binding.Provider, binding.Model); err != nil {
		return agents.AgentResult{}, err
	}
	result.ActualProvider = binding.Provider
	result.ActualModel = binding.Model
	return result, nil
}

var _ agents.AgentDriver = Driver{}
