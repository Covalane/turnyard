// Package toolgateway exposes a small, runtime-independent MCP surface over
// the tools explicitly selected for one Turnyard agent.
package toolgateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	ServerID        = "turnyard_tools"
	FindToolName    = "find_tools"
	CallToolName    = "call_tool"
	MaxSearchResult = 8
	maxToolResult   = 256 << 10
	maxFindResult   = 64 << 10
	maxCatalogTools = 4096
)

// Backend is a sandbox-local MCP process. The configuration carries names of
// permitted environment variables, never their values.
type Backend struct {
	ID             string   `json:"id"`
	Argv           []string `json:"argv"`
	PassEnv        []string `json:"passEnv,omitempty"`
	TimeoutSeconds int      `json:"timeoutSeconds,omitempty"`
}

type Config struct {
	Backends    []Backend      `json:"backends"`
	CatalogLock string         `json:"catalogLock,omitempty"`
	Matcher     *MatcherConfig `json:"matcher,omitempty"`
}

type candidate struct {
	ID          string `json:"id"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"inputSchema"`
}

type findInput struct {
	Query string `json:"query" jsonschema:"Search for the task capability, using names or natural language"`
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum matches to return, from 1 to 8"`
}

type findOutput struct {
	Tools []candidate `json:"tools"`
}

type searchMatch struct {
	item  candidate
	score int
}

type callInput struct {
	ToolID    string         `json:"toolId" jsonschema:"Exact tool ID returned by find_tools"`
	Arguments map[string]any `json:"arguments" jsonschema:"Arguments matching that tool's input schema"`
}

type catalogEntry struct {
	candidate
	nativeName string
	backend    *runningBackend
	schema     *jsonschema.Schema
}

type runningBackend struct {
	session *mcp.ClientSession
	config  Backend
}

// Gateway freezes one backend catalog for the life of the process. A lock in
// session state detects changed tool definitions after a resumed invocation.
type Gateway struct {
	config   Config
	logger   *slog.Logger
	mu       sync.Mutex
	ready    bool
	entries  map[string]*catalogEntry
	backends []*runningBackend
	matcher  *LLMMatcher
}

func New(config Config, logger *slog.Logger) (*Gateway, error) {
	ids := make(map[string]bool, len(config.Backends))
	for _, backend := range config.Backends {
		if backend.ID == "" || strings.Contains(backend.ID, "/") || ids[backend.ID] || len(backend.Argv) == 0 || backend.Argv[0] == "" || backend.TimeoutSeconds < 0 {
			return nil, fmt.Errorf("invalid tool gateway backend configuration")
		}
		ids[backend.ID] = true
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	gateway := &Gateway{config: config, logger: logger}
	if config.Matcher != nil {
		matcher, err := NewLLMMatcher(*config.Matcher)
		if err != nil {
			return nil, err
		}
		gateway.matcher = matcher
	}
	return gateway, nil
}

func (g *Gateway) Server() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "turnyard-tool-gateway", Version: "1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: FindToolName, Description: "Find tools available for this task. Returns exact tool IDs and argument schemas."},
		func(ctx context.Context, _ *mcp.CallToolRequest, input findInput) (*mcp.CallToolResult, findOutput, error) {
			output, err := g.Find(ctx, input.Query, input.Limit)
			return nil, output, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: CallToolName, Description: "Call one tool previously found with find_tools, using its exact ID and schema."},
		func(ctx context.Context, _ *mcp.CallToolRequest, input callInput) (*mcp.CallToolResult, any, error) {
			result, err := g.Call(ctx, input.ToolID, input.Arguments)
			return result, nil, err
		})
	return server
}

// Verify discovers every granted backend before the agent starts. This makes
// a connected gateway mean its catalog and pinned schemas are actually ready.
func (g *Gateway) Verify(ctx context.Context) error {
	return g.ensureCatalog(ctx)
}

