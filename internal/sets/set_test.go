package sets

import (
	"slices"
	"testing"
)

func TestNewSeedsItems(t *testing.T) {
	s := New("a", "b", "a")
	if s.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", s.Len())
	}
	if !s.Has("a") || !s.Has("b") {
		t.Fatalf("expected a and b in set")
	}
}

func TestAddIsIdempotent(t *testing.T) {
	s := New[int]()
	s.Add(1, 2)
	s.Add(2, 3)
	if s.Len() != 3 {
		t.Fatalf("Len() = %d, want 3", s.Len())
	}
}

func TestHasMissing(t *testing.T) {
	s := New[string]()
	if s.Has("x") {
		t.Fatalf("empty set should not contain anything")
	}
}

func TestItemsIteratesEveryItem(t *testing.T) {
	s := New(3, 1, 2)
	var got []int
	for item := range s.Items() {
		got = append(got, item)
	}
	slices.Sort(got)
	if !slices.Equal(got, []int{1, 2, 3}) {
		t.Fatalf("Items() yielded %v, want [1 2 3] (in any order)", got)
	}
}

func TestItemsStopsOnEarlyReturn(t *testing.T) {
	s := New(1, 2, 3, 4, 5)
	count := 0
	for range s.Items() {
		count++
		if count == 2 {
			break
		}
	}
	if count != 2 {
		t.Fatalf("iteration count = %d, want 2", count)
	}
}
