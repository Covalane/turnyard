package contracts

import "testing"

func TestDigestPreservesLargeIntegerDifferences(t *testing.T) {
	const maxExactFloat = uint64(1 << 53)
	first, err := Digest(map[string]any{"size": maxExactFloat})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Digest(map[string]any{"size": maxExactFloat + 1})
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("different integer values produced the same digest")
	}
	a, err := Digest(map[string]any{"a": 1, "b": 2})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Digest(map[string]any{"b": 2, "a": 1})
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("map key order changed digest")
	}
}
