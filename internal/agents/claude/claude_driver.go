package claude

import (
	"context"
	"path/filepath"

	"github.com/Covalane/turnyard/internal/agents"
	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
)

type Driver struct{}

func (Driver) Runtime() string {
	return agents.RuntimeClaude
}
func (Driver) ValidateEnvironment(agent contracts.AgentSpec, env contracts.EnvironmentSpec) error {
	return agents.ValidateTools(agent, env)
}
func (Driver) ValidateBinding(binding contracts.ModelBinding) error {
	_, _, err := agents.ProviderFor(binding, agents.WireAnthropic)
	return err
}
func (Driver) Invoke(ctx context.Context, input agents.AgentInvocation) (agents.AgentResult, error) {
	var result agents.AgentResult
	agent, binding, credential, err := agents.ResolveInvocation(input)
	if err != nil {
		return result, err
	}
	_, endpoint, err := agents.ProviderFor(binding, agents.WireAnthropic)
	if err != nil {
		return result, err
	}
	endpoint, err = agents.GatewayEndpoint(endpoint, input.Environment.Sandbox.Network)
	if err != nil {
		return result, err
	}
	if err := agents.EnsureStateDirectory(input.State, filepath.Join(input.State, "claude")); err != nil {
		return result, err
	}
	for _, id := range agent.Skills {
		skill, err := agents.SelectedSkill(input.Environment, id)
		if err != nil {
			return result, err
		}
		if err := agents.InstallPinnedBundle(input.State, skill.Path, skill.SourceDigest, filepath.Join(input.State, "claude", "skills", id), "skill "+id); err != nil {
			return result, err
		}
	}
	launches, err := agents.PrepareTools(agent, input.Environment, input.State)
	if err != nil {
		return result, err
	}
	mcp := map[string]any{}
	for _, launch := range launches {
		mcp[launch.ID] = map[string]any{"command": launch.Argv[0], "args": launch.Argv[1:]}
	}
	if len(mcp) > 0 {
		path := filepath.Join(input.State, "claude", "mcp.json")
		content, err := contracts.JSONText(map[string]any{"mcpServers": mcp})
		if err != nil {
			return result, err
		}
		if err := agents.WritePinnedConfiguration(input.State, path, []byte(content), "Claude MCP configuration"); err != nil {
			return result, err
		}
	}
	env := map[string]string{"CLAUDE_CONFIG_DIR": "/state/claude", "ANTHROPIC_BASE_URL": endpoint, "ANTHROPIC_MODEL": binding.Model, "ANTHROPIC_DEFAULT_OPUS_MODEL": binding.Model, "ANTHROPIC_DEFAULT_SONNET_MODEL": binding.Model, "ANTHROPIC_DEFAULT_HAIKU_MODEL": binding.Model, "CLAUDE_CODE_SUBAGENT_MODEL": binding.Model, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"}
	cmd := []string{"--setting-sources", "user", "--strict-mcp-config", "--dangerously-skip-permissions", "--print", "--verbose", "--output-format", "stream-json", "--model", binding.Model}
	if input.NativeID != "" {
		cmd = append(cmd, "--resume", input.NativeID)
	}
	cmd = append(cmd, input.Prompt)
	if len(mcp) > 0 {
		cmd = append(cmd, "--mcp-config", "/state/claude/mcp.json")
	}
	run, err := agents.RunMain(ctx, input, agents.WireAnthropic, credential, agents.RuntimeClaude, cmd, env, map[string]string{"ANTHROPIC_API_KEY": credential, "ANTHROPIC_AUTH_TOKEN": credential})
	if err != nil {
		return result, err
	}
	if err := agents.MainRunError(run, "Claude"); err != nil {
		return result, err
	}
	result, err = parseClaudeEvents(run.Output, binding.Model)
	if err != nil {
		return result, err
	}
	if input.NativeID != "" && result.NativeID != input.NativeID {
		return agents.AgentResult{}, fault.New(fault.CodeNativeSessionMismatch, "Claude did not resume the pinned native session")
	}
	result.ActualProvider = binding.Provider
	return result, nil
}

var _ agents.AgentDriver = Driver{}
