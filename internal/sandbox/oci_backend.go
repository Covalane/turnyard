package sandbox

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
)

func (b OCIBackend) Name() string {
	return b.dialect.Name()
}

func (b OCIBackend) ValidateSpec(ctx context.Context, spec contracts.SandboxSpec) error {
	if spec.Network == contracts.SandboxNetworkModelOnly && b.Name() != BackendDocker {
		return fault.New(fault.CodeCapabilityMissing, "model-only networking requires Docker")
	}
	if _, err := b.dialect.RunOptions(spec.Network); err != nil {
		return err
	}
	_, err := b.dialect.IsolationOptions(ctx, spec.Isolation)
	return err
}
func (b OCIBackend) Probe(ctx context.Context) (map[string]any, error) {
	path, err := exec.LookPath(b.dialect.Binary())
	if err != nil {
		return map[string]any{"backend": b.Name(), "available": false},
			fault.Wrap(fault.CodeSandboxUnavailable, "locate sandbox binary", err, "%s binary is unavailable", b.Name())
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		return map[string]any{"backend": b.Name(), "available": false},
			fault.Wrap(fault.CodeSandboxUnavailable, "probe sandbox binary", err, "%s probe failed", b.Name())
	}
	return map[string]any{"backend": b.Name(), "available": true, "version": strings.TrimSpace(string(out))}, nil
}
func (b OCIBackend) ImageIdentity(ctx context.Context, image string) (string, error) {
	return b.dialect.ImageIdentity(ctx, image)
}
func (b OCIBackend) exists(ctx context.Context, name string) bool {
	present, err := b.containerPresent(ctx, name)
	return present || err != nil
}
func (b OCIBackend) containerPresent(ctx context.Context, name string) (bool, error) {
	names, err := b.containerNames(ctx)
	if err != nil {
		return false, err
	}
	for _, candidate := range names {
		if candidate == name {
			return true, nil
		}
	}
	return false, nil
}
func (b OCIBackend) containerNames(ctx context.Context) ([]string, error) {
	args := b.dialect.ListArgs()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, b.dialect.Binary(), args...).Output()
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, line := range strings.Split(string(out), "\n") {
		columns := strings.Fields(line)
		if len(columns) > 0 {
			names = append(names, columns[0])
		}
	}
	return names, nil
}
func (b OCIBackend) cleanup(ctx context.Context, name string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, b.dialect.Binary(), b.dialect.DeleteArgs(name)...).CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if len(message) > 512 {
			message = message[:512]
		}
		return fault.Wrap(fault.CodeSandboxCleanupUnknown, "remove container", err, "remove %s container %s: %s", b.Name(), name, message)
	}
	return nil
}

// confirmInterruptedCleanup accounts for OCI daemons that finish creating a
// container after the client process has been killed on timeout or cancel.
func (b OCIBackend) confirmInterruptedCleanup(ctx context.Context, name string) error {
	call, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	const quietPeriod = 2 * time.Second
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var absentSince time.Time
	var lastError error
	for {
		present, err := b.containerPresent(call, name)
		switch {
		case err != nil:
			absentSince = time.Time{}
			lastError = err
		case present:
			absentSince = time.Time{}
			if err := b.cleanup(call, name); err != nil {
				lastError = err
			}
		default:
			if absentSince.IsZero() {
				absentSince = time.Now()
			} else if time.Since(absentSince) >= quietPeriod {
				return nil
			}
		}
		select {
		case <-call.Done():
			return fault.Wrap(fault.CodeSandboxCleanupUnknown, "confirm interrupted cleanup", errors.Join(lastError, call.Err()), "cannot confirm container %s stayed absent", name)
		case <-ticker.C:
		}
	}
}

