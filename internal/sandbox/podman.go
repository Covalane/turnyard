package sandbox

import (
	"context"
	"fmt"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
)

type PodmanDialect struct{}

func (PodmanDialect) Name() string                    { return BackendPodman }
func (PodmanDialect) Binary() string                  { return "podman" }
func (PodmanDialect) ListArgs() []string              { return containerListArgs() }
func (PodmanDialect) DeleteArgs(name string) []string { return containerDeleteArgs(name) }
func (PodmanDialect) UserOptions(uid, gid int) []string {
	// Rootful Podman already has a direct host identity mapping. keep-id
	// creates an unnecessary user namespace and can fail in nested runtimes.
	if uid == 0 {
		return []string{"--user", fmt.Sprintf("%d:%d", uid, gid)}
	}
	return []string{"--userns=keep-id", "--user", fmt.Sprintf("%d:%d", uid, gid)}
}
func (PodmanDialect) RunOptions(network contracts.SandboxNetworkPolicy) ([]string, error) {
	return containerRunOptions(network)
}
func (d PodmanDialect) ImageIdentity(ctx context.Context, image string) (string, error) {
	return containerImageIdentity(ctx, d.Binary(), image)
}
func (d PodmanDialect) IsolationOptions(_ context.Context, isolation contracts.SandboxIsolationProfile) ([]string, error) {
	if isolation == "" {
		return nil, nil
	}
	return nil, fault.New(fault.CodeCapabilityMissing, "%s does not support isolation profile %s", d.Name(), isolation)
}
