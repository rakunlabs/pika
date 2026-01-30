package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"log/slog"

	"github.com/rakunlabs/muz"
)

//go:embed migrations/*
var migrationsFS embed.FS

func migration(ctx context.Context, dsn string) error {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	m := muz.Migrate{
		Path:      "migrations", // directory inside the FS
		FS:        migrationsFS, // optional: if not set, uses os.DirFS
		Extension: ".sql",       // optional: default not set and supports all files
	}

	driver := &muz.SQLDriver{
		DB:      db,
		Dialect: muz.DialectSQLite,
		Table:   "pika_migrations", // migration tracking table name
		Logger:  slog.Default(),    // optional: logger instance
	}

	if err := m.Migrate(ctx, driver); err != nil {
		return err
	}

	return nil
}