// StopInvocation also finds auxiliary inventory and MCP probe containers.
// Failed listing keeps task outcome unknown rather than guessing absence.
func (b OCIBackend) StopInvocation(ctx context.Context, invocationID string) error {
	ctx = context.WithoutCancel(ctx)
	names, err := b.containerNames(ctx)
	if err != nil {
		return fault.Wrap(fault.CodeSandboxCleanupUnknown, "", err, "list %s containers", b.Name())
	}
	belongs := func(name string) bool {
		return name == "ty-"+invocationID || name == "ty-model-ty-"+invocationID ||
			strings.HasPrefix(name, "ty-tools-"+invocationID) || strings.HasPrefix(name, "ty-scan-"+invocationID) || strings.HasPrefix(name, "ty-kimi-scan-"+invocationID) ||
			name == "ty-tool-gw-ty-"+invocationID || strings.HasPrefix(name, "ty-tool-gw-ty-tools-"+invocationID) ||
			strings.HasPrefix(name, "ty-tool-gw-ty-scan-"+invocationID) || strings.HasPrefix(name, "ty-tool-gw-ty-kimi-scan-"+invocationID)
	}
	failed := map[string]error{}
	for _, name := range names {
		if belongs(name) {
			if err := b.cleanup(ctx, name); err != nil {
				failed[name] = err
			}
		}
	}
	names, err = b.containerNames(ctx)
	if err != nil {
		return fault.Wrap(fault.CodeSandboxCleanupUnknown, "", err, "confirm %s cleanup", b.Name())
	}
	for _, name := range names {
		if belongs(name) {
			if err := failed[name]; err != nil {
				return fault.At(err, "confirm invocation container cleanup")
			}
			return fault.New(fault.CodeSandboxCleanupUnknown, "container %s is still present", name)
		}
	}
	if b.Name() == BackendDocker {
		lease := modelGatewayLease{binary: b.dialect.Binary(), name: "ty-model-ty-" + invocationID,
			network: "ty-net-ty-" + invocationID}
		if err := lease.close(ctx); err != nil {
			return err
		}
		if err := b.stopToolNetworks(ctx, invocationID); err != nil {
			return err
		}
	}
	return nil
}

func (b OCIBackend) stopToolNetworks(ctx context.Context, invocationID string) error {
	belongs := func(name string) bool {
		return name == "ty-tool-net-ty-"+invocationID || strings.HasPrefix(name, "ty-tool-net-ty-tools-"+invocationID) ||
			strings.HasPrefix(name, "ty-tool-net-ty-scan-"+invocationID) || strings.HasPrefix(name, "ty-tool-net-ty-kimi-scan-"+invocationID)
	}
	list := func() ([]string, error) {
		call, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		output, err := exec.CommandContext(call, b.dialect.Binary(), "network", "ls", "--format", "{{.Name}}").Output()
		if err != nil {
			return nil, fault.Wrap(fault.CodeSandboxCleanupUnknown, "list tool networks", err, "cannot list Docker networks")
		}
		return strings.Fields(string(output)), nil
	}
	networks, err := list()
	if err != nil {
		return err
	}
	for _, name := range networks {
		if belongs(name) {
			if err := runGatewayCLI(ctx, b.dialect.Binary(), "network", "rm", name); err != nil {
				return fault.Wrap(fault.CodeSandboxCleanupUnknown, "remove tool network", err, "cannot remove tool gateway network %s", name)
			}
		}
	}
	networks, err = list()
	if err != nil {
		return err
	}
	for _, name := range networks {
		if belongs(name) {
			return fault.New(fault.CodeSandboxCleanupUnknown, "tool gateway network %s is still present", name)
		}
	}
	return nil
}
func (b OCIBackend) StopTaskChecks(ctx context.Context, taskID string) error {
	ctx = context.WithoutCancel(ctx)
	prefix := "ty-check-" + taskID + "-"
	names, err := b.containerNames(ctx)
	if err != nil {
		return fault.Wrap(fault.CodeSandboxCleanupUnknown, "", err, "list %s checks", b.Name())
	}
	failed := map[string]error{}
	for _, name := range names {
		if strings.HasPrefix(name, prefix) {
			if err := b.cleanup(ctx, name); err != nil {
				failed[name] = err
			}
		}
	}
	names, err = b.containerNames(ctx)
	if err != nil {
		return fault.Wrap(fault.CodeSandboxCleanupUnknown, "", err, "confirm %s check cleanup", b.Name())
	}
	for _, name := range names {
		if strings.HasPrefix(name, prefix) {
			if err := failed[name]; err != nil {
				return fault.At(err, "confirm task check cleanup")
			}
			return fault.New(fault.CodeSandboxCleanupUnknown, "check container %s is still present", name)
		}
	}
	return nil
}
