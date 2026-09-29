package contracts

import (
	"errors"
	"testing"

	"github.com/Covalane/turnyard/internal/fault"
)

type failingEntropy struct{}

func (failingEntropy) Read([]byte) (int, error) { return 0, errors.New("entropy unavailable") }

func TestUtilityFailuresReturnErrors(t *testing.T) {
	if _, err := JSONText(make(chan int)); fault.CodeOf(err) != fault.CodeInternalError {
		t.Fatalf("JSON encoding failure was not returned: %v", err)
	}
	if _, err := Digest(make(chan int)); fault.CodeOf(err) != fault.CodeInternalError {
		t.Fatalf("digest encoding failure was not returned: %v", err)
	}
	if _, err := newID("req", failingEntropy{}); fault.CodeOf(err) != fault.CodeInternalError {
		t.Fatalf("random source failure was not returned: %v", err)
	}
}
