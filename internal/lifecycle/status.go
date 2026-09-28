// Package lifecycle names the persisted states and events shared by the
// executor, supervisor, and store. Wire values are stable across releases.
package lifecycle

const (
	Ready          = "ready"
	Running        = "running"
	Paused         = "paused"
	Completed      = "completed"
	Cancelled      = "cancelled"
	Queued         = "queued"
	Starting       = "starting"
	Verified       = "verified"
	Failed         = "failed"
	Unknown        = "unknown"
	NeedsInput     = "needs_input"
	CandidateReady = "candidate_ready"
)

// TerminalSession reports whether a session must reject further mutations.
func TerminalSession(status string) bool {
	return status == Completed || status == Cancelled
}

// WaitStops reports whether a task wait should return to its caller.
// NeedsInput pauses execution for a human decision, but the task can resume.
func WaitStops(status string) bool {
	switch status {
	case Verified, NeedsInput, Failed, Unknown, Cancelled:
		return true
	default:
		return false
	}
}
