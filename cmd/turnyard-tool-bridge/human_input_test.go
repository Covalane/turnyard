package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Covalane/turnyard/internal/humaninput"
)

func TestHumanInputMCPRecordsQuestion(t *testing.T) {
	dir := t.TempDir()
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"request_input","arguments":{"question":"Which branch?"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"request_input","arguments":{"question":"Another branch?"}}}`,
	}, "\n") + "\n"
	var output bytes.Buffer
	if err := serveHumanInput(strings.NewReader(input), &output, dir); err != nil {
		t.Fatal(err)
	}
	var lines []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var item map[string]any
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, item)
	}
	if len(lines) != 4 || lines[2]["error"] != nil {
		t.Fatalf("MCP responses: %v", lines)
	}
	if result, ok := lines[3]["result"].(map[string]any); !ok || result["isError"] != true {
		t.Fatalf("conflicting question was accepted: %v", lines[3])
	}
	if got, err := humaninput.Read(dir); err != nil || got != "Which branch?" {
		t.Fatalf("request not recorded: %q %v", got, err)
	}
}
