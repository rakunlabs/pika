package service

import (
	"context"
	"errors"
	"time"
)

// Vault-specific errors. Returned by every vault service method so callers
// can distinguish the failure mode without parsing error strings. The HTTP
// layer (api.errorHandler) already maps ErrNotFound / ErrConflict /
// ErrBadRequest / ErrForbidden to the right status codes; the Vault*
// sentinels are layered on top of those to add domain context for the
// audit log and the SPA error toasts.
var (
	// ErrVaultNotInitialized means the user has no vault account row.
	// Returned by every items API path so the SPA can route the user to
	// the setup screen instead of leaking a 500 from the storage layer.
	ErrVaultNotInitialized = errors.New("vault: account not initialized")
	// ErrVaultAlreadyInitialized is returned when Setup is called on a
	// user that already has a vault row. Re-initialization is a
	// destructive operation (wipes every item because the encryption
	// key changes); the dedicated Reset path requires re-auth.
	ErrVaultAlreadyInitialized = errors.New("vault: account already initialized")
	// ErrVaultVersionConflict is returned by UpdateItem when the
	// supplied expected_version doesn't match the stored version. The
	// SPA refreshes and re-prompts the user; this is the optimistic-
	// concurrency story for concurrent edits across browser tabs.
	ErrVaultVersionConflict = errors.New("vault: item version conflict")
	// ErrVaultUnknownItemType is returned by CreateItem / UpdateItem
	// when the supplied type is not one of the known VaultItemType
	// values. The server keeps a fixed vocabulary so the UI can render
	// type-specific icons / labels; unknown types would break that.
	ErrVaultUnknownItemType = errors.New("vault: unknown item type")
)

// VaultItemType identifies the kind of item stored in a personal vault.
// The server treats the vocabulary as a fixed enum (rather than free
// text) so the UI can render type-specific icons, default field
// templates, and category filters without parsing arbitrary strings.
//
// Adding a new type requires:
//  1. a constant here,
//  2. an entry in KnownVaultItemTypes for /api/v1/info discovery,
//  3. a default-fields template on the frontend.
//
// Removing a type is a breaking change for any existing data — items
// stored with a removed type would be unreadable. Prefer leaving
// retired types in the list and hiding them from the new-item UI.
type VaultItemType string

const (
	VaultItemTypeLogin         VaultItemType = "login"
	VaultItemTypeCard          VaultItemType = "card"
	VaultItemTypeIdentity      VaultItemType = "identity"
	VaultItemTypeSecureNote    VaultItemType = "secure_note"
	VaultItemTypeSSHKey        VaultItemType = "ssh_key"
	VaultItemTypeAPICredential VaultItemType = "api_credential"
	VaultItemTypeDatabase      VaultItemType = "database"
	VaultItemTypeServer        VaultItemType = "server"
	VaultItemTypeLicense       VaultItemType = "license"
	VaultItemTypeTLSCert       VaultItemType = "tls_cert"
)

// KnownVaultItemTypes is the canonical list the UI consumes via
// /api/v1/info — keeps the SPA's "new item" picker in sync with the
// server's allowed vocabulary. The order here is the order shown in
// the picker.
var KnownVaultItemTypes = []VaultItemType{
	VaultItemTypeLogin,
	VaultItemTypeCard,
	VaultItemTypeIdentity,
	VaultItemTypeSecureNote,
	VaultItemTypeSSHKey,
	VaultItemTypeAPICredential,
	VaultItemTypeDatabase,
	VaultItemTypeServer,
	VaultItemTypeLicense,
	VaultItemTypeTLSCert,
}

// IsValid reports whether t is one of the KnownVaultItemTypes. Used by
// the service layer to reject unknown types at the create/update edge
// (server returns ErrVaultUnknownItemType — the SPA surfaces a toast).
func (t VaultItemType) IsValid() bool {
	for _, k := range KnownVaultItemTypes {
		if k == t {
			return true
		}
	}
	return false
}

