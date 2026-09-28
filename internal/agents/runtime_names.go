package agents

// Built-in runtime names are registry keys, not a closed set of supported
// agents. Additional drivers may register other names.
const (
	RuntimeClaude   = "claude"
	RuntimeCodex    = "codex"
	RuntimeKimi     = "kimi"
	RuntimeOpenCode = "opencode"
)
