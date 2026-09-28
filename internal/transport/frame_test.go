package transport

import (
	"errors"
	"strings"
	"testing"
)

func TestReadFrameLimits(t *testing.T) {
	largeResult := strings.Repeat("x", maxRequestBytes) + "\n"
	got, err := readFrame(strings.NewReader(largeResult), maxResponseBytes)
	if err != nil || string(got) != largeResult {
		t.Fatalf("large result should fit response limit: bytes=%d error=%v", len(got), err)
	}
	if _, err := readFrame(strings.NewReader(largeResult), maxRequestBytes); !errors.Is(err, errFrameTooLarge) {
		t.Fatalf("expected request size error, got %v", err)
	}
}
