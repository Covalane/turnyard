package transport

import (
	"context"
	"errors"
	"testing"

	"github.com/Covalane/turnyard/internal/fault"
)

func TestUnavailableSupervisorKeepsClientRequestID(t *testing.T) {
	_, err := ClientRequest(context.Background(), t.TempDir(), Request{Action: actionPing, RequestID: "req_trace_test"}, false)
	if fault.CodeOf(err) != fault.CodeDaemonUnavailable {
		t.Fatalf("unexpected client error: %v", err)
	}
	var typed *fault.Error
	if !errors.As(err, &typed) || typed.RequestID != "req_trace_test" || fault.Origin(err) == "" {
		t.Fatalf("client error cannot be correlated: %v", err)
	}
}
