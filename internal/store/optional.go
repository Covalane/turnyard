package store

import "encoding/json"

// Optional values are domain-facing projections of nullable Ent fields.
// Neither Ent entities nor SQL null types cross the storage boundary.
type OptionalString struct {
	Value   string
	Present bool
}
type OptionalInt struct {
	Value   int64
	Present bool
}
type OptionalFloat struct {
	Value   float64
	Present bool
}

// Nullable fields encode as JSON null until a value is present. Keeping this
// behavior on the value types lets row structs use their declared JSON tags.
func (x OptionalString) MarshalJSON() ([]byte, error) {
	if !x.Present {
		return []byte("null"), nil
	}
	return json.Marshal(x.Value)
}

func (x OptionalInt) MarshalJSON() ([]byte, error) {
	if !x.Present {
		return []byte("null"), nil
	}
	return json.Marshal(x.Value)
}

func (x OptionalFloat) MarshalJSON() ([]byte, error) {
	if !x.Present {
		return []byte("null"), nil
	}
	return json.Marshal(x.Value)
}
