package sandbox

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Covalane/turnyard/internal/fault"
)

func TestStopTaskChecksKeepsContainerRemovalFailure(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("test uses a POSIX shell fake")
	}
	binary := filepath.Join(t.TempDir(), "fake-container")
	script := "#!/bin/sh\ncase \"$1\" in\n  ps) echo ty-check-work_test-1 ;;\n  rm) echo delete-denied >&2; exit 27 ;;\nesac\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	backend := NewOCIBackend(DockerDialect{Executable: binary})
	err := backend.StopTaskChecks(context.Background(), "work_test")
	if fault.CodeOf(err) != fault.CodeSandboxCleanupUnknown {
		t.Fatalf("cleanup code=%s, err=%v", fault.CodeOf(err), err)
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 27 {
		t.Fatalf("container delete exit status was lost: %v", err)
	}
	if got := strings.Join(fault.Operations(err), "/"); !strings.Contains(got, "remove container") || fault.Origin(err) == "" {
		t.Fatalf("cleanup diagnostics incomplete: %q, %v", got, err)
	}
	if sites := strings.Join(fault.Sites(err), "/"); !strings.Contains(sites, "internal/sandbox/oci_backend.go") {
		t.Fatalf("container removal source location missing: %s", sites)
	}
}

func TestStopInvocationRemovesToolGatewayResources(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("test uses a POSIX shell fake")
	}
	root := t.TempDir()
	t.Setenv("TURNYARD_FAKE_DOCKER_STATE", root)
	containers := "ty-inv_test\nty-model-ty-inv_test\nty-tool-gw-ty-inv_test\nty-tools-inv_test\nty-tool-gw-ty-tools-inv_test\nunrelated\n"
	networks := "ty-net-ty-inv_test\nty-tool-net-ty-tools-inv_test\nunrelated\n"
	if err := os.WriteFile(filepath.Join(root, "containers"), []byte(containers), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "networks"), []byte(networks), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "fake-docker")
	script := `#!/bin/sh
set -eu
state="$TURNYARD_FAKE_DOCKER_STATE"
remove() { awk -v name="$2" '$0 != name' "$state/$1" > "$state/next"; mv "$state/next" "$state/$1"; }
case "$1" in
  ps) cat "$state/containers" ;;
  rm) remove containers "$3" ;;
  inspect) echo "No such object: $2" >&2; exit 1 ;;
  network)
    case "$2" in
      ls) cat "$state/networks" ;;
      rm) remove networks "$3" ;;
      inspect) echo "No such network: $3" >&2; exit 1 ;;
    esac ;;
esac
`
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	backend := NewOCIBackend(DockerDialect{Executable: binary})
	if err := backend.StopInvocation(context.Background(), "inv_test"); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"containers", "networks"} {
		body, err := os.ReadFile(filepath.Join(root, file))
		if err != nil || string(body) != "unrelated\n" {
			t.Fatalf("%s cleanup left resources: %q %v", file, body, err)
		}
	}
}
