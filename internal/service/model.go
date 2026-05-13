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
	UserPreferences() UserPreferencesStorage
	Passkeys() PasskeyStorage
	PasskeyChallenges() PasskeyChallengeStorage
	UserTOTPs() UserTOTPStorage
	VaultAccounts() VaultAccountStorage
	VaultItems() VaultItemStorage
	VaultItemVersions() VaultItemVersionStorage

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
	// ListUserIDsByPermission returns the IDs of every user that has been
	// granted the given permission bundle (regardless of grant source).
	// Used by the admin UI to filter the user list by permission. Returns
	// an empty slice (not nil) when no users hold the permission.
	ListUserIDsByPermission(ctx context.Context, permissionID string) ([]string, error)
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

// EditorPreferences captures the user-facing CodeMirror configuration that
// is persisted server-side so the same look-and-feel follows the user
// across devices.
type EditorPreferences struct {
	Theme      string `json:"theme"`
	FontSize   int    `json:"font_size"`
	FontFamily string `json:"font_family"`
	LineWrap   bool   `json:"line_wrap"`
}

// AppPreferences captures application-wide UI preferences (independent
// from the editor theme).
type AppPreferences struct {
	// Theme is one of "light", "dark", or "system".
	Theme string `json:"theme"`
}

// PanelPreferences captures persisted layout sizes for resizable panels.
type PanelPreferences struct {
	LeftWidth  int `json:"left_width"`
	RightWidth int `json:"right_width"`
}

