package artifactio

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Covalane/turnyard/internal/contracts"
)

func TestGetStopsOversizedConnectorDownloadAndRemovesTemporaryFile(t *testing.T) {
	t.Setenv("TURNYARD_CONNECTOR_OVERSIZE_HELPER", "1")
	connector := Connector{Spec: contracts.ArtifactConnectorSpec{
		ID: "oversize", URIPrefix: "mem://bucket/allowed/",
		GetArgv: []string{os.Args[0], "-test.run=^TestOversizeConnectorHelperProcess$", "--", "{uri}", "{file}"},
		PassEnv: []string{"TURNYARD_CONNECTOR_OVERSIZE_HELPER"},
	}}
	root := t.TempDir()
	start := time.Now()
	err := connector.Get(context.Background(), "mem://bucket/allowed/data", filepath.Join(root, "result"), 1024)
	if err == nil {
		t.Fatal("oversized connector download accepted")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("oversized download was not interrupted during transfer: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("oversized download left temporary files: %+v %v", entries, err)
	}
}

func TestOversizeConnectorHelperProcess(t *testing.T) {
	if os.Getenv("TURNYARD_CONNECTOR_OVERSIZE_HELPER") != "1" {
		return
	}
	file := os.Args[len(os.Args)-1]
	if !strings.HasPrefix(os.Args[len(os.Args)-2], "mem://bucket/allowed/") ||
		os.WriteFile(file, make([]byte, 64<<10), 0o600) != nil {
		os.Exit(2)
	}
	time.Sleep(2 * time.Second)
	os.Exit(0)
}
