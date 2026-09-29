package fault

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"

	crerrors "github.com/cockroachdb/errors"

	"github.com/Covalane/turnyard/internal/collections"
)

// Code is a stable machine-readable failure category, independent of text.
type Code string

const (
	CodeAgentFailed            Code = "AGENT_FAILED"
	CodeAgentOutputInvalid     Code = "AGENT_OUTPUT_INVALID"
	CodeAgentOutputTruncated   Code = "AGENT_OUTPUT_TRUNCATED"
	CodeAgentTimeout           Code = "AGENT_TIMEOUT"
	CodeAlreadyRunning         Code = "ALREADY_RUNNING"
	CodeAuthUnavailable        Code = "AUTH_UNAVAILABLE"
	CodeBranchDrift            Code = "BRANCH_DRIFT"
	CodeCandidateCorrupt       Code = "CANDIDATE_CORRUPT"
	CodeCandidateStale         Code = "CANDIDATE_STALE"
	CodeCapacityExceeded       Code = "CAPACITY_EXCEEDED"
	CodeCapacityWaitTimeout    Code = "CAPACITY_WAIT_TIMEOUT"
	CodeCapabilityMissing      Code = "CAPABILITY_MISSING"
	CodeCheckpointCorrupt      Code = "CHECKPOINT_CORRUPT"
	CodeConcurrentRun          Code = "CONCURRENT_RUN"
	CodeDaemonFailed           Code = "DAEMON_FAILED"
	CodeDaemonUnavailable      Code = "DAEMON_UNAVAILABLE"
	CodeDirtyCandidate         Code = "DIRTY_CANDIDATE"
	CodeDeliverableMissing     Code = "DELIVERABLE_MISSING"
	CodeDeliverableMismatch    Code = "DELIVERABLE_MISMATCH"
	CodeDeliverableInvalid     Code = "DELIVERABLE_INVALID"
	CodeDeliverableUnverified  Code = "DELIVERABLE_UNVERIFIED"
	CodeEnvironmentDrift       Code = "ENVIRONMENT_DRIFT"
	CodeGitError               Code = "GIT_ERROR"
	CodeHandoffUnavailable     Code = "HANDOFF_UNAVAILABLE"
	CodeHeadDrift              Code = "HEAD_DRIFT"
	CodeIdempotencyConflict    Code = "IDEMPOTENCY_CONFLICT"
	CodeImageInspectFailed     Code = "IMAGE_INSPECT_FAILED"
	CodeImageMissing           Code = "IMAGE_MISSING"
	CodeInputRequired          Code = "INPUT_REQUIRED"
	CodeInvalidJson            Code = "INVALID_JSON"
	CodeInvalidRepository      Code = "INVALID_REPOSITORY"
	CodeInvalidRequest         Code = "INVALID_REQUEST"
	CodeInvalidSpec            Code = "INVALID_SPEC"
	CodeInvalidTransition      Code = "INVALID_TRANSITION"
	CodeModelEvidenceMissing   Code = "MODEL_EVIDENCE_MISSING"
	CodeModelMismatch          Code = "MODEL_MISMATCH"
	CodeModelUnavailable       Code = "MODEL_UNAVAILABLE"
	CodeNativeSessionMismatch  Code = "NATIVE_SESSION_MISMATCH"
	CodeNativeSessionMissing   Code = "NATIVE_SESSION_MISSING"
	CodeNativeStateMissing     Code = "NATIVE_STATE_MISSING"
	CodeNativeStateUnavailable Code = "NATIVE_STATE_UNAVAILABLE"
	CodeNativeStateUnsafe      Code = "NATIVE_STATE_UNSAFE"
	CodeNotFound               Code = "NOT_FOUND"
	CodeRestoreConflict        Code = "RESTORE_CONFLICT"
	CodeRuntimeUnavailable     Code = "RUNTIME_UNAVAILABLE"
	CodeSandboxCleanupUnknown  Code = "SANDBOX_CLEANUP_UNKNOWN"
	CodeSandboxUnavailable     Code = "SANDBOX_UNAVAILABLE"
	CodeScopeViolation         Code = "SCOPE_VIOLATION"
	CodeSessionBusy            Code = "SESSION_BUSY"
	CodeSessionCorrupt         Code = "SESSION_CORRUPT"
	CodeToolUnavailable        Code = "TOOL_UNAVAILABLE"
	CodeUnknownAction          Code = "UNKNOWN_ACTION"
	CodeWorkspaceDrift         Code = "WORKSPACE_DRIFT"
	CodeWorkspaceMissing       Code = "WORKSPACE_MISSING"
	CodeInternalError          Code = "INTERNAL_ERROR"
	CodeInterrupted            Code = "INTERRUPTED"
	CodeReconciledUnknown      Code = "RECONCILED_UNKNOWN"
	CodeCheckError             Code = "CHECK_ERROR"
	CodeCheckFailed            Code = "CHECK_FAILED"
)

