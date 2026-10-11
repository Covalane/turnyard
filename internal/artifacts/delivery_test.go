package artifacts

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Covalane/turnyard/internal/contracts"
)

func TestConnectorDeliveryPreservesExistingObjectsAndReconcilesWrites(t *testing.T) {
	for _, tc := range []struct {
		scenario, remote string
		status           contracts.DeliverableStatus
		put              bool
	}{
		{"unknown", "other", StatusUnverified, false},
		{"same", "new", StatusPresent, false},
		{"conflict", "other", StatusMismatch, false},
		{"missing", "", StatusPresent, true},
		{"race-same", "", StatusPresent, true},
		{"race-conflict", "", StatusMismatch, true},
		{"lost-ack", "", StatusPresent, true},
		{"readback-failure", "", StatusUnverified, true},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("TURNYARD_DELIVERY_HELPER", "1")
			t.Setenv("TURNYARD_DELIVERY_ROOT", root)
			t.Setenv("TURNYARD_DELIVERY_SCENARIO", tc.scenario)
			local := filepath.Join(root, "source")
			if err := os.WriteFile(local, []byte("new"), 0600); err != nil {
				t.Fatal(err)
			}
			remote := filepath.Join(root, "remote")
			if tc.remote != "" {
				if err := os.WriteFile(remote, []byte(tc.remote), 0600); err != nil {
					t.Fatal(err)
				}
			}
			connector := contracts.ArtifactConnectorSpec{
				ID: "object", URIPrefix: "mem://bucket/objects/", PutMode: contracts.ConnectorPutCreateOnly,
				NotFoundExitCodes: []int{44},
				GetArgv:           []string{os.Args[0], "-test.run=^TestDeliveryHelperProcess$", "--", "get", "{uri}", "{file}"},
				PutArgv:           []string{os.Args[0], "-test.run=^TestDeliveryHelperProcess$", "--", "put", "{uri}", "{file}"},
				PassEnv:           []string{"TURNYARD_DELIVERY_HELPER", "TURNYARD_DELIVERY_ROOT", "TURNYARD_DELIVERY_SCENARIO"},
			}
			spec := contracts.DeliverableSpec{ID: "report", Destination: &contracts.DestinationSpec{
				Kind: contracts.DestinationConnector, Connector: "object", URI: "mem://bucket/objects/{sha256}"}}
			result := DeliverConnector(context.Background(), contracts.EnvironmentSpec{ArtifactConnectors: []contracts.ArtifactConnectorSpec{connector}},
				spec, Result{Status: StatusPresent, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("new"))), LocalPath: local})
			if result.Status != tc.status {
				t.Fatalf("result=%+v", result)
			}
			trace, err := os.ReadFile(filepath.Join(root, "trace"))
			if err != nil || strings.Contains(string(trace), "put") != tc.put {
				t.Fatalf("write decision: %s %v", trace, err)
			}
			body, err := os.ReadFile(remote)
			if tc.remote != "" && (err != nil || string(body) != tc.remote) {
				t.Fatalf("existing object changed: %q %v", body, err)
			}
			if tc.scenario == "race-conflict" && string(body) != "other" {
				t.Fatalf("racing object overwritten: %q", body)
			}
		})
	}
}

func TestDeliveryHelperProcess(t *testing.T) {
	if os.Getenv("TURNYARD_DELIVERY_HELPER") != "1" {
		return
	}
	root := os.Getenv("TURNYARD_DELIVERY_ROOT")
	scenario := os.Getenv("TURNYARD_DELIVERY_SCENARIO")
	mode, file := os.Args[len(os.Args)-3], os.Args[len(os.Args)-1]
	trace, err := os.OpenFile(filepath.Join(root, "trace"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(2)
	}
	if _, err := fmt.Fprintln(trace, mode); err != nil {
		os.Exit(2)
	}
	trace.Close()
	remote := filepath.Join(root, "remote")
	if mode == "get" {
		if scenario == "unknown" {
			os.Exit(1)
		}
		if _, err := os.Stat(filepath.Join(root, "readfail")); err == nil {
			os.Exit(1)
		}
		body, err := os.ReadFile(remote)
		if os.IsNotExist(err) {
			os.Exit(44)
		}
		if err != nil || os.WriteFile(file, body, 0600) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	if mode != "put" {
		os.Exit(2)
	}
	if scenario == "race-same" || scenario == "race-conflict" {
		body := "new"
		if scenario == "race-conflict" {
			body = "other"
		}
		if os.WriteFile(remote, []byte(body), 0600) != nil {
			os.Exit(1)
		}
	}
	// O_EXCL models the storage service's atomic conditional create.
	target, err := os.OpenFile(remote, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		os.Exit(1)
	}
	source, err := os.Open(file)
	if err != nil {
		os.Exit(1)
	}
	_, err = io.Copy(target, source)
	source.Close()
	closeErr := target.Close()
	if err != nil || closeErr != nil {
		os.Exit(1)
	}
	if scenario == "readback-failure" {
		if os.WriteFile(filepath.Join(root, "readfail"), nil, 0600) != nil {
			os.Exit(1)
		}
	}
	if scenario == "lost-ack" {
		os.Exit(1)
	}
	os.Exit(0)
}
