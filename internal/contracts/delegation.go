package contracts

// DelegationToolID is reserved for Turnyard's managed child-agent tool.
const DelegationToolID = "turnyard_delegate"

// DelegationAction is the action accepted by the managed delegation tool.
// These values are shared by its MCP schema and the supervisor dispatcher.
type DelegationAction string

const (
	DelegationActionSubmit   DelegationAction = "submit"
	DelegationActionStatus   DelegationAction = "status"
	DelegationActionContinue DelegationAction = "continue"
)
