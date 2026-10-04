package service_test

import (
	"testing"

	"github.com/rakunlabs/query"

	"github.com/rakunlabs/pika/internal/service"
	bwstore "github.com/rakunlabs/pika/internal/storage/bw"
)

func newTokenTestStore(t *testing.T) *bwstore.Storage {
	t.Helper()
	store, err := bwstore.New(t.Context(), &bwstore.Config{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func newServiceOn(t *testing.T, store *bwstore.Storage) *service.Service {
	t.Helper()
	svc := service.New(store)
	t.Cleanup(svc.Close)
	return svc
}

func createReadToken(t *testing.T, svc *service.Service, name string) *service.CreateTokenResponse {
	t.Helper()
	created, err := svc.CreateToken(t.Context(), &service.CreateTokenRequest{
		Name:   name,
		Scopes: []service.TokenScope{{Path: "**", Operations: []string{"read"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func lastUsedFromFreshService(t *testing.T, store *bwstore.Storage) *service.TokenInfo {
	t.Helper()
	tokens, _, err := newServiceOn(t, store).ListTokens(t.Context(), nil)
	if err != nil || len(tokens) != 1 {
		t.Fatalf("list: %v (%d tokens)", err, len(tokens))
	}
	return &tokens[0]
}

func TestTokenLastUsedAt(t *testing.T) {
	store := newTokenTestStore(t)
	svc := newServiceOn(t, store)
	ctx := t.Context()

	created := createReadToken(t, svc, "ci")

	list, _, err := svc.ListTokens(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if list[0].LastUsedAt != nil {
		t.Fatal("new token should have no last_used_at")
	}

	if _, err := svc.AuthenticateToken(ctx, created.RawKey); err != nil {
		t.Fatal(err)
	}

	// Visible on this node before the batch is flushed...
	list, _, _ = svc.ListTokens(ctx, nil)
	if list[0].LastUsedAt == nil {
		t.Fatal("last_used_at should reflect the unflushed use")
	}
	if lastUsedFromFreshService(t, store).LastUsedAt != nil {
		t.Fatal("use should not be persisted before flush")
	}

	// ...and persisted after.
	svc.FlushTokenUsage(ctx)
	if lastUsedFromFreshService(t, store).LastUsedAt == nil {
		t.Fatal("persisted last_used_at missing after flush")
	}
}

func TestTokenUsageFlushDoesNotResurrectDeletedToken(t *testing.T) {
	store := newTokenTestStore(t)
	svc := newServiceOn(t, store)
	ctx := t.Context()

	created := createReadToken(t, svc, "revoked")
	if _, err := svc.AuthenticateToken(ctx, created.RawKey); err != nil {
		t.Fatal(err)
	}
	// Delete straight in storage so the pending usage isn't forgotten,
	// mimicking a delete that races the flush.
	if err := store.Tokens().Delete(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	svc.FlushTokenUsage(ctx)

	if _, err := store.Tokens().Get(ctx, created.ID); err == nil {
		t.Fatal("flush re-created a deleted token")
	}
}

func TestTokenUsageGateSkipsWrites(t *testing.T) {
	store := newTokenTestStore(t)
	svc := newServiceOn(t, store)
	ctx := t.Context()
	svc.SetBackgroundWriteGate(func() bool { return false })

	created := createReadToken(t, svc, "follower")
	if _, err := svc.AuthenticateToken(ctx, created.RawKey); err != nil {
		t.Fatal(err)
	}
	svc.FlushTokenUsage(ctx)

	if lastUsedFromFreshService(t, store).LastUsedAt != nil {
		t.Fatal("closed gate must not persist usage")
	}
}

func TestListTokensPaging(t *testing.T) {
	svc := newServiceOn(t, newTokenTestStore(t))
	for _, name := range []string{"a", "b", "c"} {
		createReadToken(t, svc, name)
	}

	q, err := query.Parse("_limit=2&_offset=0&_sort=name")
	if err != nil {
		t.Fatal(err)
	}
	page, total, err := svc.ListTokens(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(page) != 2 || page[0].Name != "a" {
		t.Fatalf("page=%v total=%d", page, total)
	}
}
