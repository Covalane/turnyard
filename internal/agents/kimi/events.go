package kimi

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/Covalane/turnyard/internal/agents"
	"github.com/Covalane/turnyard/internal/fault"
)

type kimiEvent struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	ToolCalls []struct {
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls"`
}

func kimiSessions(output string) ([]string, error) {
	var sessions []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(output), &sessions); err != nil {
		return nil, fault.Wrap(fault.CodeNativeStateUnavailable, "parse Kimi inventory", err, "Kimi session inventory is not JSON")
	}
	ids := make([]string, 0, len(sessions))
	for _, session := range sessions {
		if session.ID != "" {
			ids = append(ids, session.ID)
		}
	}
	return ids, nil
}

func verifyKimiModel(state, native, expected string) error {
	paths, err := filepath.Glob(filepath.Join(state, "kimi-code", "sessions", "*", native, "agents", "main", "wire.jsonl"))
	if err != nil || len(paths) != 1 {
		return fault.New(fault.CodeModelEvidenceMissing, "Kimi native session log is unavailable")
	}
	body, err := agents.ReadStateFile(state, paths[0])
	if err != nil {
		return err
	}
	found := false
	for _, line := range strings.Split(string(body), "\n") {
		var entry struct {
			Type       string `json:"type"`
			Model      string `json:"model"`
			Provider   string `json:"provider"`
			ModelAlias string `json:"modelAlias"`
		}
		if json.Unmarshal([]byte(line), &entry) != nil || entry.Type != "llm.request" {
			continue
		}
		found = true
		if entry.Model != expected || entry.Provider != kimiProviderType || entry.ModelAlias != kimiModelAlias {
			return fault.New(fault.CodeModelMismatch, "Kimi native request differs from pinned model binding")
		}
	}
	if !found {
		return fault.New(fault.CodeModelEvidenceMissing, "Kimi native session has no model request")
	}
	return nil
}

func parseKimiEvents(raw string) ([]json.RawMessage, []string, []string, []string) {
	events := []json.RawMessage{}
	texts := []string{}
	called := map[string]bool{}
	loaded := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		var event kimiEvent
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		events = append(events, json.RawMessage(line))
		if event.Role != "assistant" {
			continue
		}
		if event.Content != "" {
			texts = append(texts, event.Content)
		}
		for _, call := range event.ToolCalls {
			name := call.Function.Name
			if name == "" {
				continue
			}
			called[name] = true
			if name != "Skill" {
				continue
			}
			var args struct {
				Skill string `json:"skill"`
			}
			if json.Unmarshal([]byte(call.Function.Arguments), &args) == nil && args.Skill != "" {
				loaded[args.Skill] = true
			}
		}
	}
	return events, texts, agents.SortedKeys(called), agents.SortedKeys(loaded)
}
