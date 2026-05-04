package service

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/rakunlabs/query"
)

var (
	ErrNotFound         = errors.New("not found")
	ErrBadRequest       = errors.New("bad request")
	ErrNoStorageBackend = errors.New("no storage backend configured")
	ErrUnauthorized     = errors.New("unauthorized")
	ErrForbidden        = errors.New("forbidden")
	ErrConflict         = errors.New("conflict")
)

// Storage defines the top-level storage interface.
// Each entity has its own typed repository.
type Storage interface {
	Users() UserStorage
	UserIdentities() UserIdentityStorage
	Tokens() TokenStorage
	Sessions() SessionStorage
	Permissions() PermissionStorage
	Folders() FolderStorage
	Files() FileStorage
	FileVersions() FileVersionStorage
	Settings() SettingsStorage

	// Tx executes a function within a transaction.
	// If the function returns an error, the transaction is rolled back.
	Tx(ctx context.Context, fn func(ctx context.Context, tx Storage) error) error

	// Backup writes a streaming backup of the entire database to w.
	// since=0 produces a full backup; non-zero values produce an
	// incremental backup including only entries newer than the given
	// version. The format is backend-specific (currently Badger's
	// streaming format) and not interchangeable across major versions
	// of the storage backend. Returns the latest version included in
	// the backup so a follow-up incremental can chain off it.
	Backup(w io.Writer, since uint64) (uint64, error)

	// BackupUntil writes a point-in-time backup containing only entries
	// whose internal version is ≤ until. Useful for restoring the
	// database to the exact state it had at a known checkpoint.
	BackupUntil(w io.Writer, until uint64) (uint64, error)

	// Restore replays a backup stream into the database. Restore is an
	// upsert: existing keys are overwritten where they overlap with the
	// stream, but keys absent from the stream are NOT removed. Callers
	// that want a true "replace" must call Wipe first.
	Restore(r io.Reader) error

	// Wipe removes every key from the database in one operation —
	// data, indexes, unique reservations, and bw schema metadata.
	// After Wipe the in-process bucket handles still work; the next
	// write repopulates whatever bw needs. This is destructive and
	// irreversible; callers gating it behind a confirmation prompt
	// is strongly recommended.
	Wipe() error

	// Version returns the current monotonic transaction version of the
	// database. This number is what callers pass to Backup(since=…) and
	// BackupUntil(until=…). The value increases on every write tx
	// commit.
	Version() uint64
}

// UserStorage manages user records.
type UserStorage interface {
	Create(ctx context.Context, user *User) error
	Get(ctx context.Context, id string) (*User, error)
	GetByUsername(ctx context.Context, username string) (*User, error)
	// GetByEmail looks up a user by their canonical email. Returns
	// ErrNotFound when no user has that email or when email is empty.
	// Used for auto-linking an external identity to an existing local
	// user when AuthSettings.LinkByVerifiedEmail is true.
	GetByEmail(ctx context.Context, email string) (*User, error)
	List(ctx context.Context, q *query.Query) ([]User, int64, error)
	Update(ctx context.Context, user *User) error
	Delete(ctx context.Context, id string) error
	Count(ctx context.Context) (int64, error)
}

