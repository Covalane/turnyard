package agents

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/Covalane/turnyard/internal/contracts"
)

func TestStateSymlinkAndInstalledBundleMutation(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	outside := filepath.Join(root, "outside.toml")
	if err := os.MkdirAll(filepath.Join(state, "codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(state, "codex", "config.toml")); err != nil {
		t.Fatal(err)
	}
	if err := WritePinnedConfiguration(state, filepath.Join(state, "codex", "config.toml"), []byte("secret"), "Codex"); contracts.ErrorCode(err) != "NATIVE_STATE_UNSAFE" {
		t.Fatalf("symlink accepted: %v", err)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("wrote outside state: %v", err)
	}
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "server.go"), []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, err := contracts.BundleDigest(source)
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(state, "tools", "witness")
	if err := InstallPinnedBundle(state, source, digest, dest, "tool witness"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "server.go"), []byte("mutated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := InstallPinnedBundle(state, source, digest, dest, "tool witness"); contracts.ErrorCode(err) != "ENVIRONMENT_DRIFT" {
		t.Fatalf("modified installed bundle accepted: %v", err)
	}
}

func TestInstalledBundleRejectsFIFOWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	state, source := filepath.Join(root, "state"), filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "server.go"), []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, err := contracts.BundleDigest(source)
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(state, "tools", "witness")
	if err := InstallPinnedBundle(state, source, digest, dest, "tool witness"); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dest, "server.go")
	if err := os.Remove(fifo); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- InstallPinnedBundle(state, source, digest, dest, "tool witness") }()
	select {
	case err := <-done:
		if contracts.ErrorCode(err) != "NATIVE_STATE_UNSAFE" {
			t.Fatalf("FIFO accepted: %v", err)
		}
	case <-time.After(time.Second):
		go func() {
			writer, err := os.OpenFile(fifo, os.O_WRONLY, 0)
			if err == nil {
				_ = writer.Close()
			}
		}()
		t.Fatal("bundle validation blocked on FIFO")
	}
}
