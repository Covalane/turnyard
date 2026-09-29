package registry

import (
	"testing"

	"github.com/Covalane/turnyard/internal/collections"
	"github.com/Covalane/turnyard/internal/fault"
)

func TestBuiltinBackendsResolve(t *testing.T) {
	names := Backends()
	if len(names) != len(factories) {
		t.Fatalf("listed backends=%d; registered=%d", len(names), len(factories))
	}
	seen := collections.NewSet[string](len(names))
	for _, name := range names {
		if seen.Has(name) {
			t.Fatalf("duplicate backend %q", name)
		}
		seen.Add(name)
		backend, err := Backend(name)
		if err != nil || backend.Name() != name {
			t.Fatalf("backend %q resolved to %v: %v", name, backend, err)
		}
	}
	if _, err := Backend("unknown"); fault.CodeOf(err) != fault.CodeSandboxUnavailable {
		t.Fatalf("unknown backend error: %v", err)
	}
}
