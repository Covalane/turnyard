package codex

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Covalane/turnyard/internal/agents"
	"github.com/Covalane/turnyard/internal/collections"
	"github.com/Covalane/turnyard/internal/fault"
)

var codexSkillPath = regexp.MustCompile(`/state/home/\.agents/skills/([a-z0-9-]+)/SKILL\.md`)

type codexEvent struct {
	Type     string `json:"type"`
	ThreadID string `json:"thread_id"`
	Item     struct {
		Type    string `json:"type"`
		Status  string `json:"status"`
		Command string `json:"command"`
		Server  string `json:"server"`
		Tool    string `json:"tool"`
	} `json:"item"`
}

func parseCodexEvents(raw string) (agents.AgentResult, error) {
	result := agents.AgentResult{}
	ids := collections.Set[string]{}
	loadedSkills := collections.Set[string]{}
	calledTools := collections.Set[string]{}
	completed := false
	for _, line := range strings.Split(raw, "\n") {
		var event codexEvent
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		result.Events = append(result.Events, json.RawMessage(line))
		switch event.Type {
		case "thread.started":
			if event.ThreadID != "" {
				ids.Add(event.ThreadID)
			}
		case "turn.completed":
			completed = true
		case "turn.failed":
			return result, fault.New(fault.CodeAgentFailed, "Codex turn failed")
		}
		if event.Type != "item.completed" || event.Item.Status != "completed" {
			continue
		}
		switch event.Item.Type {
		case "command_execution":
			for _, match := range codexSkillPath.FindAllStringSubmatch(event.Item.Command, -1) {
				loadedSkills.Add(match[1])
			}
		case "mcp_tool_call":
			if event.Item.Server != "" && event.Item.Tool != "" {
				calledTools.Add("mcp__" + event.Item.Server + "__" + event.Item.Tool)
			}
		}
	}
	if !completed || len(ids) != 1 {
		return result, fault.New(fault.CodeNativeSessionMissing, "Codex did not complete one native thread")
	}
	for id := range ids {
		result.NativeID = id
	}
	result.Output = raw
	result.LoadedSkills = agents.SortedKeys(loadedSkills)
	result.CalledTools = agents.SortedKeys(calledTools)
	return result, nil
}

func verifyCodexModel(state, native, expectedProvider, expectedModel string) error {
	paths, err := filepath.Glob(filepath.Join(state, "codex", "sessions", "*", "*", "*", "rollout-*-"+native+".jsonl"))
	if err != nil || len(paths) != 1 {
		return fault.New(fault.CodeModelEvidenceMissing, "Codex native rollout is unavailable")
	}
	body, err := agents.ReadStateFile(state, paths[0])
	if err != nil {
		return err
	}
	found := false
	providerFound := false
	for _, line := range strings.Split(string(body), "\n") {
		var entry struct {
			Type    string `json:"type"`
			Payload struct {
				ID            string `json:"id"`
				ModelProvider string `json:"model_provider"`
				Model         string `json:"model"`
			} `json:"payload"`
		}
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		switch entry.Type {
		case "session_meta":
			if entry.Payload.ID != native || entry.Payload.ModelProvider != expectedProvider {
				return fault.New(fault.CodeModelMismatch, "Codex native session provider differs from pinned binding")
			}
			providerFound = true
		case "turn_context":
			if entry.Payload.Model == "" {
				continue
			}
			found = true
			if entry.Payload.Model != expectedModel {
				return fault.New(fault.CodeModelMismatch, "Codex native turn used model %s", entry.Payload.Model)
			}
		}
	}
	if !found || !providerFound {
		return fault.New(fault.CodeModelEvidenceMissing, "Codex native rollout has no model metadata")
	}
	return nil
}
