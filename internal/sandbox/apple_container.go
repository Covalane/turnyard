package sandbox

import (
	"context"
	"encoding/json"
	"os/exec"
	"time"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
)

type AppleDialect struct{}

func (AppleDialect) IsolationOptions(_ context.Context, isolation contracts.SandboxIsolationProfile) ([]string, error) {
	if isolation == "" {
		return nil, nil
	}
	return nil, fault.New(fault.CodeCapabilityMissing, "Apple container does not support isolation profile %s", isolation)
}

func (AppleDialect) Name() string                    { return BackendAppleContainer }
func (AppleDialect) Binary() string                  { return "container" }
func (AppleDialect) UserOptions(_, _ int) []string   { return nil }
func (AppleDialect) ListArgs() []string              { return []string{"list", "--all"} }
func (AppleDialect) DeleteArgs(name string) []string { return []string{"delete", "--force", name} }
func (AppleDialect) RunOptions(network contracts.SandboxNetworkPolicy) ([]string, error) {
	switch network {
	case "", contracts.SandboxNetworkDefault:
		return nil, nil
	case contracts.SandboxNetworkNone, contracts.SandboxNetworkModelOnly:
		return nil, fault.New(fault.CodeCapabilityMissing, "Apple container network isolation is not supported")
	default:
		return nil, fault.New(fault.CodeInvalidSpec, "unknown sandbox network policy %s", network)
	}
}
func (AppleDialect) ImageIdentity(ctx context.Context, image string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "container", "image", "inspect", image).Output()
	if err != nil {
		return "", imageInspectError(ctx, err, image)
	}
	var value []struct {
		Configuration struct {
			Descriptor struct {
				Digest string `json:"digest"`
			} `json:"descriptor"`
		} `json:"configuration"`
	}
	if err := json.Unmarshal(out, &value); err != nil || len(value) == 0 || value[0].Configuration.Descriptor.Digest == "" {
		return "", fault.New(fault.CodeImageInspectFailed, "unrecognized Apple container image metadata")
	}
	return value[0].Configuration.Descriptor.Digest, nil
}
