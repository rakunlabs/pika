package storage

import (
	"context"
	"errors"

	"github.com/rakunlabs/pika/internal/service"
	"github.com/rakunlabs/pika/internal/storage/sqlite"
)

var (
	ErrNoStorageBackend = errors.New("no storage backend configured")
)

type Config struct {
	SQLite *sqlite.Config `cfg:"sqlite"`
}

func New(ctx context.Context, cfg *Config) (service.Storage, error) {
	switch {
	case cfg.SQLite != nil:
		return sqlite.New(ctx, cfg.SQLite)
	default:
		return nil, ErrNoStorageBackend
	}
}
