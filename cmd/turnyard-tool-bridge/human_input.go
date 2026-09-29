package main

import (
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
	tool := mcpToolDefinition{
		Name:        contracts.HumanInputToolName,
		Description: "Pause this task for a human decision. Provide the exact question to show the operator. After this succeeds, finish the turn without continuing the blocked work; Turnyard will resume your native session with the human reply.",
		InputSchema: map[string]any{"type": "object", "additionalProperties": false, "required": []string{"question"}, "properties": map[string]any{
			"question": map[string]any{"type": "string", "minLength": 1, "maxLength": 4096},
		}},
	}
	return serveMCP(in, out, mcpServer{name: "turnyard-human-input", tool: tool, call: func(raw json.RawMessage) (any, *mcpRPCError) {
		var params struct {
			Name      string `json:"name"`
			Arguments struct {
				Question string `json:"question"`
			} `json:"arguments"`
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		var trailing any
		if decoder.Decode(&params) != nil || decoder.Decode(&trailing) != io.EOF || params.Name != contracts.HumanInputToolName {
			return nil, &mcpRPCError{Code: mcpInvalidParams, Message: "invalid human input arguments"}
		}
		if err := humaninput.Write(outputDir, params.Arguments.Question); err != nil {
			return textResult(err.Error(), true), nil
		}
		return textResult("Human input requested. Finish this turn and wait for Turnyard to resume the session.", false), nil
	}})
}
