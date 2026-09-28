package inputs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Covalane/turnyard/internal/contracts"
)

func TestHTTPSDownloadChecksHostAndRedirect(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "https://other.example/private", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("image bytes"))
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "input")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	transport := server.Client().Transport
	if err := downloadHTTPSWithTransport(context.Background(), server.URL+"/image", []string{"127.0.0.1"}, path, transport); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "image bytes" {
		t.Fatalf("downloaded bytes: %q %v", got, err)
	}
	if err := downloadHTTPSWithTransport(context.Background(), server.URL+"/redirect", []string{"127.0.0.1"}, path, transport); err == nil {
		t.Fatal("redirect outside the host allowlist was accepted")
	}
	if err := downloadHTTPSWithTransport(context.Background(), server.URL+"/image", []string{"other.example"}, path, transport); err == nil {
		t.Fatal("unlisted source host was accepted")
	}
}

func TestPartialStageRemovesLinkedSnapshot(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first.txt")
	if err := os.WriteFile(first, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	work := contracts.WorkSpec{IdempotencyKey: "partial", Inputs: []contracts.InputSpec{
		{ID: "first", Source: contracts.InputSource{Kind: contracts.InputFile, Path: first}},
		{ID: "missing", Source: contracts.InputSource{Kind: contracts.InputFile, Path: filepath.Join(root, "missing.txt")}},
	}}
	batch := BatchPath(root, work)
	if _, err := Stage(context.Background(), work, "", root, contracts.EnvironmentSpec{}); err == nil {
		t.Fatal("missing second input did not fail staging")
	}
	if _, err := os.Stat(batch); !os.IsNotExist(err) {
		t.Fatalf("partial snapshot remained: %v", err)
	}
}

func TestPruneOrphanedKeepsCommittedBatches(t *testing.T) {
	root := t.TempDir()
	session := "ses_test"
	inputRoot := filepath.Join(root, "sessions", session, "inputs")
	committed := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	orphan := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	for _, batch := range []string{committed, orphan, "unrelated"} {
		if err := os.MkdirAll(filepath.Join(inputRoot, batch), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := PruneOrphaned(context.Background(), root, map[string]bool{session + "/" + committed: true})
	if err != nil || removed != 1 {
		t.Fatalf("pruned %d batches: %v", removed, err)
	}
	for _, batch := range []string{committed, "unrelated"} {
		if _, err := os.Stat(filepath.Join(inputRoot, batch)); err != nil {
			t.Fatalf("retained batch %s: %v", batch, err)
		}
	}
	if _, err := os.Stat(filepath.Join(inputRoot, orphan)); !os.IsNotExist(err) {
		t.Fatalf("orphaned batch remains: %v", err)
	}
}