// VaultKDFParams describes the Argon2id parameters used to derive the
// account key from the master password + secret key. Stored alongside
// the wrapped vault key so unlocking is deterministic across devices.
//
// All four fields together describe a single Argon2id invocation:
//
//	derived_key = Argon2id(password=master_password,
//	                       salt=secret_key || user_salt,
//	                       memory=Memory KiB,
//	                       iterations=Iterations,
//	                       parallelism=Parallelism,
//	                       length=32)
//
// The frontend implements this with libsodium's
// crypto_pwhash_argon2id (which expects memory in bytes — multiply
// Memory by 1024). Both Memory and Iterations are tunable so a future
// "fast preset" or "strong preset" can land without schema churn.
//
// Algorithm is the string "argon2id" today; reserved for forward
// compatibility if we ever swap to Argon2i or scrypt. Frontends MUST
// refuse to unlock when Algorithm is anything other than what they
// implement — never silently fall back, since a downgrade attack
// against this field would weaken the derivation.
type VaultKDFParams struct {
	Algorithm   string `json:"algorithm"`   // currently always "argon2id"
	Memory      int    `json:"memory"`      // KiB; OWASP recommends ≥19MiB, our default 64MiB
	Iterations  int    `json:"iterations"`  // OWASP recommends ≥2; our default 3
	Parallelism int    `json:"parallelism"` // OWASP recommends 1 for server-side parity; default 1
	Salt        []byte `json:"salt"`        // 16 bytes random, generated at Setup
}

// VaultAccount is the per-user crypto state for the personal vault.
// Exactly one row per user (keyed by UserID, mirroring UserTOTP).
//
// Why every field is persisted:
//
//   - SecretKeyHash is a SHA-256 (or BLAKE2b) of the user's Secret Key.
//     It is NOT a verification oracle for the master password — the
//     master password is verified implicitly by attempting to decrypt
//     WrappedVaultKey. SecretKeyHash exists so the server can reject
//     an unlock attempt that supplies a wrong Secret Key without
//     having to perform a costly Argon2id derivation first; it also
//     lets the recovery-kit endpoint confirm "this is the right
//     Secret Key" before regenerating the kit. Storing only a hash
//     (not the key itself) means a DB dump still doesn't reveal the
//     Secret Key.
//
//   - KDF describes the Argon2id parameters used at Setup. Stored so
//     unlocking on a second device can re-derive the same account
//     key without guessing parameters. Bumping KDF tuning later
//     requires a vault-key rotation (re-wrap with new params).
//
//   - WrappedVaultKey is the master vault key (used to encrypt every
//     item's payload) encrypted with the Argon2id-derived account
//     key. Server never sees the unwrapped form; the client unwraps
//     in memory and keeps it for the session lifetime.
//
//   - WrappedVaultKeyVersion is the format/version of the wrapping
//     envelope. Today only v1 is defined: XChaCha20-Poly1305 with a
//     24-byte nonce prepended to the ciphertext. Incrementing this
//     would let us migrate to a different AEAD without breaking
//     existing vaults — restore code switches on the version.
//
//   - RecoveryKitID is an opaque UUID stamped on the emergency kit
//     PDF/QR at Setup time. The endpoint that re-generates the kit
//     updates this ID; an out-of-band recovery attempt presenting an
//     old kit can be rejected if it doesn't match. Soft signal only
//     — the kit's value is the Secret Key it carries, not the ID.
//
//   - SessionLockSeconds is the user's preferred auto-lock TTL. The
//     SPA reads this and zeroizes the in-memory vault key after that
//     many seconds of inactivity. Stored on the account (not on
//     user_preferences) so it travels with the vault on backup.
//     Zero means "use the SPA default" (15 minutes today).
type VaultAccount struct {
	UserID                 string         `json:"user_id"`
	SecretKeyHash          []byte         `json:"-"` // never expose over API
	KDF                    VaultKDFParams `json:"kdf"`
	WrappedVaultKey        []byte         `json:"-"` // bytes are sensitive; surfaced only via Account()
	WrappedVaultKeyVersion int            `json:"wrapped_vault_key_version"`
	RecoveryKitID          string         `json:"recovery_kit_id,omitempty"`
	SessionLockSeconds     int            `json:"session_lock_seconds,omitempty"`
	CreatedAt              time.Time      `json:"created_at"`
	UpdatedAt              time.Time      `json:"updated_at"`
}

