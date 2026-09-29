package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMCPBridgeRunsExecutableWithoutInterpretingArgumentsAsShell(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "should-not-exist")
	argument := "$(touch " + marker + ")"
	requests := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"run","arguments":{"args":[` + stringJSON(argument) + `]}}}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := serve(strings.NewReader(requests), &out, toolConfig{Description: "Print an argument", Argv: []string{"printf", "%s"}}); err != nil {
		t.Fatal(err)
	}
	var responses []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var value map[string]any
		if err := json.Unmarshal([]byte(line), &value); err != nil {
			t.Fatal(err)
		}
		responses = append(responses, value)
	}
	if len(responses) != 3 || !strings.Contains(out.String(), `"name":"run"`) || !strings.Contains(out.String(), argument) {
		t.Fatalf("MCP handshake or command result missing: %s", out.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("model argument was evaluated by a shell: %v", err)
	}
}

func TestExecutableFailureAndCredentialFiltering(t *testing.T) {
	t.Setenv("TURNYARD_TOOL_TEST_ALLOWED", "visible")
	t.Setenv("TURNYARD_TOOL_TEST_HIDDEN", "secret")
	params := json.RawMessage(`{"name":"run","arguments":{"args":[]}}`)
	result, err := call(params, toolConfig{Description: "Show environment", Argv: []string{"env"}, PassEnv: []string{"TURNYARD_TOOL_TEST_ALLOWED"}})
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "TURNYARD_TOOL_TEST_ALLOWED=visible") || strings.Contains(text, "TURNYARD_TOOL_TEST_HIDDEN") {
		t.Fatalf("tool environment was not filtered: %s", text)
	}
	result, err = call(params, toolConfig{Description: "Fail", Argv: []string{"false"}})
	if err != nil || !result.IsError {
		t.Fatalf("nonzero exit was not reported: %+v %v", result, err)
	}
}

func TestExecutableTimeoutIsReturnedAsToolError(t *testing.T) {
	start := time.Now()
	result, err := call(json.RawMessage(`{"name":"run","arguments":{}}`),
		toolConfig{Description: "Sleep", Argv: []string{"sleep", "5"}, TimeoutSeconds: 1})
	if err != nil || !result.IsError || time.Since(start) > 3*time.Second {
		t.Fatalf("timed out command was not stopped: %+v %v", result, err)
	}
}

func stringJSON(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}
