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

// Querier abstracts *sql.DB and *sql.Tx for shared query execution.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

// Sqlite implements the service.Storage interface using SQLite
// with proper SQL tables for each entity.
type Sqlite struct {
	db *sql.DB
	q  Querier
}

func New(ctx context.Context, cfg *Config) (*Sqlite, error) {
	c := cfg.GetConfig()

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

func (s *Sqlite) DB() *sql.DB {
	return s.db
}

func (s *Sqlite) Close() error {
	return s.db.Close()
}

func (s *Sqlite) Users() service.UserStorage               { return &userStorage{q: s.q} }
func (s *Sqlite) Tokens() service.TokenStorage             { return &tokenStorage{q: s.q} }
func (s *Sqlite) Sessions() service.SessionStorage         { return &sessionStorage{q: s.q} }
func (s *Sqlite) Folders() service.FolderStorage           { return &folderStorage{q: s.q} }
func (s *Sqlite) Files() service.FileStorage               { return &fileStorage{q: s.q} }
func (s *Sqlite) FileVersions() service.FileVersionStorage { return &fileVersionStorage{q: s.q} }
func (s *Sqlite) Settings() service.SettingsStorage        { return &settingsStorage{q: s.q} }

func (s *Sqlite) Tx(ctx context.Context, fn func(ctx context.Context, tx service.Storage) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	sqliteTx := &Sqlite{db: s.db, q: tx}

	if err := fn(ctx, sqliteTx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return errors.Join(err, rbErr)
		}
		return err
	}

	return tx.Commit()
}
