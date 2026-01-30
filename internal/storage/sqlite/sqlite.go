package sqlite

import (
	"context"
	"database/sql"
	"time"

	_ "modernc.org/sqlite"
)

var (
	DefaultDSN             = "file:pika.db?cache=shared"
	DefaultMaxIdleConns    = 3
	DefaultMaxOpenConns    = 5
	DefaultConnMaxIdleTime = 15 * time.Minute
)

type Config struct {
	DSN string `cfg:"dsn"`

	MaxIdleConns    *int           `cfg:"max_idle_conns"`
	MaxOpenConns    *int           `cfg:"max_open_conns"`
	ConnMaxIdleTime *time.Duration `cfg:"conn_max_idle_time"`
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
}

func New(ctx context.Context, cfg *Config) (*Sqlite, error) {
	c := cfg.GetConfig()

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
