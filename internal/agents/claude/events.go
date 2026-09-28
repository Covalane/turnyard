package claude

import (
	"encoding/json"
	"strings"

	"github.com/Covalane/turnyard/internal/agents"
	"github.com/Covalane/turnyard/internal/fault"
)

type claudeEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	IsError   bool   `json:"is_error"`
	Message   struct {
		Model   string `json:"model"`
		Content []struct {
			Type  string `json:"type"`
			Name  string `json:"name"`
			Input struct {
				Skill string `json:"skill"`
			} `json:"input"`
		} `json:"content"`
	} `json:"message"`
}

func parseClaudeEvents(raw string, expected string) (agents.AgentResult, error) {
	result := agents.AgentResult{}
	models := map[string]bool{}
	ids := map[string]bool{}
	loaded := map[string]bool{}
	completed := false
	for _, line := range strings.Split(raw, "\n") {
		var event claudeEvent
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		result.Events = append(result.Events, json.RawMessage(line))
		if event.SessionID != "" {
			ids[event.SessionID] = true
		}
		switch event.Type {
		case "assistant":
			if event.Message.Model != "" {
				models[event.Message.Model] = true
			}
			for _, part := range event.Message.Content {
				switch part.Type {
				case "tool_use":
					if part.Name == "" {
						continue
					}
					result.CalledTools = append(result.CalledTools, part.Name)
					if part.Name == "Skill" && part.Input.Skill != "" {
						loaded[part.Input.Skill] = true
					}
				}
			}
		case "result":
			completed = true
			if event.IsError {
				return result, fault.New(fault.CodeAgentFailed, "Claude result reported an error")
			}
		}
	}
	if !completed || len(ids) != 1 {
		return result, fault.New(fault.CodeNativeSessionMissing, "Claude did not report one completed native session")
	}
	for id := range ids {
		result.NativeID = id
	}
	if len(models) == 0 {
		return result, fault.New(fault.CodeModelEvidenceMissing, "Claude emitted no assistant model metadata")
	}
	for model := range models {
		if model != expected {
			return result, fault.New(fault.CodeModelMismatch, "Claude used model %s instead of %s", model, expected)
		}
	}
	result.Output = raw
	result.LoadedSkills = agents.SortedKeys(loaded)
	result.ActualModel = expected
	return result, nil
}
