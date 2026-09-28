package fault

import (
	"errors"
	"strings"
	"testing"

	crerrors "github.com/cockroachdb/errors"
)

func TestWrappedErrorKeepsCauseAndCode(t *testing.T) {
	cause := errors.New("network failure")
	err := Wrap(CodeImageMissing, "inspect", cause, "image %s", "agent:latest")
	if CodeOf(err) != CodeImageMissing {
		t.Fatalf("code=%s", CodeOf(err))
	}
	if !errors.Is(err, cause) {
		t.Fatal("original cause is not inspectable")
	}
	var typed *Error
	if !errors.As(err, &typed) || typed.Trace == nil {
		t.Fatal("origin trace is missing")
	}
	if got := Origin(err); !strings.HasPrefix(got, "fault_test.go:") {
		t.Fatalf("origin should point to the Wrap caller, got %q", got)
	}
	if !strings.Contains(Stack(err), "fault_test.go") {
		t.Fatal("full stack is not available on the typed fault")
	}
	if strings.Contains(Stack(err), cause.Error()) || strings.Contains(Stack(err), "agent:latest") {
		t.Fatal("stack exposed fault or cause text")
	}
	if got := Origin(New(CodeInvalidSpec, "invalid")); !strings.HasPrefix(got, "fault_test.go:") {
		t.Fatalf("origin should point to the New caller, got %q", got)
	}
	outer := Wrap(CodeAgentFailed, "outer", err, "retry failed")
	if Origin(outer) == Origin(err) {
		t.Fatal("outer fault reused the cause's origin")
	}
	wrapped := crerrors.WithStack(outer)
	if CodeOf(wrapped) != CodeAgentFailed || Origin(wrapped) != Origin(outer) || !errors.Is(wrapped, cause) {
		t.Fatal("library wrapper changed the stable code, origin, or cause chain")
	}
}

func TestDiagnosticBoundariesPreserveCodeAndCause(t *testing.T) {
	cause := errors.New("private subprocess detail")
	inner := Wrap(CodeSandboxCleanupUnknown, "remove container", cause, "cleanup failed")
	err := At(inner, "stop invocation")
	if CodeOf(err) != CodeSandboxCleanupUnknown || !errors.Is(err, cause) {
		t.Fatalf("operation changed category or cause: %v", err)
	}
	operations := Operations(err)
	if len(operations) != 2 || operations[0] != "stop invocation" || operations[1] != "remove container" {
		t.Fatalf("operations=%v", operations)
	}
	if strings.Contains(strings.Join(operations, " "), cause.Error()) || CauseType(err) == "" || Origin(err) == "" {
		t.Fatalf("diagnostic data incomplete or unsafe: %v", operations)
	}
	if len(Sites(err)) < 2 {
		t.Fatalf("nested error locations were lost: %v", Sites(err))
	}
	joined := errors.Join(err, New(CodeInternalError, "second failure"))
	if len(Operations(joined)) != 2 || CodeOf(joined) != CodeSandboxCleanupUnknown {
		t.Fatal("joined error lost the primary diagnostic chain")
	}
	if Ensure(err, "dispatch") != err || Ensure(nil, "dispatch") != nil {
		t.Fatal("Ensure changed an existing fault")
	}
	raw := Ensure(cause, "dispatch")
	if CodeOf(raw) != CodeInternalError || Origin(raw) == "" || !errors.Is(raw, cause) {
		t.Fatalf("raw error was not traced at the boundary: %v", raw)
	}
}
