package contracts

import "testing"

func TestDigestPreservesLargeIntegerDifferences(t *testing.T) {
	const maxExactFloat = uint64(1 << 53)
	first := Digest(map[string]any{"size": maxExactFloat})
	second := Digest(map[string]any{"size": maxExactFloat + 1})
	if first == second {
		t.Fatal("different integer values produced the same digest")
	}
	if Digest(map[string]any{"a": 1, "b": 2}) != Digest(map[string]any{"b": 2, "a": 1}) {
		t.Fatal("map key order changed digest")
	}
}