// Error preserves the originating cause and call site while exposing a stable code.
// Message is safe for the local CLI; callers must redact it before exporting telemetry.
type Error struct {
	Code      Code   `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
	Op        string `json:"-"`
	Cause     error  `json:"-"`
	Trace     error  `json:"-"`
}

func (e *Error) Error() string {
	message := e.Message
	if e.Cause != nil {
		if message != "" {
			message += ": "
		}
		message += e.Cause.Error()
	}
	if e.Op != "" {
		if message != "" {
			message = e.Op + ": " + message
		} else {
			message = e.Op
		}
	}
	return message
}
func (e *Error) Unwrap() error { return e.Cause }

// A fixed marker keeps task text and underlying errors out of the stack
// library's reportable safe details. The typed error retains that data locally.
var errStackMarker = errors.New("turnyard fault")

func New(code Code, format string, args ...any) error {
	return makeError(code, "", nil, crerrors.WithStackDepth(errStackMarker, 1), format, args...)
}
func Wrap(code Code, op string, cause error, format string, args ...any) error {
	return makeError(code, op, cause, crerrors.WithStackDepth(errStackMarker, 1), format, args...)
}

// Ensure gives an untyped error a stable code and call site at an application
// boundary. Typed faults retain their original code and trace.
func Ensure(err error, op string) error {
	if err == nil {
		return nil
	}
	var typed *Error
	if errors.As(err, &typed) {
		return err
	}
	return makeError(CodeInternalError, op, err, crerrors.WithStackDepth(errStackMarker, 1), "internal operation failed")
}

// At adds an operation to an error chain without changing its category.
// Use it at meaningful subsystem boundaries, not at every return statement.
func At(err error, op string) error {
	if err == nil {
		return nil
	}
	return makeError(CodeOf(err), op, err, crerrors.WithStackDepth(errStackMarker, 1), "")
}
func makeError(code Code, op string, cause, trace error, format string, args ...any) error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...), Op: op, Cause: cause, Trace: trace}
}
func CodeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return CodeInternalError
}

// Origin returns the outermost typed capture. Sites lists the inner captures
// too. Neither exposes untrusted error messages to operational logs.
func Origin(err error) string {
	var typed *Error
	if !errors.As(err, &typed) || typed.Trace == nil {
		return ""
	}
	file, line, _, ok := crerrors.GetOneLineSource(typed.Trace)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%s:%d", filepath.Base(file), line)
}

// Stack returns only the fixed-message capture, never the potentially
// sensitive fault message or its underlying cause.
func Stack(err error) string {
	var typed *Error
	if !errors.As(err, &typed) || typed.Trace == nil {
		return ""
	}
	return fmt.Sprintf("%+v", typed.Trace)
}

// Operations returns the named boundaries crossed by an error. It does not
// include error messages, which may contain agent output or credentials.
func Operations(err error) []string {
	var operations []string
	walk(err, func(current error) {
		if typed, ok := current.(*Error); ok && typed.Op != "" {
			operations = append(operations, typed.Op)
		}
	})
	return operations
}

// Sites lists captured locations across wrappers and joined cleanup errors.
func Sites(err error) []string {
	var sites []string
	seen := collections.Set[string]{}
	walk(err, func(current error) {
		typed, ok := current.(*Error)
		if !ok || typed.Trace == nil {
			return
		}
		trace := crerrors.GetReportableStackTrace(typed.Trace)
		if trace == nil || len(trace.Frames) == 0 {
			return
		}
		// Reportable traces retain the source path; GetOneLineSource only
		// returns a basename, which is ambiguous across packages.
		frame := trace.Frames[len(trace.Frames)-1]
		path := filepath.ToSlash(frame.AbsPath)
		if path == "" {
			path = filepath.ToSlash(frame.Filename)
		}
		for _, marker := range []string{"/internal/", "/cmd/", "/test/"} {
			if index := strings.LastIndex(path, marker); index >= 0 {
				path = path[index+1:]
				break
			}
		}
		if strings.HasPrefix(path, "/") {
			path = filepath.Base(path)
		}
		site := fmt.Sprintf("%s:%d", path, frame.Lineno)
		if seen.Add(site) {
			sites = append(sites, site)
		}
	})
	return sites
}

// CauseType identifies the innermost error type without logging its text.
func CauseType(err error) string {
	var kind string
	walk(err, func(current error) {
		if kind != "" || errors.Unwrap(current) != nil {
			return
		}
		if joined, ok := current.(interface{ Unwrap() []error }); ok && len(joined.Unwrap()) > 0 {
			return
		}
		kind = reflect.TypeOf(current).String()
	})
	return kind
}

func walk(err error, visit func(error)) {
	if err == nil {
		return
	}
	visit(err)
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			walk(child, visit)
		}
		return
	}
	walk(errors.Unwrap(err), visit)
}
