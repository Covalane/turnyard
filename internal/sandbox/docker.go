package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
)

type DockerDialect struct{ Executable string }

func (d DockerDialect) IsolationOptions(ctx context.Context, isolation contracts.SandboxIsolationProfile) ([]string, error) {
	if isolation == "" {
		return nil, nil
	}
	if isolation != contracts.SandboxIsolationGVisor {
		return nil, fault.New(fault.CodeCapabilityMissing, "%s does not support isolation profile %s", d.Name(), isolation)
	}
	if err := d.requireRuntime(ctx, "runsc"); err != nil {
		return nil, err
	}
	return []string{"--runtime", "runsc"}, nil
}

// requireRuntime checks the daemon's registered runtime instead of assuming
// the client-side runsc binary is sufficient.
func (d DockerDialect) requireRuntime(ctx context.Context, name string) error {
	call, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(call, d.Binary(), "info", "--format", "{{json .Runtimes}}").Output()
	if err != nil {
		return fault.Wrap(fault.CodeSandboxUnavailable, "inspect OCI runtimes", err, "cannot inspect %s runtimes", d.Name())
	}
	var runtimes map[string]json.RawMessage
	if err := json.Unmarshal(out, &runtimes); err != nil {
		return fault.Wrap(fault.CodeSandboxUnavailable, "decode OCI runtimes", err, "invalid %s runtime listing", d.Name())
	}
	if _, ok := runtimes[name]; !ok {
		return fault.New(fault.CodeCapabilityMissing, "%s runtime %s is not registered", d.Name(), name)
	}
	return nil
}

func (d DockerDialect) Name() string { return BackendDocker }
func (d DockerDialect) Binary() string {
	if d.Executable != "" {
		return d.Executable
	}
	return "docker"
}
func (d DockerDialect) ListArgs() []string              { return containerListArgs() }
func (d DockerDialect) DeleteArgs(name string) []string { return containerDeleteArgs(name) }
func (d DockerDialect) UserOptions(uid, gid int) []string {
	return []string{"--user", fmt.Sprintf("%d:%d", uid, gid)}
}
func (d DockerDialect) RunOptions(network contracts.SandboxNetworkPolicy) ([]string, error) {
	return containerRunOptions(network)
}
func (d DockerDialect) ImageIdentity(ctx context.Context, image string) (string, error) {
	return containerImageIdentity(ctx, d.Binary(), image)
}
