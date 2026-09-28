package inputs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
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
