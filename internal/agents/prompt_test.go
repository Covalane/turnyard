package agents

import (
	"strings"
	"testing"

	"github.com/Covalane/turnyard/internal/contracts"
)

func TestAgentPromptKeepsOutputsAndConstraintsAcrossTurns(t *testing.T) {
	work := contracts.WorkSpec{Objective: "Build service", Acceptance: []string{"Go tests pass"}, Deliverables: []contracts.DeliverableSpec{{Repository: "auth", Path: "cmd/turnyard-auth/main.go", Kind: contracts.DeliverableFile}, {ID: "summary", Kind: contracts.DeliverableText}}}
	scope := []contracts.ScopeRepo{{ID: "auth", Mode: contracts.ScopeWrite}}
	for _, tc := range []struct{ reply, retry string }{{}, {retry: "DELIVERABLE_MISSING"}, {reply: "Use the selected option"}} {
		prompt := AgentPrompt(work, scope, tc.reply, tc.retry, "/workspace/.turnyard-output/artifacts.json")
		for _, required := range []string{"auth (write)", "Build service", "Go tests pass", "auth/cmd/turnyard-auth/main.go", "summary (text)", "/workspace/.turnyard-output/artifacts.json"} {
			if !strings.Contains(prompt, required) {
				t.Fatalf("prompt omitted %q: %s", required, prompt)
			}
		}
		if tc.retry != "" && !strings.Contains(prompt, tc.retry) {
			t.Fatalf("retry code omitted: %s", prompt)
		}
		if tc.reply != "" && !strings.Contains(prompt, tc.reply) {
			t.Fatalf("human reply omitted: %s", prompt)
		}
	}
}
