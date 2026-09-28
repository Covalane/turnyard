package sandbox

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
)

// Docker and Podman share these OCI-compatible CLI operations. Their runtime
// capabilities remain separate in their respective dialect implementations.
func containerListArgs() []string              { return []string{"ps", "-a", "--format", "{{.Names}}"} }
func containerDeleteArgs(name string) []string { return []string{"rm", "-f", name} }

func containerRunOptions(network contracts.SandboxNetworkPolicy) ([]string, error) {
	args := []string{"--cap-drop", "ALL", "--security-opt", "no-new-privileges"}
	switch network {
	case "", contracts.SandboxNetworkDefault:
	case contracts.SandboxNetworkNone, contracts.SandboxNetworkModelOnly:
		args = append(args, "--network", "none")
	default:
		return nil, fault.New(fault.CodeInvalidSpec, "unknown sandbox network policy %s", network)
	}
	return args, nil
}

func containerImageIdentity(ctx context.Context, binary, image string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, binary, "image", "inspect", "--format", "{{.Id}}", image).Output()
	if err != nil {
		return "", imageInspectError(ctx, err, image)
	}
	digest := strings.TrimSpace(string(out))
	if digest == "" {
		return "", fault.New(fault.CodeImageInspectFailed, "image %s has no inspectable identity", image)
	}
	return digest, nil
}

// A failed inspect is not proof that an image is missing: the daemon or CLI
// may be unavailable. Only explicit CLI messages receive IMAGE_MISSING.
func imageInspectError(ctx context.Context, err error, image string) error {
	if ctx.Err() != nil {
		return fault.Wrap(fault.CodeInterrupted, "inspect image", err, "image inspection interrupted")
	}
	code := fault.CodeImageInspectFailed
	if exit, ok := err.(*exec.ExitError); ok {
		message := strings.ToLower(string(exit.Stderr))
		if strings.Contains(message, "no such image") || strings.Contains(message, "image not known") || strings.Contains(message, "image not found") {
			code = fault.CodeImageMissing
		}
	}
	return fault.Wrap(code, "inspect image", err, "inspect image %s", image)
}
