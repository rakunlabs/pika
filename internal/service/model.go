package service

import (
	"context"
	"errors"
)

var (
	ErrNotFound         = errors.New("not found")
	ErrBadRequest       = errors.New("bad request")
	ErrNoStorageBackend = errors.New("no storage backend configured")
)

// KeyValue represents a key-value pair for search results.
type KeyValue struct {
	Key   string
	Value []byte
}

// Storage defines the interface for the underlying storage backend.
type Storage interface {
	// Get retrieves the value for the given key.
	//  - if not found, returns service.ErrNotFound
	Get(ctx context.Context, key string) ([]byte, error)

	// Set stores the key-value pair.
	//  - uses UPSERT semantics
	Set(ctx context.Context, key string, value []byte) error

	// Delete removes the key-value pair for the given key.
	Delete(ctx context.Context, key string) error

	// For iterates over all key-value pairs where the key starts with the given prefix.
	For(ctx context.Context, prefix string, fn func(ctx context.Context, key string, value []byte) error) error

	// Tx executes a function within a transaction.
	//  - if the function returns an error, the transaction is rolled back.
	Tx(ctx context.Context, fn func(ctx context.Context, tx Storage) error) error
}

// Folder represents a directory containing folders and files.
type Folder struct {
	Folders []string `json:"folders"`
	Files   []string `json:"files"`
}

// SearchResult represents a search result item.
type SearchResult struct {
	Path string `json:"path"`
	File *File  `json:"file"`
}
