// Package contracts loads and validates the JSON inputs used to create a
// session and append work without depending on execution implementations.
package contracts

// RepositorySourceType selects how a Git repository is obtained. These values
// are part of the JSON contract and must stay in sync with session.json.
type RepositorySourceType string

const (
	RepositorySourceLocalGit  RepositorySourceType = "local-git"
	RepositorySourceRemoteGit RepositorySourceType = "remote-git"
)

// RepositorySpec pins a source repository to one commit for a session.
type RepositorySpec struct {
	ID      string               `json:"id"`
	Type    RepositorySourceType `json:"type"`
	Path    string               `json:"path,omitempty"`
	URL     string               `json:"url,omitempty"`
	PushURL string               `json:"pushUrl,omitempty"`
	Commit  string               `json:"commit"`
}

// SessionSpec selects the repositories, environment, and primary agent.
type SessionSpec struct {
	SchemaVersion  string           `json:"schemaVersion"`
	IdempotencyKey string           `json:"idempotencyKey"`
	Repositories   []RepositorySpec `json:"repositories"`
	Environment    string           `json:"environment"`
	PrimaryAgent   string           `json:"primaryAgent"`
}

// SandboxSpec selects a backend, image, and resource boundaries.
type SandboxSpec struct {
	Backend     string                  `json:"backend"`
	Image       string                  `json:"image"`
	ImageDigest string                  `json:"imageDigest,omitempty"`
	CPUs        int                     `json:"cpus,omitempty"`
	MemoryMB    int                     `json:"memoryMB,omitempty"`
	Network     SandboxNetworkPolicy    `json:"network,omitempty"`
	Isolation   SandboxIsolationProfile `json:"isolation,omitempty"`
}

type SandboxNetworkPolicy string
type SandboxIsolationProfile string

const (
	SandboxNetworkDefault   SandboxNetworkPolicy    = "default"
	SandboxNetworkNone      SandboxNetworkPolicy    = "none"
	SandboxNetworkModelOnly SandboxNetworkPolicy    = "model-only"
	SandboxIsolationGVisor  SandboxIsolationProfile = "gvisor"
)

// AgentSpec binds a runtime to a model and optional injected capabilities.
type AgentSpec struct {
	ID           string   `json:"id"`
	Runtime      string   `json:"runtime"`
	ModelBinding string   `json:"modelBinding"`
	Skills       []string `json:"skills,omitempty"`
	Tools        []string `json:"tools,omitempty"`
	// Delegates lists agents this agent may launch as managed child sessions.
	// An absent list does not expose the delegation tool.
	Delegates []string `json:"delegates,omitempty"`
}

