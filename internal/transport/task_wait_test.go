package transport

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/Covalane/turnyard/internal/fault"
)

func TestTaskWaitUsesTypedSupervisorResult(t *testing.T) {
	for _, body := range []string{
		`{}`,
		`{"task":{"status":"verified"}}`,
		`{"active":false,"task":{}}`,
		`{"active":"false","task":{"status":"verified"}}`,
	} {
		response := &clientResponse{Result: json.RawMessage(body), RequestID: "req_wait"}
		state, err := decodeClientResult[taskWaitResult](response)
		if err == nil {
			_, err = taskWaitDone(state)
		}
		if err == nil {
			t.Fatalf("malformed task result was accepted: %s", body)
		}
	}
	for _, tc := range []struct {
		body string
		done bool
	}{
		{`{"active":false,"task":{"status":"verified"}}`, true},
		{`{"active":true,"task":{"status":"verified"}}`, false},
	} {
		state, err := decodeClientResult[taskWaitResult](&clientResponse{Result: json.RawMessage(tc.body)})
		if err != nil {
			t.Fatal(err)
		}
		done, err := taskWaitDone(state)
		if err != nil || done != tc.done {
			t.Fatalf("unexpected wait state for %s: %v %v", tc.body, done, err)
		}
	}
}

func TestTypedSupervisorDecodeKeepsRequestID(t *testing.T) {
	_, err := decodeClientResult[taskWaitResult](&clientResponse{Result: json.RawMessage(`{`), RequestID: "req_trace"})
	var typed *fault.Error
	if !errors.As(err, &typed) || typed.RequestID != "req_trace" || typed.Code != fault.CodeDaemonUnavailable {
		t.Fatalf("invalid supervisor response lost request identity: %v", err)
	}
}
