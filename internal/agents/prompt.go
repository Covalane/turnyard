package agents

import (
	"fmt"
	"strings"

	"github.com/Covalane/turnyard/internal/contracts"
)

// AgentPrompt names the invocation-scoped file used for text and
// external output claims. Repository files are verified from Git instead.
func AgentPrompt(work contracts.WorkSpec, scope []contracts.ScopeRepo, reply, retryCode, claimPath string) string {
	allowed := []string{}
	for _, r := range scope {
		allowed = append(allowed, fmt.Sprintf("%s (%s)", r.ID, r.Mode))
	}
	parts := []string{
		"You are working inside Turnyard. Repositories are separate directories under /workspace.",
		"Do not run git commit, switch branches, push, or read credentials. Only create PRs through an explicitly authorized tool when the task requires one; never use a direct shell or network call to create a PR.",
		"Turnyard will inspect and commit repository changes. Write only in authorized repositories and the declared output directory.",
		"Only if a human decision is required, put TURNYARD_NEEDS_INPUT: followed by a concise question on the final line of your response. Never mention this marker otherwise.",
		"Repository permissions: " + strings.Join(allowed, ", ") + ".",
		"Objective: " + work.Objective + ".",
		"Acceptance: " + strings.Join(work.Acceptance, "; ") + ".",
	}
	if len(work.Inputs) > 0 {
		inputs := make([]string, 0, len(work.Inputs))
		for _, item := range work.Inputs {
			inputs = append(inputs, item.ID+"=/workspace/.turnyard-input/"+item.Source.Path+" (SHA-256 "+item.ExpectedSHA256+", "+item.MediaType+")")
		}
		parts = append(parts, "Read-only task inputs: "+strings.Join(inputs, "; ")+".")
	}
	if len(work.Deliverables) > 0 {
		files := make([]string, 0, len(work.Deliverables))
		localFiles := make([]string, 0, len(work.Deliverables))
		claims := make([]string, 0, len(work.Deliverables))
		for _, item := range work.Deliverables {
			switch item.Kind {
			case contracts.DeliverableText:
				claims = append(claims, item.ID+" (text)")
			case contracts.DeliverablePullRequest:
				claims = append(claims, item.ID+" (pull request URL)")
			default:
				if item.Repository == "" {
					localFiles = append(localFiles, "/workspace/.turnyard-output/files/"+item.Path+" ("+string(item.Kind)+")")
				} else {
					files = append(files, item.Repository+"/"+item.Path+" ("+string(item.Kind)+")")
				}
			}
		}
		if len(files) > 0 {
			parts = append(parts, "Required files in the final Git commit: "+strings.Join(files, ", ")+".")
		}
		if len(localFiles) > 0 {
			parts = append(parts, "Required files outside Git: "+strings.Join(localFiles, ", ")+". Turnyard will verify and deliver them to their configured destinations after checks pass.")
		}
		if len(claims) > 0 && claimPath != "" {
			parts = append(parts, "Required outputs: "+strings.Join(claims, ", ")+". Write JSON to "+claimPath+" with schema_version turnyard.artifact-claims/v1 and artifacts array. Each item has id and either text or url. Create the parent directory first. This file is a claim; Turnyard will independently verify the output.")
		}
	}
	if retryCode != "" {
		parts = append(parts, "The previous attempt failed Turnyard validation with code "+retryCode+". Recheck the required files and acceptance conditions before finishing.")
	}
	if reply != "" {
		parts = append(parts, "Continue the same task and native session. Human reply: "+reply+".")
	}
	return strings.Join(parts, " ")
}