// ModelBinding names the provider model and its credential source.
type ModelBinding struct {
	ID            string `json:"id"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	CredentialEnv string `json:"credentialEnv"`
	// Endpoints is for a trusted, explicitly configured provider that is not
	// in the built-in catalog. Only the transports a runtime uses are required.
	Endpoints ModelEndpoints `json:"endpoints,omitzero"`
}

type ModelEndpoints struct {
	OpenAIChat string `json:"openaiChat,omitempty"`
	Anthropic  string `json:"anthropic,omitempty"`
	Responses  string `json:"responses,omitempty"`
}

// GitPolicy limits the repository writes Turnyard may perform.
type GitPolicy struct {
	LocalCommits bool                 `json:"localCommits"`
	RemoteWrites GitRemoteWritePolicy `json:"remoteWrites"`
}

// GitRemoteWritePolicy controls whether Turnyard may publish feature branches.
type GitRemoteWritePolicy string

const (
	GitRemoteWritesNone GitRemoteWritePolicy = "none"
	GitRemoteWritesPush GitRemoteWritePolicy = "push"
)

// CheckSpec defines a trusted command run against the candidate workspace.
type CheckSpec struct {
	ID             string   `json:"id"`
	Argv           []string `json:"argv"`
	Repositories   []string `json:"repositories"`
	TimeoutSeconds int      `json:"timeoutSeconds,omitempty"`
}

// BundleSpec identifies a skill bundle and its optional source digest.
type BundleSpec struct {
	ID           string `json:"id"`
	Path         string `json:"path"`
	SourceDigest string `json:"sourceDigest,omitempty"`
}

// ToolSpec identifies a tool available for agent injection.
type ToolSpec struct {
	ID             string   `json:"id"`
	Kind           ToolKind `json:"kind"`
	Description    string   `json:"description,omitempty"`
	Path           string   `json:"path,omitempty"`
	Argv           []string `json:"argv"`
	PassEnv        []string `json:"passEnv,omitempty"`
	TimeoutSeconds int      `json:"timeoutSeconds,omitempty"`
	SourceDigest   string   `json:"sourceDigest,omitempty"`
}

type ToolKind string

const (
	// ToolKindMCP identifies a program that already speaks MCP over stdio.
	ToolKindMCP ToolKind = "mcp"
	// ToolKindExecutable exposes an arbitrary sandbox command through Turnyard's MCP bridge.
	ToolKindExecutable ToolKind = "executable"
)

// ToolSearchSpec controls optional model-assisted ranking inside the gateway.
// Search never authorizes or executes a tool.
type ToolSearchSpec struct {
	Mode         ToolSearchMode `json:"mode"`
	ModelBinding string         `json:"modelBinding,omitempty"`
}

type ToolSearchMode string

const (
	ToolSearchLexical   ToolSearchMode = "lexical"
	ToolSearchLLMRerank ToolSearchMode = "llm-rerank"
)

// EnvironmentSpec defines the sandbox, agents, tools, and checks for a session.
type EnvironmentSpec struct {
	SchemaVersion      string                  `json:"schemaVersion"`
	Sandbox            SandboxSpec             `json:"sandbox"`
	Agents             []AgentSpec             `json:"agents"`
	ModelBindings      []ModelBinding          `json:"modelBindings"`
	Git                GitPolicy               `json:"git,omitzero"`
	Checks             []CheckSpec             `json:"checks,omitempty"`
	Skills             []BundleSpec            `json:"skills,omitempty"`
	Tools              []ToolSpec              `json:"tools,omitempty"`
	ToolSearch         *ToolSearchSpec         `json:"toolSearch,omitempty"`
	ArtifactConnectors []ArtifactConnectorSpec `json:"artifactConnectors,omitempty"`
	InputHosts         []string                `json:"inputHosts,omitempty"`
}

// ArtifactConnectorSpec is a trusted host-side byte transport. Work may only
// select one of these preconfigured commands and a URI under its prefix.
type ArtifactConnectorSpec struct {
	ID             string   `json:"id"`
	URIPrefix      string   `json:"uriPrefix"`
	GetArgv        []string `json:"getArgv"`
	PutArgv        []string `json:"putArgv,omitempty"`
	PassEnv        []string `json:"passEnv,omitempty"`
	TimeoutSeconds int      `json:"timeoutSeconds,omitempty"`
}

// ScopeRepo grants one task read or write access to a session repository.
type ScopeRepo struct {
	ID   string    `json:"id"`
	Mode ScopeMode `json:"mode"`
}

type ScopeMode string

const (
	// ScopeRead permits reading a repository without agent writes.
	ScopeRead ScopeMode = "read"
	// ScopeWrite permits agent edits that Turnyard can commit.
	ScopeWrite ScopeMode = "write"
)

// WorkSpec states one task's objective, scope, checks, and required outputs.
type WorkSpec struct {
	SchemaVersion  string `json:"schemaVersion"`
	IdempotencyKey string `json:"idempotencyKey"`
	// RequestDigest is supervisor-owned. It lets a retried task add return its
	// original result without fetching a potentially expired signed URL again.
	RequestDigest string `json:"requestDigest,omitempty"`
	Objective     string `json:"objective"`
	// CommitMessage is the human-readable first line of the Git commit. When
	// omitted, Turnyard derives a short title from Objective for older inputs.
	CommitMessage string `json:"commitMessage,omitempty"`
	Scope         struct {
		Repositories []ScopeRepo `json:"repositories"`
	} `json:"scope"`
	Acceptance   []string          `json:"acceptance"`
	Checks       []string          `json:"checks"`
	Inputs       []InputSpec       `json:"inputs,omitempty"`
	Deliverables []DeliverableSpec `json:"deliverables,omitempty"`
}

// InputSpec is staged to immutable session bytes before a task is accepted.
// The staged form replaces the original source in the stored work specification.
type InputSpec struct {
	ID             string      `json:"id"`
	Source         InputSource `json:"source"`
	ExpectedSHA256 string      `json:"expectedSha256,omitempty"`
	MediaType      string      `json:"mediaType,omitempty"`
}

type InputSource struct {
	Kind      InputSourceKind `json:"kind"`
	Path      string          `json:"path,omitempty"`
	URL       string          `json:"url,omitempty"`
	Connector string          `json:"connector,omitempty"`
	URI       string          `json:"uri,omitempty"`
}

// DestinationSpec selects how verified bytes leave a task. A connector URI
// contains {sha256}, making retries target the same content-addressed object.
type DestinationSpec struct {
	Kind      DestinationKind `json:"kind"`
	Connector string          `json:"connector,omitempty"`
	URI       string          `json:"uri,omitempty"`
}

// DeliverableSpec describes one required output. Repository files are read from
// a candidate commit; text and external references use an invocation claim.
type DeliverableSpec struct {
	ID             string           `json:"id"`
	Repository     string           `json:"repository,omitempty"`
	Path           string           `json:"path,omitempty"`
	Kind           DeliverableKind  `json:"kind"`
	ExpectedSHA256 string           `json:"expectedSha256,omitempty"`
	MediaType      string           `json:"mediaType,omitempty"`
	Base           string           `json:"base,omitempty"`
	Destination    *DestinationSpec `json:"destination,omitempty"`
}

type InputSourceKind string
type DestinationKind string
type DeliverableKind string

const (
	InputFile            InputSourceKind = "file"
	InputHTTPS           InputSourceKind = "https"
	InputConnector       InputSourceKind = "connector"
	InputStaged          InputSourceKind = "staged"
	DestinationLocal     DestinationKind = "local"
	DestinationConnector DestinationKind = "connector"
)

const (
	DeliverableFile        DeliverableKind = "file"
	DeliverableImage       DeliverableKind = "image"
	DeliverableText        DeliverableKind = "text"
	DeliverablePullRequest DeliverableKind = "pull_request"
)
