package bw

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/rakunlabs/pika/internal/service"
	"github.com/rakunlabs/query"
)

func newTestStore(t *testing.T) *Storage {
	t.Helper()
	store, err := New(t.Context(), &Config{InMemory: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func mustParse(t *testing.T, s string) *query.Query {
	t.Helper()
	q, err := query.Parse(s)
	if err != nil {
		t.Fatalf("query.Parse(%q): %v", s, err)
	}
	return q
}

func TestTokenStorage_CRUD(t *testing.T) {
	ctx := t.Context()
	store := newTestStore(t)
	tokens := store.Tokens()

	now := time.Now().UTC().Truncate(time.Microsecond)
	exp := now.Add(time.Hour)
	tok := &service.Token{
		ID:        "tok-1",
		Name:      "ci",
		HashedKey: "abc123",
		Scopes:    []service.TokenScope{{Path: "app/*", Operations: []string{"read"}}},
		CreatedAt: now,
		CreatedBy: "alice",
		ExpiresAt: &exp,
		Active:    true,
	}
	if err := tokens.Create(ctx, tok); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := tokens.Get(ctx, "tok-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "ci" || got.HashedKey != "abc123" || got.CreatedBy != "alice" || !got.Active {
		t.Errorf("Get = %+v", got)
	}
	if !got.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt = %v; want %v", got.CreatedAt, now)
	}
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(exp) {
		t.Errorf("ExpiresAt = %v; want %v", got.ExpiresAt, exp)
	}
	if len(got.Scopes) != 1 || got.Scopes[0].Path != "app/*" || len(got.Scopes[0].Operations) != 1 {
		t.Errorf("Scopes = %+v", got.Scopes)
	}

	byHash, err := tokens.FindByHash(ctx, "abc123")
	if err != nil {
		t.Fatalf("FindByHash: %v", err)
	}
	if byHash.ID != "tok-1" {
		t.Errorf("FindByHash ID = %q", byHash.ID)
	}

	if _, err := tokens.FindByHash(ctx, "unknown"); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("FindByHash(unknown) err = %v; want ErrNotFound", err)
	}
	if _, err := tokens.Get(ctx, "missing"); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("Get(missing) err = %v; want ErrNotFound", err)
	}

	used := now.Add(time.Minute)
	got.Active = false
	got.LastUsedAt = &used
	got.Name = "ci-renamed"
	if err := tokens.Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got2, err := tokens.Get(ctx, "tok-1")
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if got2.Active || got2.Name != "ci-renamed" || got2.LastUsedAt == nil || !got2.LastUsedAt.Equal(used) {
		t.Errorf("after update = %+v", got2)
	}

	if err := tokens.Delete(ctx, "tok-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := tokens.Get(ctx, "tok-1"); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("Get after delete err = %v; want ErrNotFound", err)
	}
	if _, err := tokens.FindByHash(ctx, "abc123"); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("FindByHash after delete err = %v; want ErrNotFound", err)
	}
}

func TestTokenStorage_UniqueHash(t *testing.T) {
	ctx := t.Context()
	store := newTestStore(t)
	tokens := store.Tokens()

	if err := tokens.Create(ctx, &service.Token{ID: "a", HashedKey: "dup", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("Create a: %v", err)
	}
	err := tokens.Create(ctx, &service.Token{ID: "b", HashedKey: "dup", CreatedAt: time.Now()})
	if err == nil {
		t.Fatal("Create with duplicate hashed_key succeeded; want error")
	}
}

// bw's Bucket.Update is an upsert, so updating an unknown id creates it.
// This pins the current behaviour so a semantic change is noticed.
func TestTokenStorage_UpdateMissingUpserts(t *testing.T) {
	ctx := t.Context()
	store := newTestStore(t)
	if err := store.Tokens().Update(ctx, &service.Token{ID: "nope", HashedKey: "x"}); err != nil {
		t.Fatalf("Update(missing): %v", err)
	}
	if _, err := store.Tokens().Get(ctx, "nope"); err != nil {
		t.Fatalf("Get after upsert: %v", err)
	}
}

func TestTokenStorage_ListPagingAndCount(t *testing.T) {
	ctx := t.Context()
	store := newTestStore(t)
	tokens := store.Tokens()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 5 {
		if err := tokens.Create(ctx, &service.Token{
			ID:        fmt.Sprintf("t%d", i),
			Name:      fmt.Sprintf("name-%d", i),
			HashedKey: fmt.Sprintf("h%d", i),
			CreatedAt: base.Add(time.Duration(i) * time.Hour),
			CreatedBy: map[bool]string{true: "alice", false: "bob"}[i%2 == 0],
			Active:    true,
		}); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}

	all, total, err := tokens.List(ctx, nil)
	if err != nil {
		t.Fatalf("List(nil): %v", err)
	}
	if len(all) != 5 || total != 5 {
		t.Fatalf("List(nil) = %d rows, total %d; want 5, 5", len(all), total)
	}

	page, total, err := tokens.List(ctx, mustParse(t, "_limit=2&_offset=1&_sort=name"))
	if err != nil {
		t.Fatalf("List paged: %v", err)
	}
	if total != 5 {
		t.Errorf("paged total = %d; want 5", total)
	}
	if len(page) != 2 || page[0].Name != "name-1" || page[1].Name != "name-2" {
		t.Errorf("paged = %v", tokenNames(page))
	}

	// Time-sorted path (handled in-process by bucketFindSorted).
	desc, total, err := tokens.List(ctx, mustParse(t, "_sort=-created_at&_limit=3"))
	if err != nil {
		t.Fatalf("List time sorted: %v", err)
	}
	if total != 5 {
		t.Errorf("time-sorted total = %d; want 5", total)
	}
	if got := tokenNames(desc); len(got) != 3 || got[0] != "name-4" || got[1] != "name-3" || got[2] != "name-2" {
		t.Errorf("time-sorted = %v", got)
	}

	beyond, total, err := tokens.List(ctx, mustParse(t, "_sort=created_at&_offset=10"))
	if err != nil {
		t.Fatalf("List beyond: %v", err)
	}
	if len(beyond) != 0 || total != 5 {
		t.Errorf("beyond = %d rows, total %d; want 0, 5", len(beyond), total)
	}

	filtered, total, err := tokens.List(ctx, mustParse(t, "created_by=alice&_limit=1"))
	if err != nil {
		t.Fatalf("List filtered: %v", err)
	}
	if len(filtered) != 1 || total != 3 {
		t.Errorf("filtered = %d rows, total %d; want 1, 3", len(filtered), total)
	}
}

func tokenNames(ts []service.Token) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Name
	}
	return out
}

func TestTokenStorage_TxVisibility(t *testing.T) {
	ctx := t.Context()
	store := newTestStore(t)

	err := store.Tx(ctx, func(ctx context.Context, tx service.Storage) error {
		if err := tx.Tokens().Create(ctx, &service.Token{ID: "tx", HashedKey: "txh", CreatedAt: time.Now()}); err != nil {
			return err
		}
		got, err := tx.Tokens().FindByHash(ctx, "txh")
		if err != nil {
			return fmt.Errorf("FindByHash in tx: %w", err)
		}
		if got.ID != "tx" {
			return fmt.Errorf("FindByHash in tx ID = %q", got.ID)
		}
		_, total, err := tx.Tokens().List(ctx, nil)
		if err != nil {
			return err
		}
		if total != 1 {
			return fmt.Errorf("List in tx total = %d", total)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Tx: %v", err)
	}
	if _, err := store.Tokens().Get(ctx, "tx"); err != nil {
		t.Fatalf("Get after commit: %v", err)
	}
}
