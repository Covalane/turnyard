package store

import (
	"encoding/json"
	"testing"
)

func TestRowJSONKeepsNullableWireFields(t *testing.T) {
	cases := []struct {
		name       string
		row        any
		nullFields []string
		valueField string
		value      any
	}{
		{"session", SessionRow{NativeID: OptionalString{Value: "native", Present: true}}, []string{"completed_at", "cancelled_at", "cancel_reason", "parent_session_id", "parent_task_id", "parent_invocation_id", "delegate_agent_id", "delegation_request_digest"}, "native_id", "native"},
		{"task", TaskRow{CandidateID: OptionalString{Value: "candidate", Present: true}}, []string{"error_code"}, "candidate_id", "candidate"},
		{"invocation", InvocationRow{ExitCode: OptionalInt{Value: 7, Present: true}}, []string{"native_id", "log_path", "ended_at"}, "exit_code", float64(7)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.row)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			for _, name := range tc.nullFields {
				if value, exists := fields[name]; !exists || value != nil {
					t.Fatalf("%s should be JSON null: %s", name, data)
				}
			}
			if fields[tc.valueField] != tc.value {
				t.Fatalf("%s should retain its value: %s", tc.valueField, data)
			}
		})
	}
}
