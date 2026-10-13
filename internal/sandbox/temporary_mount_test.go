package sandbox

import (
	"reflect"
	"testing"

	"github.com/Covalane/turnyard/internal/sandbox/applecontainer"
	"github.com/Covalane/turnyard/internal/sandbox/docker"
	"github.com/Covalane/turnyard/internal/sandbox/podman"
)

func TestTemporaryMountsPermitBoundedCompilerScratch(t *testing.T) {
	for _, dialect := range []Dialect{docker.Dialect{}, podman.Dialect{}} {
		for _, tc := range []struct {
			memory int
			mount  string
		}{
			{256, "/tmp:rw,exec,nosuid,nodev,size=128m,mode=1777"},
			{2048, "/tmp:rw,exec,nosuid,nodev,size=1024m,mode=1777"},
			{4096, "/tmp:rw,exec,nosuid,nodev,size=1024m,mode=1777"},
		} {
			if got, want := dialect.TemporaryMountOptions(tc.memory), []string{"--tmpfs", tc.mount}; !reflect.DeepEqual(got, want) {
				t.Fatalf("%s memory=%d: %v; want %v", dialect.Name(), tc.memory, got, want)
			}
		}
	}
	if got, want := (applecontainer.Dialect{}).TemporaryMountOptions(2048), []string{"--tmpfs", "/tmp"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Apple-specific mount changed: %v; want %v", got, want)
	}
}
