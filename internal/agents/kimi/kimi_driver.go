package kimi

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Covalane/turnyard/internal/agents"
	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/sandbox"
)

type Driver struct{}

const (
	kimiModelAlias   = "turnyard-model"
	kimiProviderType = "openai"
)

func (Driver) Runtime() string {
	return agents.RuntimeKimi
}
func (Driver) ValidateEnvironment(agent contracts.AgentSpec, env contracts.EnvironmentSpec) error {
	return agents.ValidateTools(agent, env)
}
func (Driver) ValidateBinding(binding contracts.ModelBinding) error {
	_, _, err := agents.ProviderFor(binding, agents.WireOpenAIChat)
	return err
}
func kimiConfig(binding contracts.ModelBinding, agent contracts.AgentSpec, env contracts.EnvironmentSpec, state string) error {
	if err := (Driver{}).ValidateBinding(binding); err != nil {
		return err
	}
	_, baseURL, err := agents.ProviderFor(binding, agents.WireOpenAIChat)
	if err != nil {
		return err
	}
	baseURL, err = agents.GatewayEndpoint(baseURL, env.Sandbox.Network)
	if err != nil {
		return err
	}
	root := filepath.Join(state, "kimi-code")
	if err := agents.EnsureStateDirectory(state, root); err != nil {
		return err
	}
	config := "default_model = " + strconv.Quote(kimiModelAlias) + "\ntelemetry = false\nauto_session_title = false\n" + "merge_all_available_skills = false\n" + "[providers.turnyard]\n" + "type = " + strconv.Quote(kimiProviderType) + "\n" + "base_url = " + strconv.Quote(baseURL) + "\n" + "api_key_env = " + strconv.Quote(binding.CredentialEnv) + "\n" + "[models." + kimiModelAlias + "]\nprovider = \"turnyard\"\n" + "model = " + strconv.Quote(binding.Model) + "\n" + "max_context_size = 131072\ncapabilities = [\"tool_use\"]\n" + "[loop_control]\nmax_steps_per_turn = 24\n" + "[background]\nprint_background_mode = \"drain\"\n" + "[[permission.rules]]\ndecision = \"deny\"\npattern = \"Bash(git push*)\"\n"
	path := filepath.Join(root, "config.toml")
	if err := agents.WritePinnedConfiguration(state, path, []byte(config), "Kimi configuration"); err != nil {
		return err
	}
	for _, id := range agent.Skills {
		skill, err := agents.SelectedSkill(env, id)
		if err != nil {
			return err
		}
		dest := filepath.Join(root, "skills", id)
		if err := agents.InstallPinnedBundle(state, skill.Path, skill.SourceDigest, dest, "skill "+id); err != nil {
			return err
		}
	}
	launches, err := agents.PrepareTools(agent, env, state)
	if err != nil {
		return err
	}
	mcp := map[string]any{}
	for _, launch := range launches {
		mcp[launch.ID] = map[string]any{"command": launch.Argv[0], "args": launch.Argv[1:]}
	}
	if len(mcp) > 0 {
		content, err := contracts.JSONText(map[string]any{"mcpServers": mcp})
		if err != nil {
			return err
		}
		mcpPath := filepath.Join(root, "mcp.json")
		if err := agents.WritePinnedConfiguration(state, mcpPath, []byte(content), "Kimi MCP configuration"); err != nil {
			return err
		}
	}
	return nil
}
func (Driver) Invoke(ctx context.Context, input agents.AgentInvocation) (agents.AgentResult, error) {
	var output agents.AgentResult
	agent, binding, credential, err := agents.ResolveInvocation(input)
	if err != nil {
		return output, err
	}
	if err := kimiConfig(binding, agent, input.Environment, input.State); err != nil {
		return output, err
	}
	credentials := map[string]string{binding.CredentialEnv: credential}
	env := map[string]string{"KIMI_CODE_HOME": "/state/kimi-code"}
	readScope := agents.ReadOnlyScope(input.Scope)
	inventory := func(suffix string) ([]string, error) {
		probe, err := input.Sandbox.Run(ctx, sandbox.SandboxRun{Name: "ty-kimi-scan-" + input.InvocationID + suffix, Sandbox: input.Environment.Sandbox, Workspace: input.Workspace, Scope: readScope, State: input.State, EntryPoint: "kimi", Command: []string{"session", "list", "--cwd", "/workspace", "--json"}, Environment: env, Credentials: credentials, Timeout: 60 * time.Second, LogPath: input.LogPath + suffix + ".inventory.json"})
		if err != nil {
			return nil, err
		}
		if probe.TimedOut || probe.ExitCode != 0 {
			return nil, fault.New(fault.CodeNativeStateUnavailable, "could not inspect Kimi native sessions: %s", strings.TrimSpace(probe.Stderr))
		}
		return kimiSessions(probe.Output)
	}
	before, err := inventory("-before")
	if err != nil {
		return output, err
	}
	if input.NativeID != "" && !agents.SlicesContains(before, input.NativeID) {
		return output, fault.New(fault.CodeNativeSessionMismatch, "Kimi native session is unavailable in this workspace")
	}
	command := []string{"-m", kimiModelAlias, "-p", input.Prompt, "--output-format", "stream-json"}
	if input.NativeID != "" {
		command = append(command, "-S", input.NativeID)
	}
	if len(agent.Skills) > 0 {
		command = append(command, "--skills-dir", "/state/kimi-code/skills")
	}
	run, err := agents.RunMain(ctx, input, agents.WireOpenAIChat, credential, agents.RuntimeKimi, command, env, credentials)
	if err != nil {
		return output, err
	}
	if err := agents.MainRunError(run, "Kimi"); err != nil {
		return output, err
	}
	after, err := inventory("-after")
	if err != nil {
		return output, err
	}
	native := input.NativeID
	if native == "" {
		newIDs := []string{}
		for _, id := range after {
			if !agents.SlicesContains(before, id) {
				newIDs = append(newIDs, id)
			}
		}
		if len(newIDs) != 1 {
			return output, fault.New(fault.CodeNativeSessionMissing, "Kimi must create exactly one new native session, found %d", len(newIDs))
		}
		native = newIDs[0]
	} else if !agents.SlicesContains(after, native) {
		return output, fault.New(fault.CodeNativeSessionMismatch, "Kimi did not retain the resumed native session")
	}
	events, called, loaded := parseKimiEvents(run.Output)
	if len(events) == 0 {
		return output, fault.New(fault.CodeAgentOutputInvalid, "Kimi emitted no JSON events")
	}
	if err := verifyKimiModel(input.State, native, binding.Model); err != nil {
		return output, err
	}
	output.NativeID = native
	output.Output = run.Output
	output.Events = events
	output.ActualProvider = binding.Provider
	output.ActualModel = binding.Model
	output.CalledTools = called
	output.LoadedSkills = loaded
	return output, nil
}

var _ agents.AgentDriver = Driver{}
