package store

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
