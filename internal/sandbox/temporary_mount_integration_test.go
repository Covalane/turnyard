package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/sandbox/docker"
)

// This compiles and executes a real test inside the sandbox. Host-built probe
// binaries alone do not exercise the compiler's temporary execution directory.
func TestReadonlyGoBuildAndTest(t *testing.T) {
	if os.Getenv("TURNYARD_INTEGRATION_BACKEND") != BackendDocker {
		t.Skip("set TURNYARD_INTEGRATION_BACKEND=docker for real compiler scratch validation")
	}
	image := os.Getenv("TURNYARD_INTEGRATION_IMAGE")
	if image == "" {
		image = "turnyard-agent:dev"
	}
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	app := filepath.Join(workspace, "app")
	if err := os.MkdirAll(app, 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod": "module example.invalid/scratch-probe\n\ngo 1.27.2\n",
		"scratch_test.go": `package probe
import ("os"; "testing")
func TestPrivateScratch(t *testing.T) {
 f, err := os.CreateTemp("", "scratch-probe-")
 if err != nil { t.Fatal(err) }
 name := f.Name()
 if err := f.Close(); err != nil { t.Fatal(err) }
 defer os.Remove(name)
 if err := os.WriteFile("/workspace/app/unauthorized", []byte("changed"), 0600); err == nil {
  t.Fatal("product repository was writable")
 }
 t.Log("temporary test binary executed")
}
`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(app, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	backend := NewOCIBackend(docker.Dialect{})
	result, err := backend.Run(context.Background(), SandboxRun{
		Name: "ty-scratch-probe", Sandbox: contracts.SandboxSpec{Backend: BackendDocker, Image: image, CPUs: 2, MemoryMB: 2048, Network: contracts.SandboxNetworkNone},
		Workspace: workspace, Scope: []contracts.ScopeRepo{{ID: "app", Mode: contracts.ScopeRead}},
		State: filepath.Join(root, "state"), EntryPoint: "go", Command: []string{"-C", "/workspace/app", "test", "-count=1", "-v", "./..."},
		Environment: map[string]string{"CGO_ENABLED": "0"}, Timeout: 180 * time.Second, LogPath: filepath.Join(root, "logs", "test.log"),
	})
	if err != nil || result.ExitCode != 0 || result.TimedOut || !strings.Contains(result.Output, "temporary test binary executed") {
		t.Fatalf("real readonly Go test failed: %+v, %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(app, "unauthorized")); !os.IsNotExist(err) {
		t.Fatalf("readonly product changed: %v", err)
	}
}
