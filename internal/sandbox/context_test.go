package sandbox

import (
	"context"
	"errors"
	"testing"
)

func TestCanceledContextStopsBeforeSandboxLaunch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	backend := NewOCIBackend(DockerDialect{Executable: "docker"})
	_, err := backend.Run(ctx, SandboxRun{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}
