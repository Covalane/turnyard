// Witness is a small stdio MCP server used by the real injection test.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  struct {
		Arguments struct {
			Value string `json:"value"`
		} `json:"arguments"`
	} `json:"params"`
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	out := bufio.NewWriter(os.Stdout)
	for scanner.Scan() {
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			fmt.Fprintln(os.Stderr, "invalid MCP request:", err)
			continue
		}
		if len(req.ID) == 0 || string(req.ID) == "null" {
			continue
		}
		response := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		switch req.Method {
		case "initialize":
			response["result"] = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "turnyard-witness", "version": "0.1.0"}}
		case "tools/list":
			response["result"] = map[string]any{"tools": []any{map[string]any{"name": "stamp", "description": "Return a deterministic witness token for a supplied value.", "inputSchema": map[string]any{"type": "object", "required": []string{"value"}, "properties": map[string]any{"value": map[string]string{"type": "string"}}}}}}
		case "tools/call":
			response["result"] = map[string]any{"content": []any{map[string]string{"type": "text", "text": "MCP_WITNESS:" + req.Params.Arguments.Value}}}
		default:
			response["error"] = map[string]any{"code": -32601, "message": "Method not found"}
		}
		data, err := json.Marshal(response)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			continue
		}
		if _, err := out.Write(data); err != nil {
			fmt.Fprintln(os.Stderr, "MCP response write failed:", err)
			os.Exit(1)
		}
		if err := out.WriteByte('\n'); err != nil {
			fmt.Fprintln(os.Stderr, "MCP response newline failed:", err)
			os.Exit(1)
		}
		if err := out.Flush(); err != nil {
			fmt.Fprintln(os.Stderr, "MCP response flush failed:", err)
			os.Exit(1)
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
