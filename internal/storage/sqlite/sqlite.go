package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/rakunlabs/pika/internal/service"

	_ "modernc.org/sqlite"
)

var (
	DefaultDSN             = "file:pika.db?cache=shared"
	DefaultMaxIdleConns    = 3
	DefaultMaxOpenConns    = 5
	DefaultConnMaxIdleTime = 15 * time.Minute
)

type Querier interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

type Config struct {
	Enabled bool   `cfg:"enabled" default:"true"`
	DSN     string `cfg:"dsn"`

	MaxIdleConns    *int           `cfg:"max_idle_conns"`
	MaxOpenConns    *int           `cfg:"max_open_conns"`
	ConnMaxIdleTime *time.Duration `cfg:"conn_max_idle_time"`

	Migration Migration `cfg:"migration"`
}

type Migration struct {
	Enabled bool   `cfg:"enabled" default:"true"`
	DSN     string `cfg:"dsn"`
}

func (c *Config) GetConfig() Config {
	cfg := Config{
		DSN:             DefaultDSN,
		MaxIdleConns:    &DefaultMaxIdleConns,
		MaxOpenConns:    &DefaultMaxOpenConns,
		ConnMaxIdleTime: &DefaultConnMaxIdleTime,
	}

	if c.DSN != "" {
		cfg.DSN = c.DSN
	}
	if c.MaxIdleConns != nil {
		cfg.MaxIdleConns = c.MaxIdleConns
	}
	if c.MaxOpenConns != nil {
		cfg.MaxOpenConns = c.MaxOpenConns
	}
	if c.ConnMaxIdleTime != nil {
		cfg.ConnMaxIdleTime = c.ConnMaxIdleTime
	}

	return cfg
}

type Sqlite struct {
	db *sql.DB

	q Querier
}

func New(ctx context.Context, cfg *Config) (*Sqlite, error) {
	c := cfg.GetConfig()

	// migration
	if cfg.Migration.Enabled {
		migCfg := cfg.Migration
		if migCfg.DSN == "" {
			migCfg.DSN = c.DSN
		}
		if err := migration(ctx, migCfg.DSN); err != nil {
			return nil, err
		}
	}

	db, err := sql.Open("sqlite", c.DSN)
	if err != nil {
		return nil, err
	}

	if err := db.PingContext(ctx); err != nil {
		return nil, err
	}

	if c.MaxIdleConns != nil {
		db.SetMaxIdleConns(*c.MaxIdleConns)
	}

	if c.MaxOpenConns != nil {
		db.SetMaxOpenConns(*c.MaxOpenConns)
	}

	if c.ConnMaxIdleTime != nil {
		db.SetConnMaxIdleTime(*c.ConnMaxIdleTime)
	}

	return &Sqlite{db: db, q: db}, nil
}

func (m *Sqlite) Tx(ctx context.Context, fn func(ctx context.Context, tx service.Storage) error) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	sqliteTx := &Sqlite{db: m.db, q: tx}

	if err := fn(ctx, sqliteTx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return rbErr
		}
		return err
	}

	return tx.Commit()
}

// Get retrieves the value for the given key.
// If key ends with "/", it returns a JSON-encoded Folder with immediate children (folders and files).
// Otherwise, it returns the raw file value.
// Returns ErrNotFound if the key does not exist (for files) or has no children (for folders).
func (m *Sqlite) Get(ctx context.Context, key string) ([]byte, error) {
	var value []byte
	err := m.q.QueryRowContext(ctx, "SELECT value FROM pika WHERE key = ?", key).Scan(&value)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrNotFound
		}
		return nil, err
	}
	return value, nil
}

// Set stores the key-value pair using UPSERT semantics.
func (m *Sqlite) Set(ctx context.Context, key string, value []byte) error {
	_, err := m.q.ExecContext(
		ctx,
		"INSERT INTO pika (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		key, value,
	)
	return err
}

// Delete removes the key-value pair for the given key.
func (m *Sqlite) Delete(ctx context.Context, key string) error {
	_, err := m.q.ExecContext(ctx, "DELETE FROM pika WHERE key = ?", key)
	return err
}

func (m *Sqlite) For(ctx context.Context, prefix string, fn func(ctx context.Context, key string, value []byte) error) error {
	rows, err := m.q.QueryContext(ctx, "SELECT key, value FROM pika WHERE key LIKE ?", prefix+"%")
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var key string
		var value []byte
		if err := rows.Scan(&key, &value); err != nil {
			return err
		}

		if err := fn(ctx, key, value); err != nil {
			return err
		}
	}

	return rows.Err()
}

func (m *Sqlite) Close() error {
	return m.db.Close()
}
