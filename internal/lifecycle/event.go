package lifecycle

// PhaseDelegationHandoff identifies the candidate failure stage recorded in
// event payloads when a child result cannot be handed to its parent.
const PhaseDelegationHandoff = "delegation_handoff"

const (
	EventSessionCreated         = "session.created"
	EventSessionCompleted       = "session.completed"
	EventSessionCancelled       = "session.cancelled"
	EventFeaturePublished       = "feature.published"
	EventFeaturePublishFailed   = "feature.publish_failed"
	EventTaskAdded              = "task.added"
	EventTaskState              = "task.state"
	EventTaskPreflightFailed    = "task.preflight_failed"
	EventTaskUnknownReconciled  = "task.unknown_reconciled"
	EventTaskRecoveredUnknown   = "task.recovered_unknown"
	EventInvocationStarting     = "invocation.starting"
	EventInvocationError        = "invocation.error"
	EventAgentCompleted         = "agent.completed"
	EventCandidateRecorded      = "candidate.recorded"
	EventCandidateVerified      = "candidate.verified"
	EventCandidateFailed        = "candidate.failed"
	EventCandidateNeedsInput    = "candidate.needs_input"
	EventCandidateReverifyStart = "candidate.reverify.start"
	EventCandidateReverifyEnd   = "candidate.reverify.finish"
	EventCandidateReverifyError = "candidate.reverify.error"
	EventCheckpointSaved        = "checkpoint.saved"
	EventCheckpointRestored     = "checkpoint.restored"
	EventCheckCompleted         = "check.completed"
	EventArtifactDeliveryIntent = "artifact.delivery.intent"
	EventArtifactDeliveryResult = "artifact.delivery.result"
)
