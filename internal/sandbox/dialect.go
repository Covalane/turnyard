package sandbox

import (
	"context"

	"github.com/Covalane/turnyard/internal/contracts"
)

// Dialect isolates OCI CLI differences from the sandbox lifecycle and mount
// policy. A new runtime can be supplied through NewOCIBackend without changing
// the task engine.
type Dialect interface {
	Name() string
	Binary() string
	ImageIdentity(context.Context, string) (string, error)
	ListArgs() []string
	DeleteArgs(string) []string
	RunOptions(network contracts.SandboxNetworkPolicy) ([]string, error)
	UserOptions(uid, gid int) []string
	IsolationOptions(context.Context, contracts.SandboxIsolationProfile) ([]string, error)
}
