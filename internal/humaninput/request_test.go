package humaninput

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Covalane/turnyard/internal/fault"
)

func TestRequestIsExplicitAndInvocationScoped(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	if got, err := Read(first); err != nil || got != "" {
		t.Fatalf("missing request: %q %v", got, err)
	}
	question := "无法确定目标分支，请选择 main 还是 release？"
	if err := Write(first, question); err != nil {
		t.Fatal(err)
	}
	if err := Write(first, question); err != nil {
		t.Fatalf("same request must be idempotent: %v", err)
	}
	if got, err := Read(first); err != nil || got != question {
		t.Fatalf("recorded request: %q %v", got, err)
	}
	if got, err := Read(second); err != nil || got != "" {
		t.Fatalf("request escaped invocation: %q %v", got, err)
	}
	if err := Write(first, "Choose another branch"); fault.CodeOf(err) != fault.CodeAgentOutputInvalid {
		t.Fatalf("different second question accepted: %v", err)
	}
	for _, invalid := range []string{"", " no ", strings.Repeat("x", maxQuestionBytes+1)} {
		if err := Write(second, invalid); fault.CodeOf(err) != fault.CodeAgentOutputInvalid {
			t.Fatalf("invalid question %q accepted: %v", invalid, err)
		}
	}
}

func TestRequestRejectsUntrustedFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	for _, body := range []string{
		`{"schema_version":"turnyard.human-input/v1","question":""}`,
		`{"schema_version":"turnyard.human-input/v1","question":"yes","extra":true}`,
		`{"schema_version":"turnyard.human-input/v1","question":"yes"} {}`,
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Read(dir); fault.CodeOf(err) != fault.CodeAgentOutputInvalid {
			t.Fatalf("invalid request accepted: %s: %v", body, err)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere"), path); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(dir); fault.CodeOf(err) != fault.CodeAgentOutputInvalid {
		t.Fatalf("symlink request accepted: %v", err)
	}
	if err := Write(dir, "question?"); fault.CodeOf(err) != fault.CodeAgentOutputInvalid {
		t.Fatalf("symlink request overwritten: %v", err)
	}
}