// UserPreferences is the per-user UI preference record. It is intentionally
// modeled as a single JSON-blob style document keyed by user ID so future
// additions (e.g. a personal password vault) are purely additive without
// schema churn.
type UserPreferences struct {
	UserID    string            `json:"user_id"`
	App       AppPreferences    `json:"app"`
	Editor    EditorPreferences `json:"editor"`
	Panels    PanelPreferences  `json:"panels"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// UserPreferencesStorage manages per-user preference documents. Get
// returns ErrNotFound when no document exists for the given user — the
// service layer is responsible for substituting defaults.
type UserPreferencesStorage interface {
	Get(ctx context.Context, userID string) (*UserPreferences, error)
	Set(ctx context.Context, prefs *UserPreferences) error
	Delete(ctx context.Context, userID string) error
}

// PasskeyCredential is one WebAuthn credential bound to a pika user.
//
// Why we persist every field:
//   - CredentialID is the lookup key at login (the authenticator
//     returns it as rawId in the assertion response). Indexed and
//     unique so a passkey-login assertion can find its owning row in
//     O(1) without scanning every user's enrollments.
//   - PublicKey is the raw CBOR COSE_Key; we re-parse it on every
//     verification (cheap) instead of caching a decoded form, so a
//     future algorithm addition can re-decode without schema churn.
//   - AAGUID identifies the authenticator model (useful for showing
//     "YubiKey 5" or "iCloud Keychain" in the security UI; mostly
//     informational because pika does not enforce attestation policy).
//   - SignCount is updated after every successful login. A counter
//     that goes backwards across logins indicates a possibly-cloned
//     authenticator and the next login is rejected — see
//     ada/passkey/ceremony.go FinishLogin.
//   - BackupEligible/BackupState track whether the credential can be
//     synced across devices (set on syncable passkeys like iCloud
//     Keychain). Surfaced in the UI so users understand which
//     credentials follow them.
//   - Name is a user-supplied label ("Phone", "YubiKey 5"). Pika
//     auto-generates a placeholder ("Passkey 1", "Passkey 2") on
//     enroll if missing — better than blank rows.
type PasskeyCredential struct {
	ID              string    `json:"id"`
	UserID          string    `json:"user_id"`
	CredentialID    []byte    `json:"credential_id"`
	PublicKey       []byte    `json:"-"` // never expose raw key over API
	AAGUID          []byte    `json:"aaguid,omitempty"`
	SignCount       uint32    `json:"sign_count"`
	Transports      []string  `json:"transports,omitempty"`
	UserVerified    bool      `json:"user_verified"`
	BackupEligible  bool      `json:"backup_eligible"`
	BackupState     bool      `json:"backup_state"`
	AttestationType string    `json:"attestation_type,omitempty"`
	Name            string    `json:"name"`
	CreatedAt       time.Time `json:"created_at"`
	LastUsedAt      time.Time `json:"last_used_at,omitempty"`
}

// PasskeyStorage manages WebAuthn credential records. Per-user
// enumeration (security page listing) goes through ListByUserID;
// per-credential lookup at login goes through FindByCredentialID
// (the authenticator only returns the credential id — the owning
// user is discovered from the row).
type PasskeyStorage interface {
	Create(ctx context.Context, c *PasskeyCredential) error
	Get(ctx context.Context, id string) (*PasskeyCredential, error)
	FindByCredentialID(ctx context.Context, credentialID []byte) (*PasskeyCredential, error)
	ListByUserID(ctx context.Context, userID string) ([]PasskeyCredential, error)
	Update(ctx context.Context, c *PasskeyCredential) error
	Delete(ctx context.Context, id string) error
	DeleteByUserID(ctx context.Context, userID string) error
}

// PasskeyChallenge is one in-flight WebAuthn ceremony session. Both
// enrollment and assertion ceremonies use this row: enrollment sets
// UserID (the user the ceremony belongs to); assertion login leaves
// it empty (the user isn't known until the rawId comes back at
// finish time).
//
// We persist the SessionData blob verbatim (JSON-serialized
// ada/passkey.SessionData) rather than splitting its fields onto
// columns. The struct's shape is owned by the ada library and we
// want passkey ceremonies to keep working across ada upgrades that
// add new fields — JSON gives us forward/backward compatibility for
// free, at the cost of one indexed lookup per round trip.
//
// Kind tags the row so the security UI / audit log can tell enroll
// challenges apart from login challenges without round-tripping
// through the JSON blob. It is not part of the lookup key — the
// session id alone is unique by construction (32-byte crypto/rand).
//
// ExpiresAt is indexed so the periodic sweep can range-seek expired
// rows without scanning the whole bucket.
type PasskeyChallenge struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`              // "enroll" | "login"
	UserID    string    `json:"user_id,omitempty"` // enroll only
	Data      []byte    `json:"-"`                 // JSON-encoded passkey.SessionData; opaque to the service layer
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// PasskeyChallengeStorage persists short-lived WebAuthn ceremony
// state. In a multi-instance pika deployment the begin and finish
// requests may land on different nodes; this bucket (replicated
// through the bw cluster) is what makes that work without sticky
// sessions.
//
// Save/Get/Delete map one-to-one onto the ada/passkey.ChallengeStore
// interface; the kind/userID fields are extra metadata for audit and
// for the enrollment service's cross-user-smuggling check
// (PasskeyService.FinishEnroll rejects a session whose userID
// doesn't match the caller).
type PasskeyChallengeStorage interface {
	Save(ctx context.Context, c *PasskeyChallenge) error
	Get(ctx context.Context, id string) (*PasskeyChallenge, error)
	Delete(ctx context.Context, id string) error
	// DeleteExpired removes every row whose ExpiresAt is in the past.
	// Returns the number deleted so callers can log sweep activity.
	DeleteExpired(ctx context.Context) (int, error)
}

// UserTOTP holds the persisted state for a single user's TOTP second
// factor. One row per user (keyed by user_id) — TOTP isn't multi-device
// like passkeys; a user enrolls one authenticator and shares the
// secret with whichever apps they want to use.
//
// Secret is base32-encoded (the canonical form authenticator apps
// emit/accept). It is the actual HMAC key — anyone with the secret
// can generate valid codes. The row is marked json:"-" so it never
// leaks through the API; the service layer also blanks the field on
// every read except the brief enrollment window.
//
// RecoveryCodes are bcrypt hashes (NOT plaintext). The plaintext
// codes are shown to the user exactly once at enrollment / regenerate
// time; lost codes are not recoverable. On a successful login with a
// recovery code the corresponding hash is removed from the slice
// (single-use semantics).
//
// Enabled separates "user started enrollment but hasn't confirmed the
// first code yet" (Enabled=false, row exists but doesn't gate login)
// from "TOTP is live for this user" (Enabled=true, every login goes
// through step-up). The two-step enrollment guards against the user
// scanning the QR, closing the browser, and locking themselves out
// because they couldn't prove they actually have the secret.
type UserTOTP struct {
	UserID        string    `json:"user_id"`
	Secret        string    `json:"-"` // base32; never expose over API
	Enabled       bool      `json:"enabled"`
	RecoveryCodes []string  `json:"-"` // bcrypt hashes; never expose
	CreatedAt     time.Time `json:"created_at"`
	LastUsedAt    time.Time `json:"last_used_at,omitempty"`
}

// UserTOTPStorage manages the per-user TOTP state. One row per user;
// Get/Delete take a user_id (not a synthetic primary key) since TOTP
// is a one-to-one with users. Update and Set are kept distinct so the
// service layer can express "insert new enrollment" vs "patch
// existing" intent.
type UserTOTPStorage interface {
	Get(ctx context.Context, userID string) (*UserTOTP, error)
	Set(ctx context.Context, t *UserTOTP) error
	Delete(ctx context.Context, userID string) error
}

// Folder represents a directory containing folders and files.
type Folder struct {
	Folders  []string            `json:"folders"`
	Files    []string            `json:"files"`
	Variants map[string][]string `json:"variants,omitempty"` // file name -> variant keys
}

// SearchResult represents a single search match. Only the path is exposed
// so file contents never leak through search results.
type SearchResult struct {
	Path string `json:"path"` // config path
	Type string `json:"type"` // "name" or "content"
}

// SearchOptions controls how Service.Search walks the tree. Kept as a
// struct (rather than positional params) so we can add knobs like
// MaxResults or IncludeVariants later without touching every caller.
type SearchOptions struct {
	// Query is the substring to look for (case-insensitive). Empty Query
	// is treated as a no-op by Search.
	Query string

	// NameOnly skips reading file contents entirely. This is both faster
	// (no I/O per file) and safer (file bytes never leave storage), at
	// the cost of missing matches that only appear inside files. Useful
	// when the caller only needs to locate a config by path.
	NameOnly bool
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
