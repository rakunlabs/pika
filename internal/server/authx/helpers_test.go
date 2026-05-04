package authx

import (
	"testing"

	"github.com/rakunlabs/pika/internal/service"
	bwstore "github.com/rakunlabs/pika/internal/storage/bw"
)

// newTestService boots an in-memory bw-backed Service. Each test gets
// a fresh, isolated database (Badger in-memory mode skips the on-
// disk directory entirely).
func newTestService(t *testing.T) *service.Service {
	t.Helper()

	store, err := bwstore.New(t.Context(), &bwstore.Config{InMemory: true})
	if err != nil {
		t.Fatalf("bw.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	return service.New(store)
}