func (g *Gateway) Find(ctx context.Context, query string, limit int) (findOutput, error) {
	if err := g.ensureCatalog(ctx); err != nil {
		return findOutput{}, err
	}
	if limit <= 0 || limit > MaxSearchResult {
		limit = MaxSearchResult
	}
	words := searchWords(query)
	matches := make([]searchMatch, 0, len(g.entries))
	for _, entry := range g.entries {
		score := matchScore(entry.candidate, words)
		if score > 0 || len(words) == 0 {
			matches = append(matches, searchMatch{item: entry.candidate, score: score})
		}
	}
	slices.SortFunc(matches, func(a, b searchMatch) int {
		if a.score != b.score {
			return b.score - a.score
		}
		return strings.Compare(a.item.ID, b.item.ID)
	})
	if g.matcher != nil && needsModelRanking(matches) {
		pool := make([]candidate, 0, len(g.entries))
		for _, entry := range g.entries {
			pool = append(pool, entry.candidate)
		}
		slices.SortFunc(pool, func(a, b candidate) int { return strings.Compare(a.ID, b.ID) })
		if len(pool) <= MaxModelCandidates {
			ids, err := g.matcher.Rank(ctx, query, pool)
			if err != nil {
				g.logger.WarnContext(ctx, "tool model ranking unavailable; using lexical search", "error", err)
			} else if len(ids) > 0 {
				output := findOutput{Tools: make([]candidate, 0, min(limit, len(ids)))}
				for _, id := range ids[:min(limit, len(ids))] {
					if err := appendSearchCandidate(&output, g.entries[id].candidate); err != nil {
						return findOutput{}, err
					}
				}
				g.logger.InfoContext(ctx, "tool model search", "matches", len(output.Tools))
				return output, nil
			}
		}
	}
	output := findOutput{Tools: make([]candidate, 0, min(limit, len(matches)))}
	for _, item := range matches[:min(limit, len(matches))] {
		if err := appendSearchCandidate(&output, item.item); err != nil {
			return findOutput{}, err
		}
	}
	g.logger.InfoContext(ctx, "tool search", "matches", len(output.Tools))
	return output, nil
}

func appendSearchCandidate(output *findOutput, item candidate) error {
	output.Tools = append(output.Tools, item)
	encoded, err := json.Marshal(output)
	if err != nil {
		return err
	}
	if len(encoded) > maxFindResult {
		output.Tools = output.Tools[:len(output.Tools)-1]
		if len(output.Tools) == 0 {
			return fmt.Errorf("tool schema exceeds the search result size limit")
		}
	}
	return nil
}

func needsModelRanking(matches []searchMatch) bool {
	return len(matches) == 0 || matches[0].score < 6 || len(matches) > 1 && matches[0].score-matches[1].score <= 2
}

func (g *Gateway) Call(ctx context.Context, id string, arguments map[string]any) (*mcp.CallToolResult, error) {
	if err := g.ensureCatalog(ctx); err != nil {
		return nil, err
	}
	entry := g.entries[id]
	if entry == nil {
		return nil, fmt.Errorf("tool %q is not granted to this agent", id)
	}
	if arguments == nil {
		arguments = map[string]any{}
	}
	if err := entry.schema.Validate(arguments); err != nil {
		return nil, fmt.Errorf("invalid arguments for %q: %w", id, err)
	}
	timeout := entry.backend.config.TimeoutSeconds
	if timeout == 0 {
		timeout = 120
	}
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	start := time.Now()
	result, err := entry.backend.session.CallTool(callCtx, &mcp.CallToolParams{Name: entry.nativeName, Arguments: arguments})
	if err != nil {
		g.logger.WarnContext(ctx, "tool call failed", "tool", id, "durationMs", time.Since(start).Milliseconds(), "error", err)
		return nil, fmt.Errorf("call %q: %w", id, err)
	}
	body, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("encode %q result: %w", id, err)
	}
	if len(body) > maxToolResult {
		g.logger.WarnContext(ctx, "tool result exceeds limit", "tool", id, "bytes", len(body))
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Tool result exceeds 256 KiB; write large output to a file or artifact and return its path."}}, IsError: true}, nil
	}
	g.logger.InfoContext(ctx, "tool call", "tool", id, "durationMs", time.Since(start).Milliseconds(), "resultBytes", len(body), "toolError", result.IsError)
	return result, nil
}

func (g *Gateway) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	var errs []error
	for _, backend := range g.backends {
		errs = append(errs, backend.session.Close())
	}
	g.backends = nil
	g.entries = nil
	g.ready = false
	return errors.Join(errs...)
}

