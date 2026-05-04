package storage

import (
	"context"

	bwstore "github.com/rakunlabs/pika/internal/storage/bw"
)

// Config selects and configures the storage backend. Today bw (a typed
// BadgerDB wrapper) is the only backend; SQLite was removed in favour of
// it (cf. the bw README for capability/cluster details).
type Config struct {
	BW bwstore.Config `cfg:"bw"`
}

// Storage extends the bw-backed storage with Close.
type Storage = *bwstore.Storage

// New opens the configured storage backend. There is currently only one
// backend; the switch shape is kept so adding additional ones later is
// purely additive.
func New(ctx context.Context, cfg *Config) (Storage, error) {
	return bwstore.New(ctx, &cfg.BW)
}
