package observe

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/Covalane/turnyard/internal/fault"
)

func TestCorrelationFieldsFollowContext(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(correlationHandler{slog.NewJSONHandler(&output, nil)})
	ctx := WithIDs(context.Background(), IDs{RequestID: "req_test", TaskID: "work_test"})
	ctx = WithIDs(ctx, IDs{InvocationID: "inv_test"})
	logger.InfoContext(ctx, "completed")
	line := output.String()
	for _, want := range []string{`"request_id":"req_test"`, `"task_id":"work_test"`, `"invocation_id":"inv_test"`} {
		if !strings.Contains(line, want) {
			t.Fatalf("log missing %s: %s", want, line)
		}
	}
}

func TestFailureLogKeepsSafeProcessExitStatus(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("test uses a POSIX shell")
	}
	var output bytes.Buffer
	previous := Log
	Log = slog.New(correlationHandler{slog.NewJSONHandler(&output, nil)})
	t.Cleanup(func() { Log = previous })
	commandErr := exec.Command("sh", "-c", "exit 27").Run()
	err := fault.Wrap(fault.CodeSandboxCleanupUnknown, "remove container", commandErr, "cleanup failed")
	LogFailure(context.Background(), "cleanup unconfirmed", err)
	if line := output.String(); !strings.Contains(line, `"exit_code":27`) {
		t.Fatalf("log missing safe exit status: %s", line)
	}
}

func TestFailureLogKeepsTraceWithoutCauseText(t *testing.T) {
	var output bytes.Buffer
	previous := Log
	Log = slog.New(correlationHandler{slog.NewJSONHandler(&output, nil)})
	t.Cleanup(func() { Log = previous })
	ctx := WithIDs(context.Background(), IDs{RequestID: "req_trace"})
	err := fault.At(fault.Wrap(fault.CodeSandboxCleanupUnknown, "remove container", errors.New("private signed URL"), "cleanup failed"), "stop invocation")
	LogFailure(ctx, "cleanup unconfirmed", err, "invocation_id", "inv_test")
	line := output.String()
	for _, field := range []string{`"request_id":"req_trace"`, `"code":"SANDBOX_CLEANUP_UNKNOWN"`, `"sites":`, `"operations":`, `"cause_type":`} {
		if !strings.Contains(line, field) {
			t.Fatalf("failure log missing %s: %s", field, line)
		}
	}
	if strings.Contains(line, "private signed URL") {
		t.Fatalf("failure log leaked cause text: %s", line)
	}
}
