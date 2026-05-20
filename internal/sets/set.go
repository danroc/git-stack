// Package sets provides a small generic set type backed by a map.
package sets

import "iter"

// Set is an unordered collection of unique comparable items.
//
// The zero value is not usable; construct with [New].
type Set[T comparable] struct {
	m map[T]struct{}
}

// New returns a Set seeded with items.
func New[T comparable](items ...T) *Set[T] {
	s := &Set[T]{m: make(map[T]struct{}, len(items))}
	s.Add(items...)
	return s
}

// Add inserts items into the set. Duplicates are ignored.
func (s *Set[T]) Add(items ...T) {
	for _, item := range items {
		s.m[item] = struct{}{}
	}
}

// Has reports whether item is in the set.
func (s *Set[T]) Has(item T) bool {
	_, ok := s.m[item]
	return ok
}

// Len returns the number of items in the set.
func (s *Set[T]) Len() int { return len(s.m) }

// Items returns an iterator over the items in unspecified order.
func (s *Set[T]) Items() iter.Seq[T] {
	return func(yield func(T) bool) {
		for item := range s.m {
			if !yield(item) {
				return
			}
		}
	}
}
