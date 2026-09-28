package engine

import (
	"github.com/Covalane/turnyard/internal/agents"
	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/gitstate"
	"github.com/Covalane/turnyard/internal/sandbox"
	"github.com/Covalane/turnyard/internal/store"
)

type SessionSpec = contracts.SessionSpec
type EnvironmentSpec = contracts.EnvironmentSpec
type WorkSpec = contracts.WorkSpec
type AgentSpec = contracts.AgentSpec
type ScopeRepo = contracts.ScopeRepo
type RepositorySpec = contracts.RepositorySpec
type CheckSpec = contracts.CheckSpec
type RepoStatus = gitstate.RepoStatus
type RepoVersion = gitstate.RepoVersion
type SandboxBackend = sandbox.SandboxBackend
type SandboxRun = sandbox.SandboxRun
type AgentDriver = agents.AgentDriver
type AgentInvocation = agents.AgentInvocation
type SessionRow = store.SessionRow
type TaskRow = store.TaskRow
type SandboxSpec = contracts.SandboxSpec
type SandboxResult = sandbox.SandboxResult
type ModelBinding = contracts.ModelBinding
type GitPolicy = contracts.GitPolicy
type AgentResult = agents.AgentResult

var ReadJSON = contracts.ReadJSON
