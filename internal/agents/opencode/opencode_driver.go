package opencode

import (
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
	"time"

	"github.com/Covalane/turnyard/internal/agents"
	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/sandbox"
	"github.com/Covalane/turnyard/internal/toolgateway"
)

func prepareOpenCode(env contracts.EnvironmentSpec, agent contracts.AgentSpec, binding contracts.ModelBinding, state string) (string, error) {
	if err := (Driver{}).ValidateBinding(binding); err != nil {
		return "", err
	}
	provider, endpoint, err := agents.ProviderFor(binding, agents.WireOpenAIChat)
	if err != nil {
		return "", err
	}
	endpoint, err = agents.GatewayEndpoint(endpoint, env.Sandbox.Network)
	if err != nil {
		return "", err
	}
	config := map[string]any{"$schema": "https://opencode.ai/config.json", "provider": map[string]any{binding.Provider: map[string]any{"npm": "@ai-sdk/openai-compatible", "name": provider.Name, "options": map[string]any{"baseURL": endpoint, "apiKey": "{env:" + binding.CredentialEnv + "}"}, "models": map[string]any{binding.Model: map[string]any{"name": binding.Model}}}}}
	if len(agent.Delegates) > 0 {
		// Delegation handoffs live on a read-only mount outside the workspace.
		// OpenCode also requires explicit permission to read that external path.
		config["permission"] = map[string]any{
			"external_directory": map[string]string{"/turnyard-control/delegations/**": "allow"},
			"edit":               map[string]string{"/turnyard-control/delegations/**": "deny"},
		}
	}
	previous := contracts.JSONText(config)
	configDir := filepath.Join(state, "config", "opencode")
	if err := agents.EnsureStateDirectory(state, configDir); err != nil {
		return "", err
	}
	launches, err := agents.PrepareTools(agent, env, state)
	if err != nil {
		return "", err
	}
	if len(launches) > 0 {
		mcp := map[string]any{}
		for _, launch := range launches {
			mcp[launch.ID] = map[string]any{"type": "local", "command": launch.Argv}
		}
		config["mcp"] = mcp
	}
	expected := contracts.JSONText(config)
	configPath := filepath.Join(configDir, "opencode.json")
	if len(agent.Tools) == 0 && len(agent.Delegates) == 0 {
		if err := agents.WritePinnedAdditiveConfiguration(state, configPath, []byte(expected), []byte(previous), "OpenCode configuration"); err != nil {
			return "", err
		}
	} else if err := agents.WritePinnedConfiguration(state, configPath, []byte(expected), "OpenCode configuration"); err != nil {
		return "", err
	}
	for _, id := range agent.Skills {
		skill, err := agents.SelectedSkill(env, id)
		if err != nil {
			return "", err
		}
		dest := filepath.Join(configDir, "skills", id)
		if err := agents.InstallPinnedBundle(state, skill.Path, skill.SourceDigest, dest, "skill "+id); err != nil {
			return "", err
		}
	}
	return binding.Provider + "/" + binding.Model, nil
}

type Driver struct{}

