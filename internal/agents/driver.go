package agents

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/sandbox"
)

type AgentResult struct {
	NativeID       string
	Output         string
	Events         []json.RawMessage
	NeedsInput     string
	ExitCode       int
	ActualProvider string
	ActualModel    string
	ConnectedTools []string
	LoadedSkills   []string
	CalledTools    []string
}
type AgentInvocation struct {
	Sandbox      sandbox.SandboxBackend
	Environment  contracts.EnvironmentSpec
	AgentID      string
	Workspace    string
	Scope        []contracts.ScopeRepo
	State        string
	ArtifactDir  string
	InputDir     string
	ControlDir   string
	NativeID     string
	Prompt       string
	InvocationID string
	LogPath      string
	Timeout      time.Duration
}
type AgentDriver interface {
	Runtime() string
	ValidateBinding(contracts.ModelBinding) error
	ValidateEnvironment(contracts.AgentSpec, contracts.EnvironmentSpec) error
	Invoke(context.Context, AgentInvocation) (AgentResult, error)
}

func SelectAgent(env contracts.EnvironmentSpec, id string) (contracts.AgentSpec, contracts.ModelBinding, error) {
	var agent contracts.AgentSpec
	found := false
	for _, a := range env.Agents {
		if a.ID == id {
			agent = a
			found = true
			break
		}
	}
	if !found {
		return agent, contracts.ModelBinding{}, fault.New(fault.CodeInvalidSpec, "agent %s not found", id)
	}
	for _, b := range env.ModelBindings {
		if b.ID == agent.ModelBinding {
			return agent, b, nil
		}
	}
	return agent, contracts.ModelBinding{}, fault.New(fault.CodeInvalidSpec, "model binding %s not found", agent.ModelBinding)
}
