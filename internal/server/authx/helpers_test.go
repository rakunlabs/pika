package authx

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rakunlabs/pika/internal/service"
	"github.com/rakunlabs/pika/internal/storage/sqlite"
)

// newTestService boots an in-memory-like Service backed by a temp SQLite DB
// with all migrations applied. Each test gets a fresh, isolated database.
func newTestService(t *testing.T) *service.Service {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "pika-test.db")
	dsn := "file:" + dbPath + "?cache=shared"

	ctx := context.Background()
	store, err := sqlite.New(ctx, &sqlite.Config{
		DSN: dsn,
		Migration: sqlite.Migration{
			Enabled: true,
			DSN:     dsn,
		},
	})
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	return service.New(store)
}
