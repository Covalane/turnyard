package opencode

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/Covalane/turnyard/internal/agents"
	"github.com/Covalane/turnyard/internal/fault"
)

type openCodeEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionID"`
	Part      struct {
		Type  string `json:"type"`
		Tool  string `json:"tool"`
		State struct {
			Status string `json:"status"`
			Input  struct {
				Name string `json:"name"`
			} `json:"input"`
		} `json:"state"`
	} `json:"part"`
}

func parseOpenCodeEvents(output string) (agents.AgentResult, error) {
	result := agents.AgentResult{}
	ids := map[string]bool{}
	called := map[string]bool{}
	loaded := map[string]bool{}
	for _, line := range strings.Split(output, "\n") {
		var event openCodeEvent
		if json.Unmarshal([]byte(line), &event) != nil || event.Type == "" {
			continue
		}
		result.Events = append(result.Events, json.RawMessage(line))
		if event.SessionID != "" {
			ids[event.SessionID] = true
		}
		if event.Type != "tool_use" || event.Part.State.Status != "completed" || event.Part.Tool == "" {
			continue
		}
		called[event.Part.Tool] = true
		if event.Part.Tool == "skill" && event.Part.State.Input.Name != "" {
			loaded[event.Part.State.Input.Name] = true
		}
	}
	if len(ids) > 1 {
		return result, fault.New(fault.CodeNativeSessionMismatch, "agent emitted multiple native session IDs")
	}
	for id := range ids {
		result.NativeID = id
	}
	result.CalledTools = agents.SortedKeys(called)
	result.LoadedSkills = agents.SortedKeys(loaded)
	return result, nil
}

func verifyOpenCodeModel(ctx context.Context, state, native, expectedProvider, expectedModel string) (string, string, error) {
	path := filepath.Join(state, "data", "opencode", "opencode.db")
	if err := agents.CheckStateFile(state, path); err != nil {
		return "", "", err
	}
	if _, err := os.Stat(path); err != nil {
		return "", "", fault.New(fault.CodeModelEvidenceMissing, "OpenCode native message database missing")
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return "", "", err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, "SELECT data FROM message WHERE session_id=? ORDER BY time_created", native)
	if err != nil {
		return "", "", fault.Wrap(fault.CodeModelEvidenceMissing, "query OpenCode native messages", err, "cannot read native messages")
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return "", "", err
		}
		var msg struct {
			Role       string `json:"role"`
			ProviderID string `json:"providerID"`
			ModelID    string `json:"modelID"`
		}
		if json.Unmarshal([]byte(data), &msg) != nil || msg.Role != "assistant" {
			continue
		}
		found = true
		if msg.ProviderID != expectedProvider || msg.ModelID != expectedModel {
			return "", "", fault.New(fault.CodeModelMismatch, "native assistant model differs from pinned binding")
		}
	}
	if err := rows.Err(); err != nil {
		return "", "", err
	}
	if !found {
		return "", "", fault.New(fault.CodeModelEvidenceMissing, "no assistant messages in native session")
	}
	return expectedProvider, expectedModel, nil
}
