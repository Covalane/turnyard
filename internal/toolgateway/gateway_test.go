package toolgateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestGatewayBackendProcess(t *testing.T) {
	if os.Getenv("TURNYARD_TEST_GATEWAY_BACKEND") != "1" {
		return
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "tool-test", Version: "1"}, nil)
	type input struct {
		Value string `json:"value"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "Echo a supplied value"},
		func(_ context.Context, _ *mcp.CallToolRequest, value input) (*mcp.CallToolResult, input, error) {
			return nil, value, nil
		})
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		t.Fatal(err)
	}
}

func TestModelRankingOnlyReturnsGrantedTools(t *testing.T) {
	t.Setenv("TURNYARD_TEST_GATEWAY_BACKEND", "1")
	t.Setenv("TURNYARD_TEST_MATCHER_KEY", "private-key")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer private-key" {
			t.Errorf("unexpected matcher request: %s %s", r.URL.Path, r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{
			"content": `{"tool_ids":["ungranted/delete","allowed/echo","allowed/echo"]}`}}}})
	}))
	defer server.Close()
	config := Config{Backends: []Backend{{ID: "allowed", Argv: []string{os.Args[0], "-test.run=^TestGatewayBackendProcess$"}, PassEnv: []string{"TURNYARD_TEST_GATEWAY_BACKEND"}}},
		Matcher: &MatcherConfig{Endpoint: server.URL + "/v1", Model: "matcher", CredentialEnv: "TURNYARD_TEST_MATCHER_KEY"}}
	gateway, err := New(config, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	gateway.matcher.client = server.Client()
	found, err := gateway.Find(context.Background(), "请回显一个值", 3)
	if err != nil || len(found.Tools) != 1 || found.Tools[0].ID != "allowed/echo" {
		t.Fatalf("model ranking escaped grant boundary: %+v %v", found, err)
	}
}

func TestSearchCallAndCatalogDrift(t *testing.T) {
	t.Setenv("TURNYARD_TEST_GATEWAY_BACKEND", "1")
	config := Config{Backends: []Backend{{ID: "allowed", Argv: []string{os.Args[0], "-test.run=^TestGatewayBackendProcess$"}, PassEnv: []string{"TURNYARD_TEST_GATEWAY_BACKEND"}}},
		CatalogLock: filepath.Join(t.TempDir(), "catalog.sha256")}
	gateway, err := New(config, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	ctx := context.Background()
	found, err := gateway.Find(ctx, "echo value", 2)
	if err != nil || len(found.Tools) != 1 || found.Tools[0].ID != "allowed/echo" || found.Tools[0].InputSchema == nil {
		t.Fatalf("search did not return an authorized tool and schema: %+v %v", found, err)
	}
	encoded, err := json.Marshal(found)
	if err != nil || !strings.Contains(string(encoded), `"input_schema"`) {
		t.Fatalf("search result did not use the Turnyard JSON field name: %s %v", encoded, err)
	}
	if _, err := gateway.Call(ctx, "ungranted/echo", map[string]any{"value": "hello"}); err == nil || !strings.Contains(err.Error(), "not granted") {
		t.Fatalf("ungranted tool call was accepted: %v", err)
	}
	if _, err := gateway.Call(ctx, "allowed/echo", map[string]any{"value": 7}); err == nil || !strings.Contains(err.Error(), "invalid arguments") {
		t.Fatalf("schema-invalid tool call was accepted: %v", err)
	}
	result, err := gateway.Call(ctx, "allowed/echo", map[string]any{"value": "hello"})
	if err != nil || result.IsError || len(result.Content) == 0 || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "hello") {
		t.Fatalf("authorized tool call failed: %+v %v", result, err)
	}
	if err := gateway.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.CatalogLock, []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(config, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if _, err := restarted.Find(ctx, "echo", 1); err == nil || !strings.Contains(err.Error(), "catalog changed") {
		t.Fatalf("changed catalog was accepted on restart: %v", err)
	}
}
