package sqlite

import (
	"context"
	"database/sql"
	"time"

	_ "modernc.org/sqlite"
)

var (
	DefaultDSN             = "file:pika.db?cache=shared"
	DefaultMaxIdleConns    = 2
	DefaultMaxOpenConns    = 5
	DefaultConnMaxIdleTime = 15 * time.Minute
)

type Config struct {
	DSN string `cfg:"dsn"`

	MaxIdleConns    *int           `cfg:"max_idle_conns"`
	MaxOpenConns    *int           `cfg:"max_open_conns"`
	ConnMaxIdleTime *time.Duration `cfg:"conn_max_idle_time"`
}

type Sqlite struct {
	db *sql.DB
}

func New(ctx context.Context, cfg *Config) (*Sqlite, error) {
	dsn := DefaultDSN
	if cfg.DSN != "" {
		dsn = cfg.DSN
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	if err := db.PingContext(ctx); err != nil {
		return nil, err
	}

	if cfg.MaxIdleConns != nil {
		db.SetMaxIdleConns(*cfg.MaxIdleConns)
	} else {
		db.SetMaxIdleConns(DefaultMaxIdleConns)
	}

	if cfg.MaxOpenConns != nil {
		db.SetMaxOpenConns(*cfg.MaxOpenConns)
	} else {
		db.SetMaxOpenConns(DefaultMaxOpenConns)
	}

	if cfg.ConnMaxIdleTime != nil {
		db.SetConnMaxIdleTime(*cfg.ConnMaxIdleTime)
	} else {
		db.SetConnMaxIdleTime(DefaultConnMaxIdleTime)
	}

	return &Sqlite{db: db}, nil
}

func (m *Sqlite) Get(key string) ([]byte, error) {
	return nil, nil
}

func (m *Sqlite) Set(key string, value []byte) error {
	return nil
}

func (m *Sqlite) Close() error {
	return m.db.Close()
}
