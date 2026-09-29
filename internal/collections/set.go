// Package collections contains small data structures shared across Turnyard.
package collections

// Set stores unique comparable values. Its zero value is ready to use.
// Like a Go map, Set requires external synchronization for concurrent access.
type Set[T comparable] map[T]struct{}

// NewSet preallocates room for approximately capacity values.
func NewSet[T comparable](capacity int) Set[T] {
	return make(Set[T], capacity)
}

// SetOf creates a set from values, discarding duplicates.
func SetOf[T comparable](values ...T) Set[T] {
	set := NewSet[T](len(values))
	for _, value := range values {
		set.Add(value)
	}
	return set
}

// Add reports whether value was newly added.
func (set *Set[T]) Add(value T) bool {
	if *set == nil {
		*set = make(Set[T])
	}
	if set.Has(value) {
		return false
	}
	(*set)[value] = struct{}{}
	return true
}

func (set Set[T]) Has(value T) bool {
	_, exists := set[value]
	return exists
}

// Remove reports whether value was present.
func (set Set[T]) Remove(value T) bool {
	if !set.Has(value) {
		return false
	}
	delete(set, value)
	return true
}

func (set Set[T]) Len() int { return len(set) }
