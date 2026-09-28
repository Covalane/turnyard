// turnyard-tool-bridge exposes one configured executable as a local stdio MCP
// tool. It never invokes a shell: the model supplies only additional argv and
// optional stdin, while the trusted environment pins the executable prefix.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

const maxRequestBytes = 1 << 20
const maxOutputBytes = 256 << 10

type toolConfig struct {
	Description    string   `json:"description"`
	Argv           []string `json:"argv"`
	PassEnv        []string `json:"pass_env,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type cappedBuffer struct {
	bytes.Buffer
	truncated bool
}

func (b *cappedBuffer) Write(data []byte) (int, error) {
	remaining := maxOutputBytes - b.Len()
	if remaining > 0 {
		_, _ = b.Buffer.Write(data[:min(len(data), remaining)])
	}
	if len(data) > remaining {
		b.truncated = true
	}
	return len(data), nil
}

func main() {
	configPath := flag.String("config", "", "path to a Turnyard executable tool configuration")
	delegate := flag.Bool("delegate", false, "serve the managed delegation tool")
	flag.Parse()
	if *delegate {
		if *configPath != "" {
			fmt.Fprintln(os.Stderr, "delegate mode takes no config")
			os.Exit(2)
		}
		if err := serveDelegation(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "missing --config")
		os.Exit(2)
	}
	body, err := os.ReadFile(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	var config toolConfig
	if err := json.Unmarshal(body, &config); err != nil || len(config.Argv) == 0 || config.Argv[0] == "" || config.Description == "" || config.TimeoutSeconds < 0 || config.TimeoutSeconds > 600 {
		fmt.Fprintln(os.Stderr, "invalid tool configuration")
		os.Exit(2)
	}
	if err := serve(os.Stdin, os.Stdout, config); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func serve(in io.Reader, out io.Writer, config toolConfig) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), maxRequestBytes)
	writer := bufio.NewWriter(out)
	for scanner.Scan() {
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil || req.JSONRPC != mcpJSONRPCVersion || req.Method == "" {
			continue
		}
		if len(req.ID) == 0 || string(req.ID) == "null" {
			continue
		}
		response := map[string]any{"jsonrpc": mcpJSONRPCVersion, "id": req.ID}
		switch req.Method {
		case mcpMethodInitialize:
			response["result"] = map[string]any{"protocolVersion": mcpProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "turnyard-tool-bridge", "version": "1.0.0"}}
		case mcpMethodToolsList:
			response["result"] = map[string]any{"tools": []any{map[string]any{"name": mcpToolRun, "description": config.Description,
				"inputSchema": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
					"args":  map[string]any{"type": "array", "items": map[string]string{"type": "string"}, "description": "Additional command arguments"},
					"stdin": map[string]string{"type": "string", "description": "Optional text sent to standard input"}}}}}}
		case mcpMethodToolsCall:
			result, err := call(req.Params, config)
			if err != nil {
				response["error"] = map[string]any{"code": -32602, "message": err.Error()}
			} else {
				response["result"] = result
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

func call(raw json.RawMessage, config toolConfig) (map[string]any, error) {
	var params struct {
		Name      string `json:"name"`
		Arguments struct {
			Args  []string `json:"args"`
			Stdin string   `json:"stdin"`
		} `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &params); err != nil || params.Name != mcpToolRun || len(params.Arguments.Args) > 64 || len(params.Arguments.Stdin) > maxRequestBytes {
		return nil, fmt.Errorf("invalid executable tool arguments")
	}
	for _, arg := range params.Arguments.Args {
		if len(arg) > 8192 || strings.ContainsRune(arg, 0) {
			return nil, fmt.Errorf("invalid executable tool argument")
		}
	}
	timeout := config.TimeoutSeconds
	if timeout == 0 {
		timeout = 120
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()
	argv := append(append([]string{}, config.Argv...), params.Arguments.Args...)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.WaitDelay = 2 * time.Second
	cmd.Stdin = strings.NewReader(params.Arguments.Stdin)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
	for _, name := range config.PassEnv {
		if value, ok := os.LookupEnv(name); ok {
			cmd.Env = append(cmd.Env, name+"="+value)
		}
	}
	var stdout, stderr cappedBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	message := stdout.String()
	if stderr.Len() > 0 {
		message += "\nstderr:\n" + stderr.String()
	}
	if stdout.truncated || stderr.truncated {
		message += "\n[output truncated]"
	}
	if err != nil {
		message += "\ncommand failed: " + err.Error()
	}
	if message == "" {
		message = "command completed without output"
	}
	return map[string]any{"content": []any{map[string]string{"type": "text", "text": message}}, "isError": err != nil}, nil
}
