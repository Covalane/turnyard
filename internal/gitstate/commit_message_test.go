package gitstate

import (
	"strings"
	"testing"

	"github.com/Covalane/turnyard/internal/contracts"
)

func TestTaskCommitMessageIsReadableAndTraceable(t *testing.T) {
	taskID := "work_example"
	work := contracts.WorkSpec{Objective: "增加健康检查，并补充测试。其他实现细节。", CommitMessage: "feat: add health check"}
	if got := taskCommitMessage(work, taskID, 1); got != "feat: add health check\n\nTurnyard-Task: work_example\n" {
		t.Fatalf("explicit message: %q", got)
	}
	if got := taskCommitMessage(work, taskID, 2); got != "feat: add health check (revision 2)\n\nTurnyard-Task: work_example\n" {
		t.Fatalf("retry message: %q", got)
	}
	work.CommitMessage = ""
	got := taskCommitMessage(work, taskID, 1)
	if !strings.HasPrefix(got, "增加健康检查") || strings.HasPrefix(got, "turnyard:") {
		t.Fatalf("legacy task has an opaque commit title: %q", got)
	}
}
