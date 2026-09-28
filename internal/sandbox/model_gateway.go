package sandbox

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/observe"
)

//go:embed model_gateway.js
var modelGatewayScript []byte

const ModelCredentialPlaceholder = "turnyard-model-placeholder"

type modelGatewayLease struct {
	binary, name, network, script, address string
}

func (b OCIBackend) startModelGateway(ctx context.Context, input SandboxRun) (*modelGatewayLease, error) {
	if b.Name() != BackendDocker || input.ModelGateway == nil {
		return nil, fault.New(fault.CodeCapabilityMissing, "model-only gateway requires Docker")
	}
	if !strings.HasPrefix(input.Name, "ty-") || len(input.Name) > 80 {
		return nil, fault.New(fault.CodeInvalidSpec, "invalid model gateway invocation name")
	}
	lease := &modelGatewayLease{binary: b.dialect.Binary(), name: "ty-model-" + input.Name,
		network: "ty-net-" + input.Name}
	if err := runGatewayCLI(ctx, lease.binary, "network", "create", "--internal", lease.network); err != nil {
		return nil, err
	}
	ready := false
	defer func() {
		if !ready {
			if err := lease.close(context.WithoutCancel(ctx)); err != nil {
				observe.LogFailure(ctx, "failed model gateway setup cleanup", err, "container", lease.name, "network", lease.network)
			}
		}
	}()
	dir, err := os.MkdirTemp("", "turnyard-model-gateway-")
	if err != nil {
		return nil, err
	}
	lease.script = filepath.Join(dir, "gateway.js")
	if err := os.WriteFile(lease.script, modelGatewayScript, 0o644); err != nil {
		return nil, err
	}
	args := []string{"run", "-d", "--rm", "--name", lease.name, "--network", lease.network,
		"--network-alias", "turnyard-model", "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
		"--read-only", "--tmpfs", "/tmp", "--mount", fmt.Sprintf("type=bind,source=%s,target=/turnyard-gateway.js,readonly", lease.script),
		"--env", "TURNYARD_GATEWAY_ENDPOINT", "--env", "TURNYARD_GATEWAY_CREDENTIAL", "--env", "TURNYARD_GATEWAY_PLACEHOLDER",
		"--entrypoint", "node", input.Sandbox.Image, "/turnyard-gateway.js"}
	cmd := exec.CommandContext(ctx, lease.binary, args...)
	cmd.Env = append(os.Environ(), "TURNYARD_GATEWAY_ENDPOINT="+input.ModelGateway.Endpoint,
		"TURNYARD_GATEWAY_CREDENTIAL="+input.ModelGateway.Credential,
		"TURNYARD_GATEWAY_PLACEHOLDER="+ModelCredentialPlaceholder)
	if output, err := cmd.CombinedOutput(); err != nil {
		return nil, fault.Wrap(fault.CodeSandboxUnavailable, "model gateway", err, "start model gateway: %s", strings.TrimSpace(string(output)))
	}
	for attempt := 0; attempt < 30; attempt++ {
		check := exec.CommandContext(ctx, lease.binary, "exec", lease.name, "node", "-e",
			"fetch('http://turnyard-model:8080/_turnyard_health').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))")
		if check.Run() == nil {
			if input.Sandbox.Isolation == contracts.SandboxIsolationGVisor {
				lease.address, err = gatewayNetworkAddress(ctx, lease.binary, lease.name, lease.network)
				if err != nil {
					return nil, err
				}
			}
			if err := runGatewayCLI(ctx, lease.binary, "network", "connect", "bridge", lease.name); err != nil {
				return nil, err
			}
			ready = true
			return lease, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return nil, fault.New(fault.CodeSandboxUnavailable, "model gateway did not become ready")
}

func gatewayNetworkAddress(ctx context.Context, binary, container, network string) (string, error) {
	call, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(call, binary, "inspect", "--format", "{{json .NetworkSettings.Networks}}", container).Output()
	if err != nil {
		return "", fault.Wrap(fault.CodeSandboxUnavailable, "inspect model gateway", err, "model gateway address is unavailable")
	}
	var networks map[string]struct {
		IPAddress string `json:"IPAddress"`
	}
	if err := json.Unmarshal(out, &networks); err != nil {
		return "", fault.Wrap(fault.CodeSandboxUnavailable, "decode model gateway networks", err, "model gateway network metadata is invalid")
	}
	address := net.ParseIP(networks[network].IPAddress)
	if address == nil || address.To4() == nil {
		return "", fault.New(fault.CodeSandboxUnavailable, "model gateway has no internal IPv4 address")
	}
	return address.String(), nil
}

func runGatewayCLI(ctx context.Context, binary string, args ...string) error {
	call, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	output, err := exec.CommandContext(call, binary, args...).CombinedOutput()
	if err != nil {
		return fault.Wrap(fault.CodeSandboxUnavailable, "model gateway", err, "gateway operation failed: %s", strings.TrimSpace(string(output)))
	}
	return nil
}

func (l *modelGatewayLease) close(ctx context.Context) error {
	call, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	containerErr := exec.CommandContext(call, l.binary, "rm", "-f", l.name).Run()
	if l.script != "" {
		if err := os.RemoveAll(filepath.Dir(l.script)); err != nil {
			observe.LogFailure(ctx, "remove model gateway script failed", fault.Wrap(fault.CodeInternalError, "remove gateway script", err, "gateway script directory cleanup failed"))
		}
	}
	networkErr := exec.CommandContext(call, l.binary, "network", "rm", l.network).Run()
	if containerErr != nil {
		output, inspectErr := exec.CommandContext(call, l.binary, "inspect", l.name).CombinedOutput()
		if inspectErr == nil {
			return fault.Wrap(fault.CodeSandboxCleanupUnknown, "remove gateway container", containerErr, "model gateway container %s remained", l.name)
		}
		if !gatewayResourceMissing(output, l.name) {
			return fault.Wrap(fault.CodeSandboxCleanupUnknown, "confirm gateway container removal", errors.Join(containerErr, inspectErr), "model gateway container %s cleanup unconfirmed", l.name)
		}
	}
	if networkErr != nil {
		output, inspectErr := exec.CommandContext(call, l.binary, "network", "inspect", l.network).CombinedOutput()
		if inspectErr == nil || !gatewayResourceMissing(output, l.network) {
			return fault.Wrap(fault.CodeSandboxCleanupUnknown, "remove gateway network", errors.Join(networkErr, inspectErr), "model gateway network %s cleanup unconfirmed", l.network)
		}
	}
	return nil
}

// Docker uses distinct not-found messages for containers and networks. Any
// other inspection failure is uncertain, including daemon outages.
func gatewayResourceMissing(output []byte, name string) bool {
	message := strings.ToLower(strings.TrimSpace(string(output)))
	name = strings.ToLower(name)
	return strings.Contains(message, "no such object: "+name) ||
		strings.Contains(message, "no such container: "+name) ||
		strings.Contains(message, "no such network: "+name) ||
		strings.Contains(message, "network "+name+" not found")
}