// VaultItem is one encrypted entry in a user's personal vault.
//
// What stays in cleartext on the server (and why):
//
//   - Type: required for the UI to render the right icon and field
//     template before decrypting. Also drives the type filter in
//     the list — operating server-side keeps that O(1) by index.
//     The vocabulary is a fixed enum of ten values, so leaking it
//     is closer to "schema metadata" than "user content".
//
//   - Favorite / Archived / DeletedAt / LastUsedAt: boolean and
//     nullable timestamp markers used by the list UI for sorting
//     and tab routing. Each individual flag leaks one bit at most;
//     keeping them cleartext lets the server stream a stable
//     ordering without forcing the client to decrypt every row.
//
//   - CreatedAt / UpdatedAt / Version: lifecycle metadata. The
//     timestamps expose access-pattern signal (the server admin
//     can tell that a user edited *something* at noon) but never
//     leak the content of the edit. Version is the OCC token.
//
// What is encrypted in EncryptedTitle / EncryptedTags /
// EncryptedHostnames / EncryptedPayload:
//
//   - The item's human-readable title.
//   - Every tag value (encrypted as a JSON array of strings).
//   - Every URL hostname (encrypted as a JSON array). The plan to
//     surface these to a future autofill / browser-extension path
//     will introduce a blind index alongside the ciphertext when
//     that work lands; today the field is fully opaque.
//   - Every field value, label, per-field type, notes, and any
//     TOTP seed (inside EncryptedPayload — see the SPA crypto for
//     the wire shape).
//
// Title encryption pushes pika onto par with 1Password / Bitwarden:
// a server admin (or a stolen DB dump) sees only opaque ciphertexts
// plus the fixed-enum type and a handful of lifecycle bits.
type VaultItem struct {
	ID                 string        `json:"id"`
	UserID             string        `json:"user_id"`
	Type               VaultItemType `json:"type"`
	EncryptedTitle     []byte        `json:"encrypted_title"`
	EncryptedTags      []byte        `json:"encrypted_tags,omitempty"`
	EncryptedHostnames []byte        `json:"encrypted_hostnames,omitempty"`
	// EncryptedFolder is the AEAD ciphertext of the user-chosen
	// folder name (e.g. "Personal", "Work"). Empty means "no
	// folder" — items default to the unfiltered list. We encrypt it
	// because folder names can leak intent ("Banking", "Affair") as
	// readily as item titles can. Kept as a flat string (single
	// level) for now; no parent/child hierarchy.
	EncryptedFolder  []byte     `json:"encrypted_folder,omitempty"`
	EncryptedPayload []byte     `json:"encrypted_payload"`
	Favorite         bool       `json:"favorite,omitempty"`
	Archived         bool       `json:"archived,omitempty"`
	DeletedAt        *time.Time `json:"deleted_at,omitempty"`
	LastUsedAt       *time.Time `json:"last_used_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	Version          int64      `json:"version"`
}

// VaultItemVersion is a snapshot of one prior revision of a vault item.
// Mirrors the file-versions design: every successful UpdateItem call
// archives the pre-update row here, so a user can roll back to "two
// edits ago" without leaving the trash window.
//
// The history is per-user — listing or restoring requires the same
// user as the item owner. The encrypted payload is preserved verbatim
// so restore is a plain byte copy back onto the live row.
//
// Versioning interacts with rotation: a re-wrap of the vault key does
// NOT re-encrypt history (history rows are not re-encrypted on every
// rotation — too expensive, infrequent benefit), so history older
// than the last rotation is readable only with that rotation's key.
// The rotation handler keeps the previous key in a server-side
// "old keys" list during a grace period for this reason.
//
// EncryptedTitle is the pre-update title's ciphertext — mirrors the
// live item's title encryption so a history entry never leaks the
// readable name even when the live item's title is rotated.
type VaultItemVersion struct {
	ItemID           string    `json:"item_id"`
	Version          int64     `json:"version"`
	EncryptedTitle   []byte    `json:"encrypted_title"`
	EncryptedPayload []byte    `json:"encrypted_payload"`
	UpdatedAt        time.Time `json:"updated_at"`
	Author           string    `json:"author,omitempty"`
}

// VaultAccountStorage manages per-user vault account records.
// Get / Set / Delete take a user_id since the table is one-to-one
// with users (same shape as UserTOTPStorage).
type VaultAccountStorage interface {
	Get(ctx context.Context, userID string) (*VaultAccount, error)
	Set(ctx context.Context, a *VaultAccount) error
	Delete(ctx context.Context, userID string) error
}

// VaultItemFilter narrows ListItems queries. Zero-value means "every
// active item belonging to the user" (active = not in trash). The
// service layer translates these into a bw query + a small in-memory
// post-filter for the boolean fields where bw indexing isn't worth
// the schema cost.
//
// Why separate flags for Archived / Trash rather than a single enum:
// the UI's left-rail has three tabs (Active, Archived, Trash) and the
// client passes exactly one at a time. Booleans match that mental
// model directly. Active = !Archived && DeletedAt == nil (default).
//
// Note on what's NOT here: there are no Tag or Search fields anymore.
// Item titles and tags are encrypted at rest, so the server cannot
// usefully evaluate a substring or equality predicate against them.
// The SPA decrypts after listing and runs free-text + tag filters
// in memory — fast enough for the personal-vault scale (~ low
// thousands of items per user).
type VaultItemFilter struct {
	// Type narrows to a single VaultItemType. Empty = all types.
	Type VaultItemType
	// FavoriteOnly limits results to Favorite=true items.
	FavoriteOnly bool
	// IncludeArchived returns archived items in addition to active.
	// Default false: archived items are hidden from the active view.
	IncludeArchived bool
	// ArchivedOnly returns ONLY archived items (mutually exclusive
	// with IncludeArchived; server uses ArchivedOnly first if set).
	ArchivedOnly bool
	// TrashOnly returns soft-deleted items only. Used by the trash
	// tab. Mutually exclusive with the active-set flags above.
	TrashOnly bool
}

// VaultItemStorage manages encrypted vault items. Lookups are scoped
// to a user — every method takes a userID and the storage enforces
// that the returned row belongs to that user (defense-in-depth
// against an attacker who guesses an item id from a different user).
type VaultItemStorage interface {
	Create(ctx context.Context, item *VaultItem) error
	Get(ctx context.Context, userID, itemID string) (*VaultItem, error)
	List(ctx context.Context, userID string, filter VaultItemFilter) ([]VaultItem, error)
	Update(ctx context.Context, item *VaultItem) error
	Delete(ctx context.Context, userID, itemID string) error
	// DeleteAllByUser drops every item for a user. Used by the
	// user-delete cascade and by the vault Reset path.
	DeleteAllByUser(ctx context.Context, userID string) error
	// Count returns the total number of items (regardless of
	// filter) belonging to a user. Used by the vault status
	// endpoint so the SPA can render "X items".
	Count(ctx context.Context, userID string) (int64, error)
}

// VaultItemVersionStorage manages the per-item revision history.
// History is append-only from the service layer: every UpdateItem
// inserts one row. Deletes happen only via DeleteAllByItem (when an
// item is purged) or DeleteAllByUser (when the account is reset).
type VaultItemVersionStorage interface {
	// Append records a new history entry. The pair (item_id, version)
	// is unique; re-appending the same version errors with
	// ErrConflict so a buggy write loop can't silently overwrite an
	// existing snapshot.
	Append(ctx context.Context, v *VaultItemVersion) error
	// ListByItem returns every snapshot for an item, newest first.
	ListByItem(ctx context.Context, itemID string) ([]VaultItemVersion, error)
	// Get returns the snapshot at a specific (item, version). Used
	// by the restore endpoint.
	Get(ctx context.Context, itemID string, version int64) (*VaultItemVersion, error)
	// DeleteAllByItem drops every snapshot for an item. Called by
	// hard-delete on an item.
	DeleteAllByItem(ctx context.Context, itemID string) error
	// DeleteAllByUser drops every snapshot belonging to a user.
	// Called by the user-delete cascade and the vault Reset path.
	DeleteAllByUser(ctx context.Context, userID string) error
}
