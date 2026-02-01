package storage

import (
	"context"

	"github.com/rakunlabs/pika/internal/service"
	"github.com/rakunlabs/pika/internal/storage/sqlite"
)

type Config struct {
	SQLite sqlite.Config `cfg:"sqlite"`
}

// Storage extends service.Storage with Close functionality.
type Storage interface {
	service.Storage

	// Close closes the storage connection.
	Close() error
}

func New(ctx context.Context, cfg *Config) (Storage, error) {
	switch {
	case cfg.SQLite.Enabled:
		return sqlite.New(ctx, &cfg.SQLite)
	default:
		return nil, service.ErrNoStorageBackend
	}
}
