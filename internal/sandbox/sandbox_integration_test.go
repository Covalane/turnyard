package sandbox

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Covalane/turnyard/internal/contracts"
)

func TestRealSandboxFaults(t *testing.T) {
	name := os.Getenv("TURNYARD_INTEGRATION_BACKEND")
	if name == "" {
		t.Skip("set TURNYARD_INTEGRATION_BACKEND to run OCI boundary probes")
	}
	b, err := Backend(name)
	if err != nil {
		t.Fatal(err)
	}
	probe, err := b.Probe(context.Background())
	if err != nil || probe["available"] != true {
		t.Fatalf("backend unavailable: %v %v", probe, err)
	}
	root := t.TempDir()
	t.Cleanup(func() {
		// Docker Desktop may release nested bind mounts after the container disappears.
		for i := 0; i < 20; i++ {
			if err := os.RemoveAll(root); err == nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
	workspace := filepath.Join(root, "workspace")
	state := filepath.Join(root, "state")
	logs := filepath.Join(root, "logs")
	for _, dir := range []string{filepath.Join(workspace, "ro"), filepath.Join(workspace, "rw")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(workspace, "rw", ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "ro", "sentinel"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	image := os.Getenv("TURNYARD_INTEGRATION_IMAGE")
	if image == "" {
		image = "turnyard-agent:dev"
	}
	spec := SandboxSpec{Backend: name, Image: image, CPUs: 1, MemoryMB: 512, Isolation: contracts.SandboxIsolationProfile(os.Getenv("TURNYARD_INTEGRATION_ISOLATION"))}
	if err := b.ValidateSpec(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	scope := []ScopeRepo{{ID: "ro", Mode: "read"}, {ID: "rw", Mode: "write"}}
	// The probe runs in the same agent image and exercises the real mounts.
	probeCode := `package main
import ("encoding/json";"fmt";"net";"os";"time")
func attempt(path string) string { if os.WriteFile(path, []byte("changed"), 0600)==nil { return "wrote" }; return "blocked" }
func main() { switch os.Args[1] {
case "boundary":
 _,homeErr:=os.Stat("/Users")
 result:=map[string]any{"ro":attempt("/workspace/ro/sentinel"),"rw":attempt("/workspace/rw/result"),"git":attempt("/workspace/rw/.git/probe"),"root":attempt("/root-probe"),"host_home_visible":homeErr==nil}
 output,_:=json.Marshal(result);fmt.Println(string(output))
case "redaction":fmt.Print(os.Getenv("OLLAMA_API_KEY"))
case "timeout":time.Sleep(10*time.Second)
case "network":
 conn,err:=net.DialTimeout("tcp","1.1.1.1:53",2*time.Second)
 if err!=nil { fmt.Println("blocked") } else { _=conn.Close();fmt.Println("connected") }
} }`
	probeSource := filepath.Join(root, "probe.go")
	if err := os.WriteFile(probeSource, []byte(probeCode), 0o600); err != nil {
		t.Fatal(err)
	}
	probeBinary := filepath.Join(workspace, "rw", "probe")
	build := exec.Command("go", "build", "-o", probeBinary, probeSource)
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build Linux probe: %v: %s", err, output)
	}
	var timeoutName string
	run := func(label string, override SandboxSpec, command []string, credential string, timeout time.Duration) (SandboxResult, error) {
		name := "ty-go-" + label + "-" + contracts.NewID("x")
		if label == "timeout" {
			timeoutName = name
		}
		return b.Run(context.Background(), SandboxRun{Name: name, Sandbox: override, Workspace: workspace,
			Scope: scope, State: state, EntryPoint: "/workspace/rw/probe", Command: command, Credentials: map[string]string{"OLLAMA_API_KEY": credential},
			Timeout: timeout, LogPath: filepath.Join(logs, label+".txt")})
	}
	first, err := run("boundary", spec, []string{"boundary"}, "", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var boundary map[string]any
	if err := json.Unmarshal([]byte(first.Output), &boundary); err != nil {
		logData, _ := os.ReadFile(filepath.Join(logs, "boundary.txt"))
		t.Fatalf("boundary output invalid: %v (exit=%d output=%q log=%q)", err, first.ExitCode, first.Output, logData)
	}
	if first.ExitCode != 0 || boundary["ro"] != "blocked" || boundary["rw"] != "wrote" ||
		boundary["git"] != "blocked" || boundary["root"] != "blocked" || boundary["host_home_visible"] != false {
		t.Fatalf("boundary: %v", boundary)
	}
	secret := "turnyard-go-test-secret"
	safe, err := run("redaction", spec, []string{"redaction"}, secret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	log, err := os.ReadFile(filepath.Join(logs, "redaction.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(safe.Output, secret) || strings.Contains(string(log), secret) || !strings.Contains(safe.Output, "[REDACTED]") {
		t.Fatal("credential not redacted")
	}
	timed, err := run("timeout", spec, []string{"timeout"}, "", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !timed.TimedOut {
		t.Fatal("timeout not reported")
	}
	missing := spec
	missing.Image = "turnyard-nonexistent:never"
	if _, err := run("missing", missing, []string{"boundary"}, "", 5*time.Second); contracts.ErrorCode(err) != "IMAGE_MISSING" {
		t.Fatalf("missing image: %v", err)
	}
	drift := spec
	drift.ImageDigest = "sha256:wrong"
	if _, err := run("drift", drift, []string{"boundary"}, "", 5*time.Second); contracts.ErrorCode(err) != "ENVIRONMENT_DRIFT" {
		t.Fatalf("image drift: %v", err)
	}
	isolated := spec
	isolated.Network = "none"
	if name == "apple-container" {
		if _, err := run("network", isolated, []string{"network"}, "", 5*time.Second); contracts.ErrorCode(err) != "CAPABILITY_MISSING" {
			t.Fatalf("Apple network: %v", err)
		}
	} else {
		result, err := run("network", isolated, []string{"network"}, "", 30*time.Second)
		if err != nil || result.ExitCode != 0 || strings.TrimSpace(result.Output) != "blocked" {
			t.Fatalf("network=none: %v %v", result, err)
		}
	}
	oci := b.(OCIBackend)
	invocationID := contracts.NewID("orphan")
	orphanName := "ty-" + invocationID
	defer oci.cleanup(context.Background(), orphanName)
	if output, err := exec.Command(oci.dialect.Binary(), "run", "-d", "--name", orphanName,
		"--entrypoint", "sleep", spec.Image, "60").CombinedOutput(); err != nil {
		t.Fatalf("start orphan probe: %v: %s", err, output)
	}
	if !oci.exists(context.Background(), orphanName) {
		t.Fatal("orphan probe did not start")
	}
	if err := oci.StopInvocation(context.Background(), invocationID); err != nil {
		t.Fatalf("stop orphan: %v", err)
	}
	if oci.exists(context.Background(), orphanName) {
		t.Fatal("orphan remains after recovery cleanup")
	}
	taskID := contracts.NewID("work")
	checkName := "ty-check-" + taskID + "-probe"
	defer oci.cleanup(context.Background(), checkName)
	if output, err := exec.Command(oci.dialect.Binary(), "run", "-d", "--name", checkName,
		"--entrypoint", "sleep", spec.Image, "60").CombinedOutput(); err != nil {
		t.Fatalf("start check probe: %v: %s", err, output)
	}
	if !oci.exists(context.Background(), checkName) {
		t.Fatal("check probe did not start")
	}
	if err := oci.StopTaskChecks(context.Background(), taskID); err != nil {
		t.Fatalf("stop check: %v", err)
	}
	if oci.exists(context.Background(), checkName) {
		t.Fatal("check container remains after recovery cleanup")
	}
	if oci.exists(context.Background(), timeoutName) {
		t.Fatalf("timed-out container %s appeared after cleanup", timeoutName)
	}
}