func (g *Gateway) ensureCatalog(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.ready {
		return nil
	}
	entries := make(map[string]*catalogEntry)
	backends := make([]*runningBackend, 0, len(g.config.Backends))
	closeBackends := func() {
		for _, backend := range backends {
			_ = backend.session.Close()
		}
	}
	for _, spec := range g.config.Backends {
		cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...)
		cmd.Stderr = os.Stderr
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
		for _, name := range spec.PassEnv {
			value, ok := os.LookupEnv(name)
			if !ok || value == "" {
				closeBackends()
				return fmt.Errorf("tool backend %q requires credential %s", spec.ID, name)
			}
			cmd.Env = append(cmd.Env, name+"="+value)
		}
		client := mcp.NewClient(&mcp.Implementation{Name: "turnyard-tool-gateway", Version: "1.0.0"}, &mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}})
		connectCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		session, err := client.Connect(connectCtx, &mcp.CommandTransport{Command: cmd}, nil)
		cancel()
		if err != nil {
			closeBackends()
			return fmt.Errorf("connect tool backend %q: %w", spec.ID, err)
		}
		backend := &runningBackend{session: session, config: spec}
		backends = append(backends, backend)
		listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		for tool, err := range session.Tools(listCtx, nil) {
			if err != nil {
				cancel()
				closeBackends()
				return fmt.Errorf("list backend %q: %w", spec.ID, err)
			}
			id := spec.ID + "/" + tool.Name
			if len(entries) >= maxCatalogTools {
				cancel()
				closeBackends()
				return fmt.Errorf("tool catalog exceeds %d tools", maxCatalogTools)
			}
			if tool.Name == "" || entries[id] != nil {
				cancel()
				closeBackends()
				return fmt.Errorf("backend %q has invalid or repeated tool name", spec.ID)
			}
			compiler := jsonschema.NewCompiler()
			compiler.UseLoader(denyExternalSchemaLoader{})
			if err := compiler.AddResource("schema.json", tool.InputSchema); err != nil {
				cancel()
				closeBackends()
				return fmt.Errorf("invalid schema for %q: %w", id, err)
			}
			schema, err := compiler.Compile("schema.json")
			if err != nil {
				cancel()
				closeBackends()
				return fmt.Errorf("compile schema for %q: %w", id, err)
			}
			entries[id] = &catalogEntry{candidate: candidate{ID: id, Description: tool.Description, InputSchema: tool.InputSchema}, nativeName: tool.Name, backend: backend, schema: schema}
		}
		cancel()
	}
	if err := g.checkCatalogLock(entries); err != nil {
		closeBackends()
		return err
	}
	g.backends = backends
	g.entries = entries
	g.ready = true
	g.logger.InfoContext(ctx, "tool catalog ready", "backends", len(backends), "tools", len(entries))
	return nil
}

type denyExternalSchemaLoader struct{}

func (denyExternalSchemaLoader) Load(uri string) (any, error) {
	return nil, fmt.Errorf("external tool schema reference %q is not allowed", uri)
}

func (g *Gateway) checkCatalogLock(entries map[string]*catalogEntry) error {
	if g.config.CatalogLock == "" {
		return nil
	}
	ids := make([]string, 0, len(entries))
	for id := range entries {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	items := make([]candidate, 0, len(ids))
	for _, id := range ids {
		items = append(items, entries[id].candidate)
	}
	body, err := json.Marshal(items)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	value := hex.EncodeToString(sum[:])
	if err := os.MkdirAll(filepath.Dir(g.config.CatalogLock), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(g.config.CatalogLock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		if _, writeErr := file.WriteString(value + "\n"); writeErr != nil {
			_ = file.Close()
			return writeErr
		}
		return file.Close()
	}
	if !os.IsExist(err) {
		return err
	}
	old, err := os.ReadFile(g.config.CatalogLock)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(old)) != value {
		return fmt.Errorf("tool catalog changed since this session was created")
	}
	return nil
}

func searchWords(query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	return strings.FieldsFunc(query, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
	})
}

func matchScore(item candidate, words []string) int {
	id := strings.ToLower(item.ID)
	description := strings.ToLower(item.Description)
	schema, _ := json.Marshal(item.InputSchema)
	fields := strings.ToLower(string(schema))
	score := 0
	for _, word := range words {
		switch {
		case strings.Contains(id, word):
			score += 6
		case strings.Contains(description, word):
			score += 3
		case strings.Contains(fields, word):
			score++
		default:
			return 0
		}
	}
	return score
}
