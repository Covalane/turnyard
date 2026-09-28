package codex

import (
	"reflect"
	"testing"
)

func TestParseCodexEvents(t *testing.T) {
	raw := `{"type":"thread.started","thread_id":"thr_1"}` + "\n" +
		`{"type":"item.completed","item":{"type":"command_execution","status":"completed","command":"cat /state/home/.agents/skills/review/SKILL.md"}}` + "\n" +
		`{"type":"item.completed","item":{"type":"mcp_tool_call","status":"completed","server":"files","tool":"read"}}` + "\n" +
		`{"type":"turn.completed"}`
	result, err := parseCodexEvents(raw)
	if err != nil || result.NativeID != "thr_1" || len(result.Events) != 4 ||
		!reflect.DeepEqual(result.CalledTools, []string{"mcp__files__read"}) ||
		!reflect.DeepEqual(result.LoadedSkills, []string{"review"}) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
