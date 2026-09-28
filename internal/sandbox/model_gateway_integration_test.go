//go:build integration

package sandbox

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Covalane/turnyard/internal/contracts"
)

func TestModelOnlyDockerBoundary(t *testing.T) {
	if os.Getenv("TURNYARD_NETWORK_E2E") != "1" {
		t.Skip("set TURNYARD_NETWORK_E2E=1 for Docker network isolation")
	}
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })}
	go server.Serve(listener)
	defer server.Shutdown(context.Background())
	defer listener.Close()
	root := t.TempDir()
	workspace, state := filepath.Join(root, "workspace"), filepath.Join(root, "state")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	image := os.Getenv("TURNYARD_E2E_IMAGE")
	if image == "" {
		image = "turnyard-agent:dev"
	}
	// The internal agent can use the model gateway but cannot route around it,
	// reach the host, or read the upstream credential.
	script := `const fail = m => { console.error(m); process.exit(2) };
if(process.env.TEST_MODEL_KEY !== 'turnyard-model-placeholder') fail('real credential exposed');
const health = await fetch('http://turnyard-model:8080/_turnyard_health');
if(!health.ok) fail('gateway unavailable');
const smuggle = await fetch('http://turnyard-model:8080//api.github.com/');
if(smuggle.status !== 400) fail('gateway accepted absolute-path smuggling');
for(const url of ['https://api.github.com', 'http://host.docker.internal:` + strconv.Itoa(listener.Addr().(*net.TCPAddr).Port) + `']) {
  try { const result = await fetch(url, {signal: AbortSignal.timeout(3000)}); fail('direct egress succeeded: '+url+' '+result.status) }
  catch(error) { if(error.message.startsWith('direct egress succeeded')) throw error }
}

console.log('MODEL_ONLY_BOUNDARY_OK');`
	backend := NewOCIBackend(DockerDialect{Executable: "docker"})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	result, err := backend.Run(ctx, SandboxRun{Name: "ty-network-test", Sandbox: contracts.SandboxSpec{
		Backend: BackendDocker, Image: image, Network: contracts.SandboxNetworkModelOnly, Isolation: contracts.SandboxIsolationProfile(os.Getenv("TURNYARD_E2E_ISOLATION"))},
		Workspace: workspace, State: state, EntryPoint: "node", Command: []string{"--input-type=module", "-e", script},
		Credentials:  map[string]string{"TEST_MODEL_KEY": ModelCredentialPlaceholder},
		ModelGateway: &ModelGateway{Endpoint: "https://api.deepseek.com/", Credential: "never-expose-this-test-secret"},
		Timeout:      30 * time.Second, LogPath: filepath.Join(root, "network.log")})
	if err != nil || result.ExitCode != 0 || !strings.Contains(result.Output, "MODEL_ONLY_BOUNDARY_OK") {
		t.Fatalf("model-only boundary: result=%+v err=%v", result, err)
	}
}

func TestModelGatewayCannotBeUsedFromBridge(t *testing.T) {
	if os.Getenv("TURNYARD_NETWORK_E2E") != "1" {
		t.Skip("set TURNYARD_NETWORK_E2E=1 for Docker network isolation")
	}
	image := os.Getenv("TURNYARD_E2E_IMAGE")
	if image == "" {
		image = "turnyard-agent:dev"
	}
	backend := NewOCIBackend(DockerDialect{Executable: "docker"})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	lease, err := backend.startModelGateway(ctx, SandboxRun{Name: "ty-bridge-probe",
		Sandbox:      contracts.SandboxSpec{Backend: BackendDocker, Image: image},
		ModelGateway: &ModelGateway{Endpoint: "https://api.deepseek.com/", Credential: "private-test-key"}})
	if err != nil {
		t.Fatal(err)
	}
	defer lease.close(context.Background())
	address, err := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{(index .NetworkSettings.Networks \"bridge\").IPAddress}}", lease.name).Output()
	if err != nil || strings.TrimSpace(string(address)) == "" {
		t.Fatalf("inspect bridge address: %v %s", err, address)
	}
	url := "http://" + strings.TrimSpace(string(address)) + ":8080/_turnyard_health"
	probe := `fetch(process.argv[1], {signal: AbortSignal.timeout(2000)}).then(() => process.exit(2), () => process.exit(0))`
	call := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "bridge", "--entrypoint", "node", image,
		"-e", probe, url)
	if output, err := call.CombinedOutput(); err != nil {
		t.Fatalf("public bridge reached private gateway or probe failed: %v %s", err, output)
	}
}
