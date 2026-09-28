package sandbox

import (
	"context"
	"regexp"
	"time"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
)

// SandboxBackend owns container lifecycle and host mounts. Agent drivers
// supply an entry point, state, credentials, and repository access scope.
type SandboxBackend interface {
	Name() string
	Probe(context.Context) (map[string]any, error)
	ImageIdentity(context.Context, string) (string, error)
	ValidateSpec(context.Context, contracts.SandboxSpec) error
	Run(context.Context, SandboxRun) (SandboxResult, error)
	StopInvocation(context.Context, string) error
	StopTaskChecks(context.Context, string) error
}
type SandboxRun struct {
	Name             string
	Sandbox          contracts.SandboxSpec
	Workspace        string
	Scope            []contracts.ScopeRepo
	State            string
	CatalogDir       string
	ArtifactDir      string
	ArtifactReadOnly bool
	InputDir         string
	ControlDir       string
	Command          []string
	EntryPoint       string
	Environment      map[string]string
	Credentials      map[string]string
	ModelGateway     *ModelGateway
	ToolGateway      *ToolGateway
	Timeout          time.Duration
	LogPath          string
}

// ModelGateway keeps the real provider credential in a separate container.
// The agent is mounted only on an internal network and receives a placeholder.
type ModelGateway struct {
	Endpoint   string
	Credential string
}

// ToolGateway runs selected tools in a separate Docker container. Only a
// protocol proxy, never the tool credentials, is exposed to the agent.
type ToolGateway struct {
	ConfigPath   string
	CatalogDir   string
	Credentials  map[string]string
	InvocationID string
}
type SandboxResult struct {
	ExitCode int    `json:"exitCode"`
	Output   string `json:"-"`
	Stderr   string `json:"-"`
	TimedOut bool   `json:"timedOut"`
	Backend  string `json:"backend"`
}
type OCIBackend struct{ dialect Dialect }

func NewOCIBackend(dialect Dialect) OCIBackend {
	return OCIBackend{dialect: dialect}
}

var credentialNamePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)

func validCredentialName(name string) bool {
	return credentialNamePattern.MatchString(name)
}

func placeholderCredentials(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for name := range source {
		result[name] = ModelCredentialPlaceholder
	}
	return result
}
func Backend(name string) (SandboxBackend, error) {
	switch name {
	case BackendAppleContainer:
		return NewOCIBackend(AppleDialect{}), nil
	case BackendDocker:
		return NewOCIBackend(DockerDialect{}), nil
	case BackendPodman:
		return NewOCIBackend(PodmanDialect{}), nil
	default:
		return nil, fault.New(fault.CodeSandboxUnavailable, "unknown sandbox backend %s", name)
	}
}
