package sandbox

import (
	"context"
	"errors"
	"testing"

	"github.com/Covalane/turnyard/internal/sandbox/docker"
)

func TestCanceledContextStopsBeforeSandboxLaunch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	backend := NewOCIBackend(docker.Dialect{Executable: "docker"})
	_, err := backend.Run(ctx, SandboxRun{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}
