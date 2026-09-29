package main

import (
	"bufio"
	"encoding/json"
	"io"
)

const (
	mcpInvalidParams = -32602
	mcpMethodMissing = -32601
	mcpBridgeVersion = "1.0.0"
)

type mcpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpRPCError    `json:"error,omitempty"`
}

type mcpTextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type mcpToolResult struct {
	Content []mcpTextContent `json:"content"`
	IsError bool             `json:"isError,omitempty"`
}

type mcpToolDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

type mcpInitializeResult struct {
	ProtocolVersion string `json:"protocolVersion"`
	Capabilities    struct {
		Tools struct{} `json:"tools"`
	} `json:"capabilities"`
	ServerInfo struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
}

type mcpToolsListResult struct {
	Tools []mcpToolDefinition `json:"tools"`
}

func textResult(message string, failed bool) mcpToolResult {
	return mcpToolResult{Content: []mcpTextContent{{Type: "text", Text: message}}, IsError: failed}
}

// mcpServer owns the common JSON-RPC framing. Each mode supplies only its
// tool definition and call behavior, so notifications and errors stay aligned.
type mcpServer struct {
	name string
	tool mcpToolDefinition
	call func(json.RawMessage) (any, *mcpRPCError)
}

func serveMCP(in io.Reader, out io.Writer, server mcpServer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), maxRequestBytes)
	writer := bufio.NewWriter(out)
	for scanner.Scan() {
		var req request
		if json.Unmarshal(scanner.Bytes(), &req) != nil || req.JSONRPC != mcpJSONRPCVersion || req.Method == "" || len(req.ID) == 0 || string(req.ID) == "null" {
			continue
		}
		response := mcpResponse{JSONRPC: mcpJSONRPCVersion, ID: req.ID}
		switch req.Method {
		case mcpMethodInitialize:
			result := mcpInitializeResult{ProtocolVersion: mcpProtocolVersion}
			result.ServerInfo.Name, result.ServerInfo.Version = server.name, mcpBridgeVersion
			response.Result = result
		case mcpMethodToolsList:
			response.Result = mcpToolsListResult{Tools: []mcpToolDefinition{server.tool}}
		case mcpMethodToolsCall:
			response.Result, response.Error = server.call(req.Params)
		default:
			response.Error = &mcpRPCError{Code: mcpMethodMissing, Message: "Method not found"}
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
