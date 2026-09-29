// Package registry composes the built-in sandbox dialects with the shared OCI
// lifecycle. Callers depend on sandbox.SandboxBackend, not a concrete CLI.
package registry

import (
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/sandbox"
	"github.com/Covalane/turnyard/internal/sandbox/applecontainer"
	"github.com/Covalane/turnyard/internal/sandbox/docker"
	"github.com/Covalane/turnyard/internal/sandbox/podman"
)

var factories = map[string]func() sandbox.SandboxBackend{
	sandbox.BackendAppleContainer: func() sandbox.SandboxBackend { return sandbox.NewOCIBackend(applecontainer.Dialect{}) },
	sandbox.BackendDocker:         func() sandbox.SandboxBackend { return sandbox.NewOCIBackend(docker.Dialect{}) },
	sandbox.BackendPodman:         func() sandbox.SandboxBackend { return sandbox.NewOCIBackend(podman.Dialect{}) },
}

// Backends returns the built-in sandbox backends in display order.
func Backends() []string {
	return []string{sandbox.BackendAppleContainer, sandbox.BackendDocker, sandbox.BackendPodman}
}

func Backend(name string) (sandbox.SandboxBackend, error) {
	if factory := factories[name]; factory != nil {
		return factory(), nil
	}
	return nil, fault.New(fault.CodeSandboxUnavailable, "unknown sandbox backend %s", name)
}
