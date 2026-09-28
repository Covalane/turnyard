package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/Covalane/turnyard/internal/contracts"
)

var invocationPattern = regexp.MustCompile(`^inv_[a-f0-9]{16}$`)

// The bridge writes a request to its invocation directory and reads a response
// from a separate read-only mount. It never sees the supervisor control socket.
func serveDelegation(in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), maxRequestBytes)
	writer := bufio.NewWriter(out)
	for scanner.Scan() {
		var req request
		if json.Unmarshal(scanner.Bytes(), &req) != nil || req.JSONRPC != mcpJSONRPCVersion || len(req.ID) == 0 || string(req.ID) == "null" {
			continue
		}
		response := map[string]any{"jsonrpc": mcpJSONRPCVersion, "id": req.ID}
		switch req.Method {
		case mcpMethodInitialize:
			response["result"] = map[string]any{"protocolVersion": mcpProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "turnyard-delegation", "version": "1.0.0"}}
		case mcpMethodToolsList:
			response["result"] = map[string]any{"tools": []any{map[string]any{
				"name": mcpToolDelegate, "description": "Launch, inspect or continue a managed child agent. For submit, copy the complete actionable child objective and concrete acceptance conditions from the parent task; short placeholders such as x/y are rejected. Include a stable key, allowed agentId, full repository scope and at least one check or deliverable. The child works in an isolated session. status reads its candidate-bound result. continue resumes needs_input with reply or failed with retry=true. Never invent an additional byte count or conflicting requirement.",
				"inputSchema": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"action", "key"}, "properties": map[string]any{
					"action": map[string]any{"enum": []contracts.DelegationAction{contracts.DelegationActionSubmit, contracts.DelegationActionStatus, contracts.DelegationActionContinue}},
					"key":    map[string]string{"type": "string"}, "agentId": map[string]string{"type": "string"},
					"objective":    map[string]any{"type": "string", "minLength": 2, "description": "Complete child task objective; never a placeholder"},
					"acceptance":   map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string", "minLength": 2}},
					"scope":        map[string]any{"type": "array", "items": map[string]any{"type": "object", "required": []string{"id", "mode"}, "additionalProperties": false, "properties": map[string]any{"id": map[string]string{"type": "string"}, "mode": map[string]any{"enum": []contracts.ScopeMode{contracts.ScopeRead, contracts.ScopeWrite}}}}},
					"checks":       map[string]any{"type": "array", "items": map[string]string{"type": "string"}},
					"deliverables": map[string]any{"type": "array", "items": map[string]string{"type": "object"}},
					"inputIds":     map[string]any{"type": "array", "items": map[string]string{"type": "string"}},
					"reply":        map[string]string{"type": "string"}, "retry": map[string]string{"type": "boolean"},
					"timeoutSeconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 7200},
				}, "allOf": []any{map[string]any{"if": map[string]any{"properties": map[string]any{"action": map[string]contracts.DelegationAction{"const": contracts.DelegationActionSubmit}}},
					"then": map[string]any{"required": []string{"key", "agentId", "objective", "acceptance", "scope"}}}},
				},
			}}}
		case mcpMethodToolsCall:
			var params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if json.Unmarshal(req.Params, &params) != nil || params.Name != mcpToolDelegate || len(params.Arguments) > maxRequestBytes {
				response["error"] = map[string]any{"code": -32602, "message": "invalid delegation arguments"}
			} else {
				result, err := callDelegation(params.Arguments)
				if err != nil {
					response["result"] = map[string]any{"content": []any{map[string]string{"type": "text", "text": err.Error()}}, "isError": true}
				} else {
					response["result"] = map[string]any{"content": []any{map[string]string{"type": "text", "text": string(result)}}}
				}
			}
		default:
			response["error"] = map[string]any{"code": -32601, "message": "Method not found"}
		}
		body, err := json.Marshal(response)
		if err != nil {
			return err
		}
		if _, err := writer.Write(body); err != nil {
			return err
		}
		if err := writer.WriteByte('\n'); err != nil {
			return err
		}
		if err := writer.Flush(); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func callDelegation(args json.RawMessage) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 115*time.Second)
	defer cancel()
	invocation := os.Getenv("TURNYARD_DELEGATION_INVOCATION")
	if !invocationPattern.MatchString(invocation) {
		return nil, fmt.Errorf("delegation invocation is unavailable")
	}
	requestDir := filepath.Join("/state/delegation-requests", invocation)
	info, err := os.Lstat(requestDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("delegation request directory is unavailable")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	name := hex.EncodeToString(nonce[:]) + ".json"
	tmp := filepath.Join(requestDir, "."+name+".tmp")
	if err := os.WriteFile(tmp, args, 0o600); err != nil {
		return nil, err
	}
	defer os.Remove(tmp)
	if err := os.Rename(tmp, filepath.Join(requestDir, name)); err != nil {
		return nil, err
	}
	responsePath := filepath.Join("/turnyard-control/responses", name)
	var response struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	for {
		body, err := os.ReadFile(responsePath)
		if err == nil {
			if len(body) > maxRequestBytes {
				return nil, fmt.Errorf("delegation response exceeds limit")
			}
			if err := json.Unmarshal(body, &response); err != nil {
				return nil, err
			}
			break
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("delegation response timed out")
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !response.OK {
		return nil, fmt.Errorf("delegation rejected: %s", response.Error)
	}
	return response.Result, nil
}
