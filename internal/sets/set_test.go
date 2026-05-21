package sets

import (
	"iter"
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

func TestCollectDeduplicates(t *testing.T) {
	seq := func(yield func(int) bool) {
		for _, v := range []int{1, 2, 1, 3} {
			if !yield(v) {
				return
			}
		}
	}
	s := Collect[int](seq)
	if s.Len() != 3 {
		t.Fatalf("Len() = %d, want 3", s.Len())
	}
	for _, v := range []int{1, 2, 3} {
		if !s.Has(v) {
			t.Fatalf("expected %d in set", v)
		}
	}
}

func TestCollectEmpty(t *testing.T) {
	s := Collect(iter.Seq[string](func(yield func(string) bool) {}))
	if s.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", s.Len())
	}
}

func TestCollectKeysIgnoresValues(t *testing.T) {
	seq := func(yield func(string, int) bool) {
		for _, p := range [][2]any{{"a", 1}, {"b", 2}, {"a", 3}} {
			if !yield(p[0].(string), p[1].(int)) {
				return
			}
		}
	}
	s := CollectKeys[string, int](seq)
	if s.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", s.Len())
	}
	if !s.Has("a") || !s.Has("b") {
		t.Fatalf("expected a and b in set")
	}
}

func TestCollectKeysEmpty(t *testing.T) {
	s := CollectKeys(iter.Seq2[string, int](func(yield func(string, int) bool) {}))
	if s.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", s.Len())
	}
}
