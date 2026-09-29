package collections

import "testing"

func TestSetZeroValueAndMembership(t *testing.T) {
	var values Set[string]
	if values.Has("a") || values.Remove("a") || values.Len() != 0 {
		t.Fatal("zero value is not empty")
	}
	if !values.Add("a") || values.Add("a") || !values.Has("a") || values.Len() != 1 {
		t.Fatal("add did not preserve set semantics")
	}
	if !values.Remove("a") || values.Remove("a") || values.Len() != 0 {
		t.Fatal("remove did not preserve set semantics")
	}
}

func TestSetOfAndPreallocation(t *testing.T) {
	values := SetOf("a", "b", "a")
	if values.Len() != 2 || !values.Has("a") || !values.Has("b") {
		t.Fatal("SetOf did not deduplicate")
	}
	integers := NewSet[int](2)
	integers.Add(3)
	if !integers.Has(3) {
		t.Fatal("generic key is unavailable")
	}
}
