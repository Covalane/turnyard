package opencode

import (
	"reflect"
	"testing"

	"github.com/Covalane/turnyard/internal/fault"
)

func TestParseOpenCodeEvents(t *testing.T) {
	raw := `{"type":"text","sessionID":"ses_1","part":{"type":"text","text":"done"}}` + "\n" +
		`{"type":"tool_use","sessionID":"ses_1","part":{"type":"tool","tool":"skill","state":{"status":"completed","input":{"name":"review"}}}}`
	result, err := parseOpenCodeEvents(raw)
	if err != nil || result.NativeID != "ses_1" || len(result.Events) != 2 ||
		!reflect.DeepEqual(result.CalledTools, []string{"skill"}) ||
		!reflect.DeepEqual(result.LoadedSkills, []string{"review"}) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	_, err = parseOpenCodeEvents(raw + "\n" + `{"type":"text","sessionID":"ses_2"}`)
	if fault.CodeOf(err) != fault.CodeNativeSessionMismatch {
		t.Fatalf("multiple sessions accepted: %v", err)
	}
}
