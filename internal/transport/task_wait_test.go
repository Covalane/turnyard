package transport

import (
	"testing"

	"github.com/Covalane/turnyard/internal/lifecycle"
)

func TestTaskWaitRejectsMalformedSupervisorResult(t *testing.T) {
	for _, result := range []map[string]any{
		{},
		{"task": map[string]any{"status": lifecycle.Verified}},
		{"active": false, "task": map[string]any{}},
		{"active": "false", "task": map[string]any{"status": lifecycle.Verified}},
	} {
		if _, err := taskWaitDone(result); err == nil {
			t.Fatalf("malformed task result was accepted: %v", result)
		}
	}
	if done, err := taskWaitDone(map[string]any{"active": false, "task": map[string]any{"status": lifecycle.Verified}}); err != nil || !done {
		t.Fatalf("verified inactive task did not finish wait: %v %v", done, err)
	}
	if done, err := taskWaitDone(map[string]any{"active": true, "task": map[string]any{"status": lifecycle.Verified}}); err != nil || done {
		t.Fatalf("active task finished wait early: %v %v", done, err)
	}
}
