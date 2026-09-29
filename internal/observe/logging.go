package observe

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"syscall"

	"github.com/Covalane/turnyard/internal/collections"
	"github.com/Covalane/turnyard/internal/fault"
)

// IDs are correlation fields only. Agent text, credentials, and repository
// contents must never be placed here or in operational logs.
type IDs struct {
	RequestID    string
	SessionID    string
	TaskID       string
	InvocationID string
}

type contextKey struct{}

func WithIDs(ctx context.Context, update IDs) context.Context {
	current := IDsFrom(ctx)
	if update.RequestID != "" {
		current.RequestID = update.RequestID
	}
	if update.SessionID != "" {
		current.SessionID = update.SessionID
	}
	if update.TaskID != "" {
		current.TaskID = update.TaskID
	}
	if update.InvocationID != "" {
		current.InvocationID = update.InvocationID
	}
	return context.WithValue(ctx, contextKey{}, current)
}

func IDsFrom(ctx context.Context) IDs {
	if ctx == nil {
		return IDs{}
	}
	if ids, ok := ctx.Value(contextKey{}).(IDs); ok {
		return ids
	}
	return IDs{}
}

type correlationHandler struct{ slog.Handler }

func (h correlationHandler) Handle(ctx context.Context, record slog.Record) error {
	ids := IDsFrom(ctx)
	present := collections.Set[string]{}
	record.Attrs(func(attr slog.Attr) bool { present.Add(attr.Key); return true })
	if ids.RequestID != "" {
		if !present.Has("request_id") {
			record.AddAttrs(slog.String("request_id", ids.RequestID))
		}
	}
	if ids.SessionID != "" {
		if !present.Has("session_id") {
			record.AddAttrs(slog.String("session_id", ids.SessionID))
		}
	}
	if ids.TaskID != "" {
		if !present.Has("task_id") {
			record.AddAttrs(slog.String("task_id", ids.TaskID))
		}
	}
	if ids.InvocationID != "" {
		if !present.Has("invocation_id") {
			record.AddAttrs(slog.String("invocation_id", ids.InvocationID))
		}
	}
	return h.Handler.Handle(ctx, record)
}
func (h correlationHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return correlationHandler{h.Handler.WithAttrs(attrs)}
}
func (h correlationHandler) WithGroup(name string) slog.Handler {
	return correlationHandler{h.Handler.WithGroup(name)}
}

var Log = slog.New(correlationHandler{slog.NewJSONHandler(os.Stderr, nil)})

// LogFailure records diagnostic metadata without copying possibly sensitive
// error messages or subprocess output into the operational log.
func LogFailure(ctx context.Context, message string, err error, attrs ...any) {
	fields := []any{"code", fault.CodeOf(err), "origin", fault.Origin(err), "sites", fault.Sites(err),
		"operations", fault.Operations(err), "cause_type", fault.CauseType(err)}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		fields = append(fields, "exit_code", exit.ExitCode())
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		fields = append(fields, "errno", int(errno))
	}
	if errors.Is(err, context.DeadlineExceeded) {
		fields = append(fields, "deadline_exceeded", true)
	} else if errors.Is(err, context.Canceled) {
		fields = append(fields, "cancelled", true)
	}
	Log.ErrorContext(ctx, message, append(fields, attrs...)...)
}
