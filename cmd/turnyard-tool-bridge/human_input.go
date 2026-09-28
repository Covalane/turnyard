package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/humaninput"
)

const humanInputOutputDir = "/workspace/.turnyard-output"

// The tool acknowledges only a durably recorded request. The agent process
// then exits normally; Turnyard pauses after collecting the invocation result.
func serveHumanInput(in io.Reader, out io.Writer, outputDir string) error {
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
			response["result"] = map[string]any{"protocolVersion": mcpProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "turnyard-human-input", "version": "1.0.0"}}
		case mcpMethodToolsList:
			response["result"] = map[string]any{"tools": []any{map[string]any{
				"name":        contracts.HumanInputToolName,
				"description": "Pause this task for a human decision. Provide the exact question to show the operator. After this succeeds, finish the turn without continuing the blocked work; Turnyard will resume your native session with the human reply.",
				"inputSchema": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"question"}, "properties": map[string]any{
					"question": map[string]any{"type": "string", "minLength": 1, "maxLength": 4096},
				}},
			}}}
		case mcpMethodToolsCall:
			var params struct {
				Name      string `json:"name"`
				Arguments struct {
					Question string `json:"question"`
				} `json:"arguments"`
			}
			decoder := json.NewDecoder(bytes.NewReader(req.Params))
			decoder.DisallowUnknownFields()
			var trailing any
			if decoder.Decode(&params) != nil || decoder.Decode(&trailing) != io.EOF || params.Name != contracts.HumanInputToolName {
				response["error"] = map[string]any{"code": -32602, "message": "invalid human input arguments"}
			} else if err := humaninput.Write(outputDir, params.Arguments.Question); err != nil {
				response["result"] = map[string]any{"content": []any{map[string]string{"type": "text", "text": err.Error()}}, "isError": true}
			} else {
				response["result"] = map[string]any{"content": []any{map[string]string{"type": "text", "text": "Human input requested. Finish this turn and wait for Turnyard to resume the session."}}}
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