func (Driver) ValidateEnvironment(agent contracts.AgentSpec, env contracts.EnvironmentSpec) error {
	return agents.ValidateTools(agent, env)
}
func (Driver) ValidateBinding(binding contracts.ModelBinding) error {
	_, _, err := agents.ProviderFor(binding, agents.WireOpenAIChat)
	return err
}
func (Driver) Runtime() string {
	return agents.RuntimeOpenCode
}
func (Driver) Invoke(ctx context.Context, input agents.AgentInvocation) (agents.AgentResult, error) {
	var output agents.AgentResult
	agent, binding, credential, err := agents.ResolveInvocation(input)
	if err != nil {
		return output, err
	}
	model, err := prepareOpenCode(input.Environment, agent, binding, input.State)
	if err != nil {
		return output, err
	}
	readScope := agents.ReadOnlyScope(input.Scope)
	if len(agent.Tools) > 0 || len(agent.Delegates) > 0 {
		toolCredentials, err := agents.RuntimeCredentials(agent, input.Environment, map[string]string{binding.CredentialEnv: credential})
		if err != nil {
			return output, err
		}
		gateway, err := agents.ToolGatewayFor(input, agent)
		if err != nil {
			return output, err
		}
		var catalogDir string
		if gateway == nil {
			catalogDir = agents.ToolCatalogDir(input.State)
		}
		inventory, err := input.Sandbox.Run(ctx, sandbox.SandboxRun{Name: "ty-tools-" + input.InvocationID, Sandbox: input.Environment.Sandbox, Workspace: input.Workspace, Scope: readScope, State: input.State, CatalogDir: catalogDir, ControlDir: input.ControlDir, Command: []string{"mcp", "list"}, Credentials: toolCredentials, ToolGateway: gateway, Timeout: 90 * time.Second, LogPath: input.LogPath + ".tools.txt"})
		if err != nil {
			return output, err
		}
		if inventory.TimedOut || inventory.ExitCode != 0 {
			return output, fault.New(fault.CodeToolUnavailable, "OpenCode MCP handshake failed")
		}
		clean := regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(inventory.Output, "")
		if !regexp.MustCompile(`✓\s+` + regexp.QuoteMeta(toolgateway.ServerID) + `\s+connected\b`).MatchString(clean) {
			return output, fault.New(fault.CodeToolUnavailable, "Turnyard tool gateway is not connected")
		}
		output.ConnectedTools = []string{toolgateway.ServerID}
	}
	if input.NativeID != "" {
		inventory, err := input.Sandbox.Run(ctx, sandbox.SandboxRun{Name: "ty-scan-" + input.InvocationID, Sandbox: input.Environment.Sandbox, Workspace: input.Workspace, Scope: readScope, State: input.State, Command: []string{"session", "list", "--format", "json"}, Credentials: map[string]string{binding.CredentialEnv: credential}, Timeout: 60 * time.Second, LogPath: input.LogPath + ".inventory.json"})
		if err != nil {
			return output, err
		}
		if inventory.TimedOut || inventory.ExitCode != 0 {
			return output, fault.New(fault.CodeNativeStateUnavailable, "could not inspect OpenCode native sessions")
		}
		var sessions []struct {
			ID        string `json:"id"`
			Directory string `json:"directory"`
		}
		if err := json.Unmarshal([]byte(inventory.Output), &sessions); err != nil {
			return output, fault.New(fault.CodeNativeStateUnavailable, "OpenCode session inventory is not JSON")
		}
		matched := false
		for _, session := range sessions {
			if session.ID == input.NativeID && session.Directory == "/workspace" {
				matched = true
			}
		}
		if !matched {
			return output, fault.New(fault.CodeNativeSessionMismatch, "native session does not belong to /workspace")
		}
	}
	command := []string{"run", "--dir", "/workspace", "--format", "json", "--model", model}
	if input.NativeID != "" {
		command = append(command, "--session", input.NativeID)
	}
	command = append(command, input.Prompt)
	run, err := agents.RunMain(ctx, input, agents.WireOpenAIChat, credential, "", command, nil, map[string]string{binding.CredentialEnv: credential})
	if err != nil {
		return output, err
	}
	if run.TimedOut {
		return output, agents.MainRunError(run, "OpenCode")
	}
	parsed, err := parseOpenCodeEvents(run.Output)
	if err != nil {
		return output, err
	}
	if err := agents.MainRunError(run, "OpenCode"); err != nil {
		return output, err
	}
	if parsed.NativeID == "" {
		return output, fault.New(fault.CodeNativeSessionMissing, "OpenCode returned no native session ID")
	}
	if input.NativeID != "" && parsed.NativeID != input.NativeID {
		return output, fault.New(fault.CodeNativeSessionMismatch, "OpenCode did not resume native session")
	}
	provider, actual, err := verifyOpenCodeModel(ctx, input.State, parsed.NativeID, binding.Provider, binding.Model)
	if err != nil {
		return output, err
	}
	output.NativeID = parsed.NativeID
	output.Output = run.Output
	output.Events = parsed.Events
	output.ExitCode = 0
	output.ActualProvider = provider
	output.ActualModel = actual
	output.LoadedSkills = parsed.LoadedSkills
	output.CalledTools = parsed.CalledTools
	return output, nil
}
