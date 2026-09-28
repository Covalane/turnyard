package sandbox

import (
	"context"
	"strings"
	"testing"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
)

func TestModelGatewayRejectsRealAgentCredentialBeforeStarting(t *testing.T) {
	backend := NewOCIBackend(DockerDialect{Executable: "docker"})
	_, err := backend.Run(context.Background(), SandboxRun{Sandbox: contracts.SandboxSpec{Network: contracts.SandboxNetworkModelOnly},
		Credentials: map[string]string{"MODEL_KEY": "real-secret"}, ModelGateway: &ModelGateway{Endpoint: "https://example.org", Credential: "real-secret"}})
	if fault.CodeOf(err) != fault.CodeInvalidSpec {
		t.Fatalf("real credential reached agent boundary: %v", err)
	}
}

func TestGatewayResourceMissingRecognizesOnlyNamedResource(t *testing.T) {
	if !gatewayResourceMissing([]byte("Error: No such object: ty-gateway"), "ty-gateway") ||
		!gatewayResourceMissing([]byte("Error: network ty-network not found"), "ty-network") {
		t.Fatal("known missing resources were not recognized")
	}
	for _, message := range []string{"Cannot connect to Docker daemon", "network other not found", ""} {
		if gatewayResourceMissing([]byte(message), "ty-network") {
			t.Fatalf("uncertain inspection treated as absence: %s", strings.TrimSpace(message))
		}
	}
}

func TestUnknownNetworkPolicyFailsClosed(t *testing.T) {
	if _, err := (DockerDialect{}).RunOptions("typo-open"); fault.CodeOf(err) != fault.CodeInvalidSpec {
		t.Fatalf("Docker accepted unknown network policy: %v", err)
	}
	if _, err := (AppleDialect{}).RunOptions(contracts.SandboxNetworkModelOnly); fault.CodeOf(err) != fault.CodeCapabilityMissing {
		t.Fatalf("Apple accepted unavailable network isolation: %v", err)
	}
}
