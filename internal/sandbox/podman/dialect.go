package podman

import (
	"context"
	"fmt"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/sandbox/ocicli"
)

// Dialect supplies Podman-specific commands to the shared OCI backend.
type Dialect struct{}

func (Dialect) Name() string                    { return ocicli.BackendPodman }
func (Dialect) Binary() string                  { return "podman" }
func (Dialect) ListArgs() []string              { return ocicli.ListArgs() }
func (Dialect) DeleteArgs(name string) []string { return ocicli.DeleteArgs(name) }
func (Dialect) UserOptions(uid, gid int) []string {
	// Rootful Podman already has a direct host identity mapping. keep-id
	// creates an unnecessary user namespace and can fail in nested runtimes.
	if uid == 0 {
		return []string{"--user", fmt.Sprintf("%d:%d", uid, gid)}
	}
	return []string{"--userns=keep-id", "--user", fmt.Sprintf("%d:%d", uid, gid)}
}
func (Dialect) RunOptions(network contracts.SandboxNetworkPolicy) ([]string, error) {
	return ocicli.RunOptions(network)
}
func (d Dialect) ImageIdentity(ctx context.Context, image string) (string, error) {
	return ocicli.ImageIdentity(ctx, d.Binary(), image)
}
func (d Dialect) IsolationOptions(_ context.Context, isolation contracts.SandboxIsolationProfile) ([]string, error) {
	if isolation == "" {
		return nil, nil
	}
	return nil, fault.New(fault.CodeCapabilityMissing, "%s does not support isolation profile %s", d.Name(), isolation)
}
