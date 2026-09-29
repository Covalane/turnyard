package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestMCPServerIgnoresNotificationsAndKeepsProtocolErrors(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","method":"tools/call","params":{}}`,
		`{"jsonrpc":"1.0","id":1,"method":"tools/call","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"unknown"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{}}`,
	}, "\n") + "\n"
	calls := 0
	var output bytes.Buffer
	err := serveMCP(strings.NewReader(input), &output, mcpServer{
		name: "test", tool: mcpToolDefinition{Name: "test"},
		call: func(json.RawMessage) (any, *mcpRPCError) {
			calls++
			return nil, &mcpRPCError{Code: mcpInvalidParams, Message: "invalid arguments"}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("tool executed %d times; notifications must be ignored", calls)
	}
	var responses []mcpResponse
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var response mcpResponse
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			t.Fatal(err)
		}
		responses = append(responses, response)
	}
	if len(responses) != 2 || responses[0].JSONRPC != mcpJSONRPCVersion || string(responses[0].ID) != "2" || responses[0].Error == nil || responses[0].Error.Code != mcpMethodMissing || string(responses[1].ID) != "3" || responses[1].Error == nil || responses[1].Error.Code != mcpInvalidParams {
		t.Fatalf("unexpected protocol responses: %+v", responses)
	}
}

func TestDelegationMCPKeepsToolDiscoveryAndArgumentErrors(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"wrong","arguments":{}}}`,
	}, "\n") + "\n"
	var output bytes.Buffer
	if err := serveDelegation(strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected three responses: %s", output.String())
	}
	var discovery struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &discovery); err != nil || len(discovery.Result.Tools) != 1 || discovery.Result.Tools[0].Name != mcpToolDelegate {
		t.Fatalf("delegation tool discovery changed: %s (%v)", lines[1], err)
	}
	var call mcpResponse
	if err := json.Unmarshal([]byte(lines[2]), &call); err != nil || call.Error == nil || call.Error.Code != mcpInvalidParams {
		t.Fatalf("invalid delegation arguments were accepted: %s (%v)", lines[2], err)
	}
}
