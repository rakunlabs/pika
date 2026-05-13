// Package bw provides a service.Storage implementation backed by
// rakunlabs/bw (a typed BadgerDB wrapper).
//
// The previous SQLite backend modeled junction tables (permission_keys,
// user_permissions, permission_key_patterns) as separate tables joined on
// each lookup. bw is a key/value store with no joins, so we collapse those
// junctions into embedded slices on the entity row that owns them:
//
//   - Permission.Keys / Permission.KeyPatterns  (already on the service
//     model — was already denormalized at the service-API edge)
//   - User.Grants                               (added internally — a
//     []userGrant slice on the userRow stored in the bucket; surfaced to
//     the service layer via the PermissionStorage methods).
//
// Tx semantics: bw exposes db.Update / db.View transactions that wrap a
// *badger.Txn. Each entity's storage methods accept either the long-lived
// *bw.Bucket or a per-tx *bw.Tx. We funnel both into a thin txCtx that the
// per-bucket helpers use to read/write under the right scope.
package bw

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/rakunlabs/bw"
	"github.com/rakunlabs/pika/internal/service"
)

// DefaultPath is the on-disk directory used when no path is configured.
// Badger creates a directory tree here, so it must be writable.
var DefaultPath = "data/pika"

// Config configures the bw-backed storage.
type Config struct {
	// Enabled gates the backend. Defaults to true.
	Enabled bool `cfg:"enabled" default:"true"`
	// Path is the on-disk directory for Badger files.
	Path string `cfg:"path"`
	// InMemory runs entirely in memory — useful for tests and CI.
	InMemory bool `cfg:"in_memory"`
}

func (c *Config) effectivePath() string {
	if c.Path != "" {
		return c.Path
	}
	return DefaultPath
}

// Storage implements service.Storage on top of *bw.DB.
type Storage struct {
	db *bw.DB

	users             *bw.Bucket[userRow]
	userIdentities    *bw.Bucket[userIdentityRow]
	tokens            *bw.Bucket[tokenRow]
	sessions          *bw.Bucket[sessionRow]
	permissions       *bw.Bucket[permissionRow]
	folders           *bw.Bucket[folderRow]
	files             *bw.Bucket[fileRow]
	fileVersions      *bw.Bucket[fileVersionRow]
	settings          *bw.Bucket[settingsRow]
	userPreferences   *bw.Bucket[userPreferencesRow]
	passkeys          *bw.Bucket[passkeyCredentialRow]
	passkeyChallenges *bw.Bucket[passkeyChallengeRow]
	userTOTP          *bw.Bucket[userTOTPRow]
	vaultAccounts     *bw.Bucket[vaultAccountRow]
	vaultItems        *bw.Bucket[vaultItemRow]
	vaultItemVersions *bw.Bucket[vaultItemVersionRow]
}

// New opens (or creates) a bw database at the configured path and
// registers every bucket. The on-disk directory is created if missing.
func New(ctx context.Context, cfg *Config) (*Storage, error) {
	_ = ctx

	var opts []bw.Option
	if cfg.InMemory {
		opts = append(opts, bw.WithInMemory(true))
	}

	path := cfg.effectivePath()
	if !cfg.InMemory {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(path, 0o755); err != nil {
			return nil, err
		}
	}

	db, err := bw.Open(path, opts...)
	if err != nil {
		return nil, err
	}

	s := &Storage{db: db}

	// Pre-register migrations run on the raw Badger db while no bw
	// bucket handle yet exists for the affected names. The vault v2
	// wipe (see migrate_vault.go) lives here so that the v1 cleartext
	// row blobs are gone before bw's auto-migrate adjusts indexes
	// against the new schema; otherwise bw would happily keep the
	// stale msgpack blobs underneath the new v2 fingerprint.
	if err := MigrateVaultV2(ctx, s); err != nil {
		_ = db.Close()
		return nil, err
	}

	if err := s.registerBuckets(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// DB returns the underlying *bw.DB. Exposed for backup/restore wiring.
func (s *Storage) DB() *bw.DB { return s.db }

// Close closes the underlying database.
func (s *Storage) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// translateErr maps bw's sentinel errors to the service-layer ones.
func translateErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, bw.ErrNotFound):
		return service.ErrNotFound
	case errors.Is(err, bw.ErrConflict):
		return service.ErrConflict
	default:
		return err
	}
}

// nowUTC returns the current time truncated to microseconds — enough
// resolution for our audit fields, and avoids the noise of Go's full
// nanoseconds when round-tripped through msgpack.
func nowUTC() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }
