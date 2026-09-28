package engine

import (
	"github.com/Covalane/turnyard/internal/artifacts"
	"github.com/Covalane/turnyard/internal/gitstate"
	"github.com/Covalane/turnyard/internal/store"
)

// SessionCreationResult identifies a new or replayed session creation.
type SessionCreationResult struct {
	SessionID string `json:"sessionId"`
	Status    string `json:"status"`
	Workspace string `json:"workspace"`
	Replayed  bool   `json:"replayed"`
}

// TaskAdditionResult identifies a new or replayed task append.
type TaskAdditionResult struct {
	TaskID   string `json:"taskId"`
	Status   string `json:"status"`
	Replayed bool   `json:"replayed"`
}

// TaskRunResult ties an invocation's checks and outputs to its candidate.
type TaskRunResult struct {
	TaskID          string             `json:"taskId"`
	Status          string             `json:"status"`
	CandidateID     string             `json:"candidateId"`
	CandidateDigest string             `json:"candidateDigest"`
	CheckpointID    string             `json:"checkpointId"`
	Checks          []CheckResult      `json:"checks"`
	Deliverables    []artifacts.Result `json:"deliverables"`
	NeedsInput      string             `json:"needsInput"`
	NativeSessionID string             `json:"nativeSessionId"`
}

// CandidateVerificationResult reports a recheck of an existing candidate.
type CandidateVerificationResult struct {
	TaskID       string             `json:"taskId"`
	CandidateID  string             `json:"candidateId"`
	Status       string             `json:"status"`
	Checks       []CheckResult      `json:"checks"`
	Deliverables []artifacts.Result `json:"deliverables"`
}

// SessionCancellationResult records why a finite session was ended.
type SessionCancellationResult struct {
	SchemaVersion string  `json:"schemaVersion"`
	SessionID     string  `json:"sessionId"`
	Status        string  `json:"status"`
	CancelledAt   float64 `json:"cancelledAt"`
	Reason        string  `json:"reason"`
}

// ReconcileResult records the checkpoint and workspace seen after reconciliation.
type ReconcileResult struct {
	TaskID       string                         `json:"taskId"`
	CheckpointID string                         `json:"checkpointId"`
	Workspace    map[string]gitstate.RepoStatus `json:"workspace"`
}

// RestoreResult identifies the restored checkpoint and workspace path.
type RestoreResult struct {
	CheckpointID string `json:"checkpointId"`
	Workspace    string `json:"workspace"`
	Restored     bool   `json:"restored"`
}

// CandidateResult ties checks and declared outputs to one Git version vector.
type CandidateResult struct {
	ID           string                          `json:"id"`
	TaskID       string                          `json:"taskId"`
	Status       string                          `json:"status"`
	Digest       string                          `json:"digest"`
	Vector       map[string]gitstate.RepoVersion `json:"vector"`
	Checks       []CheckResult                   `json:"checks"`
	Deliverables []artifacts.Result              `json:"deliverables"`
	CreatedAt    float64                         `json:"createdAt"`
}

// TaskResult is the stable read model for one task and its execution history.
type TaskResult struct {
	SchemaVersion string                `json:"schemaVersion"`
	Task          store.TaskRow         `json:"task"`
	Invocations   []store.InvocationRow `json:"invocations"`
	Candidate     *CandidateResult      `json:"candidate,omitempty"`
	Turns         []store.TurnRow       `json:"turns"`
	Active        *bool                 `json:"active,omitempty"`
	Delegations   []DelegationSummary   `json:"delegations,omitempty"`
}

// DelegationHandoffUnavailable is a transient status response, not a
// persisted task lifecycle state.
const DelegationHandoffUnavailable = "handoff_unavailable"

// DelegationSummary links a parent task to one independently persisted child.
type DelegationSummary struct {
	SessionID       string `json:"sessionId"`
	TaskID          string `json:"taskId"`
	ParentTaskID    string `json:"parentTaskId"`
	AgentID         string `json:"agentId"`
	Status          string `json:"status"`
	CandidateID     string `json:"candidateId,omitempty"`
	CandidateDigest string `json:"candidateDigest,omitempty"`
}

// Delivery summarizes a task's current candidate in a session handoff.
type Delivery struct {
	TaskID          string              `json:"taskId"`
	Status          string              `json:"status"`
	CandidateID     string              `json:"candidateId,omitempty"`
	CandidateDigest string              `json:"candidateDigest,omitempty"`
	CandidateStatus string              `json:"candidateStatus,omitempty"`
	Deliverables    *[]artifacts.Result `json:"deliverables,omitempty"`
}

// SessionResult is the stable read model for a finite requirement session.
type SessionResult struct {
	SchemaVersion string              `json:"schemaVersion"`
	Session       store.SessionRow    `json:"session"`
	Tasks         []store.TaskRow     `json:"tasks"`
	Deliveries    []Delivery          `json:"deliveries"`
	Delegations   []DelegationSummary `json:"delegations,omitempty"`
}

// SessionCompletionResult records the final, candidate-bound handoff.
type SessionCompletionResult struct {
	SchemaVersion string     `json:"schemaVersion"`
	SessionID     string     `json:"sessionId"`
	Status        string     `json:"status"`
	CompletedAt   float64    `json:"completedAt"`
	Deliveries    []Delivery `json:"deliveries"`
}

// FeaturePublicationStatus describes one remote branch publication attempt.
type FeaturePublicationStatus string

const (
	FeaturePublicationPublished        FeaturePublicationStatus = "published"
	FeaturePublicationAlreadyPublished FeaturePublicationStatus = "already_published"
	FeaturePublicationFailed           FeaturePublicationStatus = "failed"
)

// PublishedFeature records the remote ref observed after a branch push.
type PublishedFeature struct {
	TaskID     string                   `json:"taskId"`
	Repository string                   `json:"repository"`
	Branch     string                   `json:"branch"`
	Head       string                   `json:"head"`
	Status     FeaturePublicationStatus `json:"status"`
	ErrorCode  string                   `json:"errorCode,omitempty"`
}

// SessionPublicationStatus summarizes the outcome across all feature branches.
type SessionPublicationStatus string

const (
	SessionPublicationPublished SessionPublicationStatus = "published"
	SessionPublicationPartial   SessionPublicationStatus = "partial"
)

// SessionPublicationResult preserves successes when a multi-repository push
// fails partway through. Repeating publish reconciles each remote branch.
type SessionPublicationResult struct {
	SchemaVersion string                   `json:"schemaVersion"`
	SessionID     string                   `json:"sessionId"`
	Status        SessionPublicationStatus `json:"status"`
	Features      []PublishedFeature       `json:"features"`
}

// EventsResult lists events after a stored sequence number.
type EventsResult struct {
	Events []store.EventRow `json:"events"`
}
