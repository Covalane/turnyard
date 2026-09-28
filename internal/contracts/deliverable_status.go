package contracts

// DeliverableStatus is the candidate-bound verification outcome for a
// declared output. It is shared by Git-tree inspection and delivery results.
type DeliverableStatus string

const (
	DeliverableStatusPresent    DeliverableStatus = "present"
	DeliverableStatusMissing    DeliverableStatus = "missing"
	DeliverableStatusInvalid    DeliverableStatus = "invalid"
	DeliverableStatusMismatch   DeliverableStatus = "mismatch"
	DeliverableStatusUnverified DeliverableStatus = "unverified"
)
