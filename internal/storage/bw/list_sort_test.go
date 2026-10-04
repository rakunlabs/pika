package bw

import (
	"testing"
	"time"

	"github.com/rakunlabs/query"

	"github.com/rakunlabs/pika/internal/service"
)

// bw's typed sort treats time.Time as equal, so time-ordered listing goes
// through bucketFindSorted; these guard that path.
func TestListSortsByTimeFields(t *testing.T) {
	s, err := New(t.Context(), &Config{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := t.Context()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Insert out of time order so primary-key order != time order.
	for i, name := range []string{"b-middle", "c-newest", "a-oldest"} {
		offset := map[string]time.Duration{"a-oldest": 0, "b-middle": time.Hour, "c-newest": 2 * time.Hour}[name]
		if err := s.Tokens().Create(ctx, &service.Token{
			ID:        name,
			Name:      name,
			HashedKey: "h" + string(rune('0'+i)),
			CreatedAt: base.Add(offset),
			Active:    true,
		}); err != nil {
			t.Fatal(err)
		}
	}

	list := func(raw string) []string {
		t.Helper()
		q, err := query.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		rows, total, err := s.Tokens().List(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		if total != 3 {
			t.Fatalf("%s: total=%d", raw, total)
		}
		out := make([]string, len(rows))
		for i, r := range rows {
			out[i] = r.Name
		}
		return out
	}

	assertOrder := func(raw string, want ...string) {
		t.Helper()
		got := list(raw)
		if len(got) != len(want) {
			t.Fatalf("%s: got %v, want %v", raw, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s: got %v, want %v", raw, got, want)
			}
		}
	}

	assertOrder("_sort=created_at", "a-oldest", "b-middle", "c-newest")
	assertOrder("_sort=-created_at", "c-newest", "b-middle", "a-oldest")
	assertOrder("_sort=-created_at&_limit=1&_offset=1", "b-middle")
	assertOrder("_sort=name", "a-oldest", "b-middle", "c-newest")
}
