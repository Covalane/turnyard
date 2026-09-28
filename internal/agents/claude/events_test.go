package claude

import (
	"reflect"
	"testing"

	"github.com/Covalane/turnyard/internal/fault"
)

func TestParseClaudeEvents(t *testing.T) {
	raw := `{"type":"assistant","session_id":"ses_1","message":{"model":"model-a","content":[{"type":"text","text":"done"},{"type":"tool_use","name":"Skill","input":{"skill":"review"}}]}}` + "\n" +
		`{"type":"result","session_id":"ses_1","is_error":false,"result":"done"}`
	result, err := parseClaudeEvents(raw, "model-a")
	if err != nil || result.NativeID != "ses_1" || len(result.Events) != 2 ||
		!reflect.DeepEqual(result.CalledTools, []string{"Skill"}) ||
		!reflect.DeepEqual(result.LoadedSkills, []string{"review"}) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	_, err = parseClaudeEvents(raw, "model-b")
	if fault.CodeOf(err) != fault.CodeModelMismatch {
		t.Fatalf("model mismatch accepted: %v", err)
	}
}
