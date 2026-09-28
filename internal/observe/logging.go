package observe

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"syscall"

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
	present := map[string]bool{}
	record.Attrs(func(attr slog.Attr) bool { present[attr.Key] = true; return true })
	if ids.RequestID != "" {
		if !present["requestId"] {
			record.AddAttrs(slog.String("requestId", ids.RequestID))
		}
	}
	if ids.SessionID != "" {
		if !present["sessionId"] {
			record.AddAttrs(slog.String("sessionId", ids.SessionID))
		}
	}
	if ids.TaskID != "" {
		if !present["taskId"] {
			record.AddAttrs(slog.String("taskId", ids.TaskID))
		}
	}
	if ids.InvocationID != "" {
		if !present["invocationId"] {
			record.AddAttrs(slog.String("invocationId", ids.InvocationID))
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
		"operations", fault.Operations(err), "causeType", fault.CauseType(err)}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		fields = append(fields, "exitCode", exit.ExitCode())
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		fields = append(fields, "errno", int(errno))
	}
	if errors.Is(err, context.DeadlineExceeded) {
		fields = append(fields, "deadlineExceeded", true)
	} else if errors.Is(err, context.Canceled) {
		fields = append(fields, "cancelled", true)
	}
	Log.ErrorContext(ctx, message, append(fields, attrs...)...)
}
