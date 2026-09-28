package kimi

import (
	"reflect"
	"testing"
)

func TestParseKimiEvents(t *testing.T) {
	raw := `{"role":"assistant","content":"done","tool_calls":[{"function":{"name":"Skill","arguments":"{\"skill\":\"review\"}"}},{"function":{"name":"mcp__files__read","arguments":"{}"}}]}`
	events, texts, tools, skills := parseKimiEvents(raw)
	if len(events) != 1 || !reflect.DeepEqual(texts, []string{"done"}) ||
		!reflect.DeepEqual(tools, []string{"Skill", "mcp__files__read"}) ||
		!reflect.DeepEqual(skills, []string{"review"}) {
		t.Fatalf("parsed events=%d texts=%v tools=%v skills=%v", len(events), texts, tools, skills)
	}
	ids, err := kimiSessions(`[{"id":"one"},{"id":""},{"id":"two"}]`)
	if err != nil || !reflect.DeepEqual(ids, []string{"one", "two"}) {
		t.Fatalf("sessions=%v err=%v", ids, err)
	}
}
