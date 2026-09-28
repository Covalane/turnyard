package main

// These values belong to the MCP wire contract shared by the executable and
// delegation bridges. They are not Turnyard task lifecycle states.
const (
	mcpJSONRPCVersion   = "2.0"
	mcpProtocolVersion  = "2025-11-25"
	mcpMethodInitialize = "initialize"
	mcpMethodToolsList  = "tools/list"
	mcpMethodToolsCall  = "tools/call"
	mcpToolRun          = "run"
	mcpToolDelegate     = "delegate"
)
