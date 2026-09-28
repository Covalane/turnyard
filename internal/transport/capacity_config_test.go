package transport

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSupervisorCapacityConfiguration(t *testing.T) {
	t.Setenv("TURNYARD_MAX_ACTIVE_TASKS", "3")
	t.Setenv("TURNYARD_MAX_ACTIVE_PREPARATIONS", "4")
	t.Setenv("TURNYARD_MAX_PENDING_PREPARATIONS", "5")
	t.Setenv("TURNYARD_MAX_ACTIVE_CPUS", "6")
	t.Setenv("TURNYARD_MAX_ACTIVE_MEMORY_MB", "8192")
	t.Setenv("TURNYARD_QUEUE_WAIT_SECONDS", "30")
	limits, err := capacityFromEnvironment()
	if err != nil || limits.MaxTasks != 3 || limits.MaxPreparations != 4 || limits.MaxPendingPreparations != 5 || limits.MaxCPUs != 6 || limits.MaxMemoryMB != 8192 || limits.QueueWait != 30*time.Second {
		t.Fatalf("unexpected capacity config: %+v %v", limits, err)
	}
	t.Setenv("TURNYARD_MAX_ACTIVE_TASKS", "0")
	if _, err := capacityFromEnvironment(); err == nil {
		t.Fatal("accepted zero task slots")
	}
}

func TestStateWarningStatus(t *testing.T) {
	t.Setenv("TURNYARD_STATE_WARN_MB", "1")
	threshold, err := stateWarningBytes()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "large"), make([]byte, 1<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Supervisor{stateWarnBytes: threshold}
	s.scanState(context.Background(), root)
	if !s.stateWarned.Load() || s.stateBytes.Load() < threshold {
		t.Fatalf("state warning was not raised: bytes=%d", s.stateBytes.Load())
	}
}