// UserIdentity is a third-party credential attached to a pika user.
// One user can have many identities (e.g. local + Google + GitHub) and
// any identity resolves back to the same user_id so permissions and
// admin operations are unified. Local (password) auth does NOT produce a
// UserIdentity row — the users.password_hash column is the credential.
type UserIdentity struct {
	ID          string     `json:"id"`
	UserID      string     `json:"user_id"`
	Provider    string     `json:"provider"`               // e.g. "google", "github", "keycloak"
	Subject     string     `json:"subject"`                // provider's stable user ID (OIDC sub)
	Email       string     `json:"email,omitempty"`        // snapshot at last login
	DisplayName string     `json:"display_name,omitempty"` // snapshot at last login
	CreatedAt   time.Time  `json:"created_at"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
}

// UserIdentityStorage manages external identity links. (provider, subject)
// is globally unique; the store enforces that at the DB level.
type UserIdentityStorage interface {
	// Upsert creates or updates the (provider, subject) row. If the pair
	// is new, CreatedAt is set; if it exists, Email/DisplayName/
	// LastLoginAt are refreshed. Returns the resulting row.
	Upsert(ctx context.Context, identity *UserIdentity) (*UserIdentity, error)
	// FindByProviderSubject returns the existing link or ErrNotFound.
	FindByProviderSubject(ctx context.Context, provider, subject string) (*UserIdentity, error)
	// ListByUserID returns every identity linked to a user, oldest first.
	ListByUserID(ctx context.Context, userID string) ([]UserIdentity, error)
	// ListByProvider returns every identity issued by a given provider,
	// oldest first. Used by the user-sync reconciliation pass to find
	// previously-synced users that the directory no longer returns.
	ListByProvider(ctx context.Context, provider string) ([]UserIdentity, error)
	// Delete removes a single (provider, subject) link. Used for admin
	// "unlink" operations.
	Delete(ctx context.Context, id string) error
	// DeleteByUserID removes every identity for a user (used when the
	// user row is deleted; FK CASCADE handles this too but the explicit
	// method keeps the code symmetric with other stores).
	DeleteByUserID(ctx context.Context, userID string) error
}

// Session represents an active user session stored in the database.
type Session struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Username  string    `json:"username"`
	Payload   []byte    `json:"payload,omitempty"`
	RefreshID string    `json:"refresh_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// SessionStorage manages user sessions.
type SessionStorage interface {
	Create(ctx context.Context, session *Session) error
	Get(ctx context.Context, id string) (*Session, error)
	Delete(ctx context.Context, id string) error
	DeleteByUserID(ctx context.Context, userID string) error
	DeleteExpired(ctx context.Context) error
	CountByUserID(ctx context.Context, userID string) (int64, error)
}

// Permission represents a defined permission in the system.
// A permission bundles one or more capability keys (e.g. "files.read", "files.write")
// under a single assignable name.
//
// KeyPatterns optionally narrows each granted capability key to one or more
// doublestar glob patterns. A capability key with no entry in KeyPatterns
// (or with an empty slice) is unrestricted — matches any path. A non-empty
// slice means: this grant only applies to paths matching one of the
// listed patterns.
type Permission struct {
	ID          string              `json:"id"`
	Key         string              `json:"key"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Keys        []string            `json:"keys"`
	KeyPatterns map[string][]string `json:"key_patterns,omitempty"`
	CreatedAt   time.Time           `json:"created_at"`
}

// PermissionStorage manages permissions and user-permission assignments.
type PermissionStorage interface {
	Create(ctx context.Context, perm *Permission) error
	Get(ctx context.Context, id string) (*Permission, error)
	List(ctx context.Context) ([]Permission, error)
	Update(ctx context.Context, perm *Permission) error
	Delete(ctx context.Context, id string) error
	SetPermissionKeys(ctx context.Context, permissionID string, keys []string) error
	// SetPermissionKeyPatterns replaces the pattern list for a single
	// (permission, key) grant. The (permission, key) pair must already
	// exist in permission_keys. Passing an empty slice clears all
	// patterns (grant becomes unrestricted again).
	SetPermissionKeyPatterns(ctx context.Context, permissionID, key string, patterns []string) error
	// SetUserPermissions replaces every assignment for a user, regardless
	// of source. Use SetUserPermissionsBySource for sync-engine rewrites
	// that must leave 'local' (admin-curated) rows alone.
	SetUserPermissions(ctx context.Context, userID string, permissionIDs []string) error
	// SetUserPermissionsBySource replaces only the rows whose source
	// matches the given tag. Rows with other source values are preserved.
	// Used by the user-sync engine: it owns rows tagged with its source
	// ID, and must never delete rows tagged 'local'.
	SetUserPermissionsBySource(ctx context.Context, userID, source string, permissionIDs []string) error
	GetUserPermissions(ctx context.Context, userID string) ([]Permission, error)
	// GetUserCapabilityKeys returns the deduplicated set of capability keys
	// granted to a user through all their assigned permissions.
	GetUserCapabilityKeys(ctx context.Context, userID string) ([]string, error)
	// HasCapabilityKey checks if any permission in the system grants the given capability key.
	HasCapabilityKey(ctx context.Context, key string) (bool, error)
	// UserHasCapability checks if a user has been granted the given capability key
	// through any of their assigned permissions.
	UserHasCapability(ctx context.Context, userID string, key string) (bool, error)
}

// TokenStorage manages access tokens.
type TokenStorage interface {
	Create(ctx context.Context, token *Token) error
	Get(ctx context.Context, id string) (*Token, error)
	FindByHash(ctx context.Context, hashedKey string) (*Token, error)
	List(ctx context.Context, q *query.Query) ([]Token, int64, error)
	Update(ctx context.Context, token *Token) error
	Delete(ctx context.Context, id string) error
}

// FolderEntry represents a single folder record for backup/export.
type FolderEntry struct {
	Path   string  `json:"path"`
	Folder *Folder `json:"folder"`
}

// FolderStorage manages folder records.
type FolderStorage interface {
	Get(ctx context.Context, path string) (*Folder, error)
	Set(ctx context.Context, path string, folder *Folder) error
	Delete(ctx context.Context, path string) error
	DeletePrefix(ctx context.Context, prefix string) error
	List(ctx context.Context) ([]FolderEntry, error)
	DeleteAll(ctx context.Context) error
}

// FileEntry represents a single file record (path + version) for backup/export.
type FileEntry struct {
	Path    string `json:"path"`
	Version int64  `json:"version"`
	File    *File  `json:"file"`
}

// FileStorage manages file content at specific versions.
type FileStorage interface {
	Get(ctx context.Context, path string, version int64) (*File, error)
	Set(ctx context.Context, path string, version int64, file *File) error
	Delete(ctx context.Context, path string, version int64) error
	DeleteAllVersions(ctx context.Context, path string) error
	DeletePrefix(ctx context.Context, prefix string) error
	List(ctx context.Context) ([]FileEntry, error)
	DeleteAll(ctx context.Context) error
}

// FileVersionEntry represents a single file version record for backup/export.
type FileVersionEntry struct {
	Path     string       `json:"path"`
	Versions FileVersions `json:"versions"`
}

// FileVersionStorage manages file version metadata.
type FileVersionStorage interface {
	Get(ctx context.Context, path string) (FileVersions, error)
	Set(ctx context.Context, path string, versions FileVersions) error
	Delete(ctx context.Context, path string) error
	DeletePrefix(ctx context.Context, prefix string) error
	List(ctx context.Context) ([]FileVersionEntry, error)
	DeleteAll(ctx context.Context) error
}

// SettingsStorage manages application settings (singleton).
type SettingsStorage interface {
	Get(ctx context.Context) (*Settings, error)
	Set(ctx context.Context, settings *Settings) error
}

// Folder represents a directory containing folders and files.
type Folder struct {
	Folders  []string            `json:"folders"`
	Files    []string            `json:"files"`
	Variants map[string][]string `json:"variants,omitempty"` // file name -> variant keys
}

// SearchResult represents a single search match.
type SearchResult struct {
	Path    string `json:"path"`              // config path
	Type    string `json:"type"`              // "name" or "content"
	Line    int    `json:"line,omitempty"`    // line number (content match)
	Snippet string `json:"snippet,omitempty"` // matching line text (content match)
}

// DataResult holds the resolved configuration data returned by GetData.
type DataResult struct {
	Data   []byte `json:"data"`
	Format string `json:"format"`
	Error  string `json:"error,omitempty"` // parse/conversion error message
}

// RenderResult holds the rendered configuration for preview.
type RenderResult struct {
	Data  string `json:"data"`            // base64 encoded
	Error string `json:"error,omitempty"` // parse/conversion error message for the UI
}
