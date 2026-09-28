package gitstate

import (
	"fmt"
	"strings"

	"github.com/Covalane/turnyard/internal/contracts"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

const maxDerivedTitleRunes = 72

// taskCommitMessage keeps the task ID in a trailer, leaving the subject useful
// in Git history. The objective fallback preserves older Work inputs that did
// not have commitMessage; new callers should supply an intentional title.
func taskCommitMessage(work contracts.WorkSpec, taskID string, revision int) string {
	title := work.CommitMessage
	if title == "" {
		title = derivedCommitTitle(work.Objective)
	}
	if revision > 1 {
		title += fmt.Sprintf(" (revision %d)", revision)
	}
	return title + "\n\nTurnyard-Task: " + taskID + "\n"
}

// taskRevision counts commits made since this task's pinned base. Git history
// must remain linear and anchored at that base; the agent cannot edit .git.
func taskRevision(repo *git.Repository, base, head string) (int, error) {
	revision := 1
	for head != base {
		if revision > 1000 {
			return 0, fmt.Errorf("task history exceeds 1000 commits")
		}
		commit, err := repo.CommitObject(plumbing.NewHash(head))
		if err != nil {
			return 0, err
		}
		if len(commit.ParentHashes) != 1 {
			return 0, fmt.Errorf("task history is not linear")
		}
		head = commit.ParentHashes[0].String()
		revision++
	}
	return revision, nil
}

func derivedCommitTitle(objective string) string {
	first := strings.TrimSpace(objective)
	if at := strings.IndexAny(first, "。！？；;\n\r"); at >= 0 {
		first = first[:at]
	}
	// A long first sentence often starts with a short useful clause. Prefer
	// that clause when it is long enough to stand as a commit subject.
	if at := strings.IndexAny(first, "，,"); at >= 0 && len([]rune(first[:at])) >= 12 {
		first = first[:at]
	}
	first = strings.Join(strings.Fields(first), " ")
	runes := []rune(first)
	if len(runes) > maxDerivedTitleRunes {
		first = strings.TrimSpace(string(runes[:maxDerivedTitleRunes-1])) + "…"
	}
	return strings.TrimSpace(first)
}
