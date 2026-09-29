package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/sandbox/applecontainer"
	"github.com/Covalane/turnyard/internal/sandbox/docker"
	"github.com/Covalane/turnyard/internal/sandbox/podman"
)

func TestContainerIdentityFollowsPrivateMountOwner(t *testing.T) {
	uid, gid := os.Getuid(), os.Getgid()
	if got, want := (docker.Dialect{}).UserOptions(uid, gid), []string{"--user", intPair(uid, gid)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Docker identity %v; want %v", got, want)
	}
	wantPodman := []string{"--userns=keep-id", "--user", intPair(uid, gid)}
	if uid == 0 {
		wantPodman = []string{"--user", intPair(uid, gid)}
	}
	if got := (podman.Dialect{}).UserOptions(uid, gid); !reflect.DeepEqual(got, wantPodman) {
		t.Fatalf("Podman identity %v; want %v", got, wantPodman)
	}
	if got, want := (podman.Dialect{}).UserOptions(0, 0), []string{"--user", "0:0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rootful Podman identity %v; want %v", got, want)
	}
	if got := (applecontainer.Dialect{}).UserOptions(uid, gid); len(got) != 0 {
		t.Fatalf("Apple container identity unexpectedly changed: %v", got)
	}
	root := t.TempDir()
	backend := NewOCIBackend(docker.Dialect{})
	cmd, _, _, err := backend.buildCommand(SandboxRun{
		Name: "identity-probe", Sandbox: contracts.SandboxSpec{Image: "node:test"},
		State: filepath.Join(root, "state"), LogPath: filepath.Join(root, "logs", "run.log"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSequence(cmd.Args, "--user", intPair(uid, gid)) {
		t.Fatalf("Docker command did not pin host identity: %v", cmd.Args)
	}
}

func intPair(uid, gid int) string { return fmt.Sprintf("%d:%d", uid, gid) }

func containsSequence(values []string, first, second string) bool {
	for i := 0; i+1 < len(values); i++ {
		if values[i] == first && values[i+1] == second {
			return true
		}
	}
	return false
}
