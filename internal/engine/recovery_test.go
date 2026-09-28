package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Covalane/turnyard/internal/fault"
)

func TestStopTaskContainersIdentifiesFailurePhase(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	added, err := f.service.AddTask(ctx, f.sid, f.task(t, "cleanup-phase"))
	if err != nil {
		t.Fatal(err)
	}
	cause := errors.New("backend registry failed")
	f.service.BackendFactory = func(string) (SandboxBackend, error) { return nil, cause }
	err = f.service.StopTaskContainers(ctx, added.TaskID)
	if !errors.Is(err, cause) || fault.CodeOf(err) != fault.CodeInternalError {
		t.Fatalf("cleanup cause or code lost: %v", err)
	}
	if !strings.Contains(strings.Join(fault.Operations(err), "/"), "select check cleanup backend") || fault.Origin(err) == "" {
		t.Fatalf("cleanup phase or origin missing: %v", err)
	}
}
