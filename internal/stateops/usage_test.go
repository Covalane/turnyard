package stateops

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestUsageExcludesLinkedFilesAndDirectories(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "state"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "state", "owned"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret"), make([]byte, 1024), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	bytes, err := Usage(context.Background(), root)
	if err != nil || bytes != 5 {
		t.Fatalf("state usage followed external link: bytes=%d err=%v", bytes, err)
	}
}
