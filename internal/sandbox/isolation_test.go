package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
)

func fakeDockerCLI(t *testing.T, output string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "docker")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s' '"+output+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestGVisorRequiresRegisteredDockerRuntime(t *testing.T) {
	ctx := context.Background()
	spec := contracts.SandboxSpec{Backend: BackendDocker, Network: contracts.SandboxNetworkModelOnly,
		Isolation: contracts.SandboxIsolationGVisor}
	available := NewOCIBackend(DockerDialect{Executable: fakeDockerCLI(t, `{"runsc":{},"runc":{}}`)})
	if err := available.ValidateSpec(ctx, spec); err != nil {
		t.Fatalf("registered runsc rejected: %v", err)
	}
	options, err := available.dialect.IsolationOptions(ctx, spec.Isolation)
	if err != nil || !reflect.DeepEqual(options, []string{"--runtime", "runsc"}) {
		t.Fatalf("gVisor runtime options: %v %v", options, err)
	}
	missing := NewOCIBackend(DockerDialect{Executable: fakeDockerCLI(t, `{"runc":{}}`)})
	if code := fault.CodeOf(missing.ValidateSpec(ctx, spec)); code != fault.CodeCapabilityMissing {
		t.Fatalf("missing runsc code: %s", code)
	}
	if code := fault.CodeOf(missing.ValidateSpec(ctx, contracts.SandboxSpec{Isolation: "unknown"})); code != fault.CodeCapabilityMissing {
		t.Fatalf("unknown isolation code: %s", code)
	}
	for _, backend := range []SandboxBackend{NewOCIBackend(AppleDialect{}), NewOCIBackend(PodmanDialect{})} {
		unsupported := spec
		unsupported.Network = contracts.SandboxNetworkDefault
		if code := fault.CodeOf(backend.ValidateSpec(ctx, unsupported)); code != fault.CodeCapabilityMissing {
			t.Errorf("%s accepted gVisor: %s", backend.Name(), code)
		}
	}
}

func TestGVisorModelGatewayUsesInternalAddress(t *testing.T) {
	lease := &modelGatewayLease{network: "ty-net-invocation", address: "172.30.0.2"}
	spec := contracts.SandboxSpec{Isolation: contracts.SandboxIsolationGVisor}
	opts, err := gatewayRunOptions(spec, lease)
	if err != nil || !reflect.DeepEqual(opts, []string{"--network", lease.network, "--add-host", "turnyard-model:172.30.0.2"}) {
		t.Fatalf("gateway options: %v %v", opts, err)
	}
	lease.address = ""
	if code := fault.CodeOf(func() error { _, err := gatewayRunOptions(spec, lease); return err }()); code != fault.CodeSandboxUnavailable {
		t.Fatalf("missing gateway address code: %s", code)
	}
	if opts, err := gatewayRunOptions(contracts.SandboxSpec{}, lease); err != nil || len(opts) != 2 {
		t.Fatalf("default runtime gateway options: %v %v", opts, err)
	}
	cli := fakeDockerCLI(t, `{"ty-net-invocation":{"IPAddress":"172.30.0.2"}}`)
	address, err := gatewayNetworkAddress(context.Background(), cli, "ty-model-test", lease.network)
	if err != nil || address != "172.30.0.2" {
		t.Fatalf("gateway inspect: %s %v", address, err)
	}
	if _, err := gatewayNetworkAddress(context.Background(), cli, "ty-model-test", "wrong-network"); fault.CodeOf(err) != fault.CodeSandboxUnavailable {
		t.Fatalf("missing internal network accepted: %v", err)
	}
}
