package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/sandbox/docker"
)

func TestImageInspectDistinguishesMissingImageFromCLIOutage(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("test uses a POSIX shell fake")
	}
	for _, tc := range []struct {
		name, stderr string
		code         fault.Code
	}{
		{name: "missing", stderr: "Error response from daemon: No such image: test:latest", code: fault.CodeImageMissing},
		{name: "daemon unavailable", stderr: "Cannot connect to the Docker daemon", code: fault.CodeImageInspectFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "fake-container")
			if err := os.WriteFile(binary, []byte("#!/bin/sh\necho '"+tc.stderr+"' >&2\nexit 27\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			_, err := NewOCIBackend(docker.Dialect{Executable: binary}).ImageIdentity(context.Background(), "test:latest")
			if fault.CodeOf(err) != tc.code || fault.Origin(err) == "" {
				t.Fatalf("inspect code=%s, err=%v", fault.CodeOf(err), err)
			}
		})
	}
}
