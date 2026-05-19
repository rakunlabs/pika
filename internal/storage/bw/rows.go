package bw

import (
	"time"

	"github.com/rakunlabs/pika/internal/external"
	"github.com/rakunlabs/pika/internal/hook"
	"github.com/rakunlabs/pika/internal/service"
)

// userGrant denormalizes one row of the SQL user_permissions table onto
// the user record that owns it. The (PermissionID, Source) pair is unique
// per user: the same permission may be granted by multiple sources
// (admin-curated 'local' and a user-sync source) without collapsing.
type userGrant struct {
	PermissionID string `bw:"permission_id"`
	Source       string `bw:"source"`
}

// userRow is the bucket payload for a single user. Username carries the
// `unique` flag — bw rejects duplicate username inserts with ErrConflict.
//
// Email is intentionally NOT marked unique here. The SQLite layer used a
// partial unique index (`WHERE email IS NOT NULL AND email != ”`) so
// rows with empty email could coexist; bw has no equivalent and would
// reject every second row with email="". For non-empty emails the
// service layer enforces uniqueness explicitly at the create/update
// path (Service.CreateUser, Service.UpdateUser), preserving the
// guarantees relied on by LinkByVerifiedEmail.
type userRow struct {
	ID           string      `bw:"id,pk"`
	Username     string      `bw:"username,unique"`
	Email        string      `bw:"email,index"`
	PasswordHash string      `bw:"password_hash"`
	DisplayName  string      `bw:"display_name"`
	External     bool        `bw:"external"`
	Disabled     bool        `bw:"disabled"`
	IsSuperadmin bool        `bw:"is_superadmin"`
	CreatedAt    time.Time   `bw:"created_at,index"`
	UpdatedAt    time.Time   `bw:"updated_at"`
	Grants       []userGrant `bw:"grants"`
}

func (r *userRow) toService() *service.User {
	return &service.User{
		ID:           r.ID,
		Username:     r.Username,
		PasswordHash: r.PasswordHash,
		Email:        r.Email,
		DisplayName:  r.DisplayName,
		External:     r.External,
		Disabled:     r.Disabled,
		IsSuperadmin: r.IsSuperadmin,
		CreatedAt:    r.CreatedAt,
		UpdatedAt:    r.UpdatedAt,
	}
}

func userRowFromService(u *service.User, grants []userGrant) *userRow {
	return &userRow{
		ID:           u.ID,
		Username:     u.Username,
		Email:        u.Email,
		PasswordHash: u.PasswordHash,
		DisplayName:  u.DisplayName,
		External:     u.External,
		Disabled:     u.Disabled,
		IsSuperadmin: u.IsSuperadmin,
		CreatedAt:    u.CreatedAt,
		UpdatedAt:    u.UpdatedAt,
		Grants:       grants,
	}
}

// userIdentityRow stores one OAuth/LDAP/Header link. (Provider, Subject)
// is enforced unique through a composite group named "providersubject".
type userIdentityRow struct {
	ID          string     `bw:"id,pk"`
	UserID      string     `bw:"user_id,index"`
	Provider    string     `bw:"provider,index,unique:providersubject"`
	Subject     string     `bw:"subject,unique:providersubject"`
	Email       string     `bw:"email"`
	DisplayName string     `bw:"display_name"`
	CreatedAt   time.Time  `bw:"created_at"`
	LastLoginAt *time.Time `bw:"last_login_at"`
}

func (r *userIdentityRow) toService() *service.UserIdentity {
	return &service.UserIdentity{
		ID:          r.ID,
		UserID:      r.UserID,
		Provider:    r.Provider,
		Subject:     r.Subject,
		Email:       r.Email,
		DisplayName: r.DisplayName,
		CreatedAt:   r.CreatedAt,
		LastLoginAt: r.LastLoginAt,
	}
}

func userIdentityRowFromService(id *service.UserIdentity) *userIdentityRow {
	return &userIdentityRow{
		ID:          id.ID,
		UserID:      id.UserID,
		Provider:    id.Provider,
		Subject:     id.Subject,
		Email:       id.Email,
		DisplayName: id.DisplayName,
		CreatedAt:   id.CreatedAt,
		LastLoginAt: id.LastLoginAt,
	}
}

// tokenRow is a single API access token. HashedKey is unique because it
// is the index used by FindByHash for every authenticated request — and
// duplicates would be a security bug, not an inconvenience.
type tokenRow struct {
	ID        string               `bw:"id,pk"`
	Name      string               `bw:"name"`
	HashedKey string               `bw:"hashed_key,unique"`
	Scopes    []service.TokenScope `bw:"scopes"`
	CreatedAt time.Time            `bw:"created_at,index"`
	CreatedBy string               `bw:"created_by"`
	ExpiresAt *time.Time           `bw:"expires_at"`
	Active    bool                 `bw:"active"`
}

func (r *tokenRow) toService() *service.Token {
	return &service.Token{
		ID:        r.ID,
		Name:      r.Name,
		HashedKey: r.HashedKey,
		Scopes:    r.Scopes,
		CreatedAt: r.CreatedAt,
		CreatedBy: r.CreatedBy,
		ExpiresAt: r.ExpiresAt,
		Active:    r.Active,
	}
}

func tokenRowFromService(t *service.Token) *tokenRow {
	return &tokenRow{
		ID:        t.ID,
		Name:      t.Name,
		HashedKey: t.HashedKey,
		Scopes:    t.Scopes,
		CreatedAt: t.CreatedAt,
		CreatedBy: t.CreatedBy,
		ExpiresAt: t.ExpiresAt,
		Active:    t.Active,
	}
}

// sessionRow stores one user session. user_id is indexed so per-user
// counts and cascading deletes don't have to scan the whole bucket.
type sessionRow struct {
	ID        string    `bw:"id,pk"`
	UserID    string    `bw:"user_id,index"`
	Username  string    `bw:"username"`
	Payload   []byte    `bw:"payload"`
	RefreshID string    `bw:"refresh_id"`
	CreatedAt time.Time `bw:"created_at"`
	ExpiresAt time.Time `bw:"expires_at,index"`
}

func (r *sessionRow) toService() *service.Session {
	return &service.Session{
		ID:        r.ID,
		UserID:    r.UserID,
		Username:  r.Username,
		Payload:   r.Payload,
		RefreshID: r.RefreshID,
		CreatedAt: r.CreatedAt,
		ExpiresAt: r.ExpiresAt,
	}
}

func sessionRowFromService(s *service.Session) *sessionRow {
	return &sessionRow{
		ID:        s.ID,
		UserID:    s.UserID,
		Username:  s.Username,
		Payload:   s.Payload,
		RefreshID: s.RefreshID,
		CreatedAt: s.CreatedAt,
		ExpiresAt: s.ExpiresAt,
	}
}

// permissionRow is a permission with its capability keys and per-key glob
// patterns embedded directly. The previous schema split these across
// `permission_keys` and `permission_key_patterns`; here the rows simply
// live on the parent because we no longer need joins.
type permissionRow struct {
	ID          string              `bw:"id,pk"`
	Key         string              `bw:"key,unique"`
	Name        string              `bw:"name"`
	Description string              `bw:"description"`
	Keys        []string            `bw:"keys"`
	KeyPatterns map[string][]string `bw:"key_patterns"`
	CreatedAt   time.Time           `bw:"created_at"`
}

func (r *permissionRow) toService() *service.Permission {
	keys := r.Keys
	if keys == nil {
		keys = []string{}
	}
	patterns := r.KeyPatterns
	if len(patterns) == 0 {
		patterns = nil
	}
	return &service.Permission{
		ID:          r.ID,
		Key:         r.Key,
		Name:        r.Name,
		Description: r.Description,
		Keys:        keys,
		KeyPatterns: patterns,
		CreatedAt:   r.CreatedAt,
	}
}

func permissionRowFromService(p *service.Permission) *permissionRow {
	return &permissionRow{
		ID:          p.ID,
		Key:         p.Key,
		Name:        p.Name,
		Description: p.Description,
		Keys:        p.Keys,
		KeyPatterns: p.KeyPatterns,
		CreatedAt:   p.CreatedAt,
	}
}

// folderRow is a single folder record. The path doubles as the primary
// key. We keep `Folders`/`Files` slices as the SQL backend did rather
// than building a per-folder index; folder writes are coarse-grained
// enough that this stays simple.
type folderRow struct {
	Path     string              `bw:"path,pk"`
	Folders  []string            `bw:"folders"`
	Files    []string            `bw:"files"`
	Variants map[string][]string `bw:"variants"`
}

func (r *folderRow) toService() *service.Folder {
	folders := r.Folders
	if folders == nil {
		folders = []string{}
	}
	files := r.Files
	if files == nil {
		files = []string{}
	}
	variants := r.Variants
	if len(variants) == 0 {
		variants = nil
	}
	return &service.Folder{
		Folders:  folders,
		Files:    files,
		Variants: variants,
	}
}

func folderRowFromService(path string, f *service.Folder) *folderRow {
	return &folderRow{
		Path:     path,
		Folders:  f.Folders,
		Files:    f.Files,
		Variants: f.Variants,
	}
}

// fileRow stores one (path, version) tuple. There's no natural single-
// field primary key, so we use a custom key extractor (see filestorage).
// Path is indexed so DeleteAllVersions / DeletePrefix can scan by path.
type fileRow struct {
	Path        string                 `bw:"path,index"`
	Version     int64                  `bw:"version"`
	Description string                 `bw:"description"`
	Format      string                 `bw:"format"`
	Inherits    []service.InheritEntry `bw:"inherits"`
	Data        []byte                 `bw:"data"`
}

func (r *fileRow) toService() *service.File {
	return &service.File{
		Meta: service.FileMeta{
			Description: r.Description,
			Format:      r.Format,
			Inherits:    r.Inherits,
		},
		Data: r.Data,
	}
}

func fileRowFromService(path string, version int64, f *service.File) *fileRow {
	return &fileRow{
		Path:        path,
		Version:     version,
		Description: f.Meta.Description,
		Format:      f.Meta.Format,
		Inherits:    f.Meta.Inherits,
		Data:        f.Data,
	}
}

// fileVersionRow holds version metadata for one path. Path is the pk.
type fileVersionRow struct {
	Path     string               `bw:"path,pk"`
	Versions service.FileVersions `bw:"versions"`
}

// passkeyCredentialRow is one persisted WebAuthn credential. PK is a
// stable random id so a credential can be renamed/deleted without
// touching the lookup key the authenticator emits. CredentialID
// carries `unique` so a malicious or buggy authenticator that
// presents the same credential twice cannot land two rows and
// confuse the assertion path; the same flag also makes
// FindByCredentialID an O(1) index hit.
//
// UserID is indexed so the security page (ListByUserID) is cheap
// even when a user has tens of credentials.
//
// Transports is stored as a single comma-joined string (rather than
// a slice) because bw's struct-tag schema doesn't support typed
// slice indexes; the join/split is done in toService/from with a
// fixed comma separator. Transport tokens are constrained to a
// fixed ASCII vocabulary by WebAuthn so comma is safe.
type passkeyCredentialRow struct {
	ID              string    `bw:"id,pk"`
	UserID          string    `bw:"user_id,index"`
	CredentialID    []byte    `bw:"credential_id,unique"`
	PublicKey       []byte    `bw:"public_key"`
	AAGUID          []byte    `bw:"aaguid"`
	SignCount       uint32    `bw:"sign_count"`
	Transports      string    `bw:"transports"`
	UserVerified    bool      `bw:"user_verified"`
	BackupEligible  bool      `bw:"backup_eligible"`
	BackupState     bool      `bw:"backup_state"`
	AttestationType string    `bw:"attestation_type"`
	Name            string    `bw:"name"`
	CreatedAt       time.Time `bw:"created_at,index"`
	LastUsedAt      time.Time `bw:"last_used_at"`
}

func (r *passkeyCredentialRow) toService() *service.PasskeyCredential {
	var transports []string
	if r.Transports != "" {
		transports = splitTransports(r.Transports)
	}
	return &service.PasskeyCredential{
		ID:              r.ID,
		UserID:          r.UserID,
		CredentialID:    r.CredentialID,
		PublicKey:       r.PublicKey,
		AAGUID:          r.AAGUID,
		SignCount:       r.SignCount,
		Transports:      transports,
		UserVerified:    r.UserVerified,
		BackupEligible:  r.BackupEligible,
		BackupState:     r.BackupState,
		AttestationType: r.AttestationType,
		Name:            r.Name,
		CreatedAt:       r.CreatedAt,
		LastUsedAt:      r.LastUsedAt,
	}
}

func passkeyCredentialRowFromService(c *service.PasskeyCredential) *passkeyCredentialRow {
	return &passkeyCredentialRow{
		ID:              c.ID,
		UserID:          c.UserID,
		CredentialID:    c.CredentialID,
		PublicKey:       c.PublicKey,
		AAGUID:          c.AAGUID,
		SignCount:       c.SignCount,
		Transports:      joinTransports(c.Transports),
		UserVerified:    c.UserVerified,
		BackupEligible:  c.BackupEligible,
		BackupState:     c.BackupState,
		AttestationType: c.AttestationType,
		Name:            c.Name,
		CreatedAt:       c.CreatedAt,
		LastUsedAt:      c.LastUsedAt,
	}
}

// joinTransports / splitTransports collapse the WebAuthn transports
// list onto a single string for bw storage. Tokens come from a
// fixed WebAuthn vocabulary ("usb", "nfc", "ble", "internal",
// "hybrid", "smart-card") so comma joining is safe.
func joinTransports(in []string) string {
	if len(in) == 0 {
		return ""
	}
	out := ""
	for i, v := range in {
		if i > 0 {
			out += ","
		}
		out += v
	}
	return out
}

func splitTransports(in string) []string {
	if in == "" {
		return nil
	}
	out := []string{}
	start := 0
	for i := 0; i < len(in); i++ {
		if in[i] == ',' {
			if i > start {
				out = append(out, in[start:i])
			}
			start = i + 1
		}
	}
	if start < len(in) {
		out = append(out, in[start:])
	}
	return out
}

// passkeyChallengeRow is one in-flight WebAuthn ceremony session. We
// store the SessionData blob as opaque JSON bytes so the on-disk
// shape doesn't have to track every field the ada library carries on
// passkey.SessionData. Kind disambiguates enroll vs login challenges
// for audit; user_id is non-empty only for enroll rows (the
// FinishEnroll path uses it for the cross-user-smuggling check).
//
// expires_at is indexed so the periodic sweep can find expired rows
// without scanning the whole bucket. The bucket lives in the same bw
// cluster as the rest of pika's state, so a challenge written on
// follower A is visible to follower B once the leader has finished
// replicating — same propagation contract as sessions/tokens.
type passkeyChallengeRow struct {
	ID        string    `bw:"id,pk"`
	Kind      string    `bw:"kind,index"`
	UserID    string    `bw:"user_id,index"`
	Data      []byte    `bw:"data"`
	CreatedAt time.Time `bw:"created_at"`
	ExpiresAt time.Time `bw:"expires_at,index"`
}

func (r *passkeyChallengeRow) toService() *service.PasskeyChallenge {
	return &service.PasskeyChallenge{
		ID:        r.ID,
		Kind:      r.Kind,
		UserID:    r.UserID,
		Data:      r.Data,
		CreatedAt: r.CreatedAt,
		ExpiresAt: r.ExpiresAt,
	}
}

func passkeyChallengeRowFromService(c *service.PasskeyChallenge) *passkeyChallengeRow {
	return &passkeyChallengeRow{
		ID:        c.ID,
		Kind:      c.Kind,
		UserID:    c.UserID,
		Data:      c.Data,
		CreatedAt: c.CreatedAt,
		ExpiresAt: c.ExpiresAt,
	}
}

// userTOTPRow is one row per user holding their TOTP enrollment state.
// user_id is the primary key — TOTP is one-to-one with users, unlike
// passkeys (multiple credentials per user).
//
// Secret is the base32-encoded HMAC key. RecoveryCodes is the
// bcrypt-hashed plaintexts; storing the hashes alongside the secret is
// fine — they're a different recovery channel that an attacker who has
// the row already doesn't need to crack.
//
// Enabled is the "live" flag: enrollment writes an Enabled=false row
// (start of the QR scan), the finish-enroll handler flips it to true
// once the user proves possession by entering a valid code. A row
// stuck at Enabled=false is harmless; it doesn't gate login.
type userTOTPRow struct {
	UserID        string    `bw:"user_id,pk"`
	Secret        string    `bw:"secret"`
	Enabled       bool      `bw:"enabled"`
	RecoveryCodes []string  `bw:"recovery_codes"`
	CreatedAt     time.Time `bw:"created_at"`
	LastUsedAt    time.Time `bw:"last_used_at"`
}

func (r *userTOTPRow) toService() *service.UserTOTP {
	codes := append([]string(nil), r.RecoveryCodes...)
	return &service.UserTOTP{
		UserID:        r.UserID,
		Secret:        r.Secret,
		Enabled:       r.Enabled,
		RecoveryCodes: codes,
		CreatedAt:     r.CreatedAt,
		LastUsedAt:    r.LastUsedAt,
	}
}

func userTOTPRowFromService(t *service.UserTOTP) *userTOTPRow {
	codes := append([]string(nil), t.RecoveryCodes...)
	return &userTOTPRow{
		UserID:        t.UserID,
		Secret:        t.Secret,
		Enabled:       t.Enabled,
		RecoveryCodes: codes,
		CreatedAt:     t.CreatedAt,
		LastUsedAt:    t.LastUsedAt,
	}
}

// vaultAccountRow is the per-user vault crypto state. PK is the user_id
// so the table is one-to-one with users (same shape as userTOTPRow).
// Every field is sensitive enough that toService returns a copy rather
// than the raw row; the service layer surfaces only the wrapped key +
// KDF parameters on the unlock path, never the secret-key hash.
//
// Argon2id parameters live as scalar columns rather than a nested
// struct so a future filter (e.g. "list vaults using the legacy
// memory budget") could index them. Today nothing scans by these
// fields; the columns are still split because bw nested-struct
// support is shallow and breaking them out keeps the row encoder
// simple.
type vaultAccountRow struct {
	UserID                 string    `bw:"user_id,pk"`
	SecretKeyHash          []byte    `bw:"secret_key_hash"`
	KDFAlgorithm           string    `bw:"kdf_algorithm"`
	KDFMemory              int       `bw:"kdf_memory"`
	KDFIterations          int       `bw:"kdf_iterations"`
	KDFParallelism         int       `bw:"kdf_parallelism"`
	KDFSalt                []byte    `bw:"kdf_salt"`
	WrappedVaultKey        []byte    `bw:"wrapped_vault_key"`
	WrappedVaultKeyVersion int       `bw:"wrapped_vault_key_version"`
	RecoveryKitID          string    `bw:"recovery_kit_id"`
	SessionLockSeconds     int       `bw:"session_lock_seconds"`
	CreatedAt              time.Time `bw:"created_at"`
	UpdatedAt              time.Time `bw:"updated_at"`
}

func (r *vaultAccountRow) toService() *service.VaultAccount {
	hash := append([]byte(nil), r.SecretKeyHash...)
	salt := append([]byte(nil), r.KDFSalt...)
	wrapped := append([]byte(nil), r.WrappedVaultKey...)
	return &service.VaultAccount{
		UserID:        r.UserID,
		SecretKeyHash: hash,
		KDF: service.VaultKDFParams{
			Algorithm:   r.KDFAlgorithm,
			Memory:      r.KDFMemory,
			Iterations:  r.KDFIterations,
			Parallelism: r.KDFParallelism,
			Salt:        salt,
		},
		WrappedVaultKey:        wrapped,
		WrappedVaultKeyVersion: r.WrappedVaultKeyVersion,
		RecoveryKitID:          r.RecoveryKitID,
		SessionLockSeconds:     r.SessionLockSeconds,
		CreatedAt:              r.CreatedAt,
		UpdatedAt:              r.UpdatedAt,
	}
}

func vaultAccountRowFromService(a *service.VaultAccount) *vaultAccountRow {
	hash := append([]byte(nil), a.SecretKeyHash...)
	salt := append([]byte(nil), a.KDF.Salt...)
	wrapped := append([]byte(nil), a.WrappedVaultKey...)
	return &vaultAccountRow{
		UserID:                 a.UserID,
		SecretKeyHash:          hash,
		KDFAlgorithm:           a.KDF.Algorithm,
		KDFMemory:              a.KDF.Memory,
		KDFIterations:          a.KDF.Iterations,
		KDFParallelism:         a.KDF.Parallelism,
		KDFSalt:                salt,
		WrappedVaultKey:        wrapped,
		WrappedVaultKeyVersion: a.WrappedVaultKeyVersion,
		RecoveryKitID:          a.RecoveryKitID,
		SessionLockSeconds:     a.SessionLockSeconds,
		CreatedAt:              a.CreatedAt,
		UpdatedAt:              a.UpdatedAt,
	}
}

// vaultItemRow stores a single encrypted entry in a user's personal
// vault. id is the primary key; user_id is indexed so the per-user
// listing (the only access path) is an O(items-per-user) scan rather
// than a global scan.
//
// Type is indexed so the SPA's type filter ("show only Logins") is a
// fast secondary scan within the user partition. Encrypted fields
// (title, tags, hostnames, payload) are opaque to the server — the
// server stores ciphertext and never inspects it, so indexing them
// would be meaningless. List filtering against those values happens
// entirely in the SPA after decryption.
//
// DeletedAt uses a pointer so the JSON "deleted_at: null" round-trips
// cleanly; the service layer treats a non-nil DeletedAt as "in
// trash" and excludes the row from the active list by default.
//
// EncryptedPayload / EncryptedTitle / EncryptedTags / EncryptedHostnames
// are XChaCha20-Poly1305 ciphertexts produced by the SPA with the
// per-user vault key. The server never holds the key and never
// inspects these bytes; bw treats them as opaque blobs.
//
// Schema is version 2 — see migrate_vault.go for the v1 → v2 wipe
// (v1 stored title/tags/url_hostnames in cleartext).
type vaultItemRow struct {
	ID                 string `bw:"id,pk"`
	UserID             string `bw:"user_id,index"`
	Type               string `bw:"type,index"`
	EncryptedTitle     []byte `bw:"encrypted_title"`
	EncryptedTags      []byte `bw:"encrypted_tags"`
	EncryptedHostnames []byte `bw:"encrypted_hostnames"`
	// EncryptedFolder is the AEAD ciphertext of a single user-chosen
	// folder name. Added after schema v2; older rows that pre-date
	// this field deserialize with a nil slice — semantically "no
	// folder", which is the same as a brand-new item. No migration
	// is required because bw treats absent fields as zero-value.
	EncryptedFolder  []byte     `bw:"encrypted_folder"`
	EncryptedPayload []byte     `bw:"encrypted_payload"`
	Favorite         bool       `bw:"favorite"`
	Archived         bool       `bw:"archived"`
	DeletedAt        *time.Time `bw:"deleted_at"`
	LastUsedAt       *time.Time `bw:"last_used_at"`
	CreatedAt        time.Time  `bw:"created_at,index"`
	UpdatedAt        time.Time  `bw:"updated_at"`
	Version          int64      `bw:"version"`
}

func (r *vaultItemRow) toService() *service.VaultItem {
	return &service.VaultItem{
		ID:                 r.ID,
		UserID:             r.UserID,
		Type:               service.VaultItemType(r.Type),
		EncryptedTitle:     append([]byte(nil), r.EncryptedTitle...),
		EncryptedTags:      append([]byte(nil), r.EncryptedTags...),
		EncryptedHostnames: append([]byte(nil), r.EncryptedHostnames...),
		EncryptedFolder:    append([]byte(nil), r.EncryptedFolder...),
		EncryptedPayload:   append([]byte(nil), r.EncryptedPayload...),
		Favorite:           r.Favorite,
		Archived:           r.Archived,
		DeletedAt:          r.DeletedAt,
		LastUsedAt:         r.LastUsedAt,
		CreatedAt:          r.CreatedAt,
		UpdatedAt:          r.UpdatedAt,
		Version:            r.Version,
	}
}

func vaultItemRowFromService(i *service.VaultItem) *vaultItemRow {
	return &vaultItemRow{
		ID:                 i.ID,
		UserID:             i.UserID,
		Type:               string(i.Type),
		EncryptedTitle:     append([]byte(nil), i.EncryptedTitle...),
		EncryptedTags:      append([]byte(nil), i.EncryptedTags...),
		EncryptedHostnames: append([]byte(nil), i.EncryptedHostnames...),
		EncryptedFolder:    append([]byte(nil), i.EncryptedFolder...),
		EncryptedPayload:   append([]byte(nil), i.EncryptedPayload...),
		Favorite:           i.Favorite,
		Archived:           i.Archived,
		DeletedAt:          i.DeletedAt,
		LastUsedAt:         i.LastUsedAt,
		CreatedAt:          i.CreatedAt,
		UpdatedAt:          i.UpdatedAt,
		Version:            i.Version,
	}
}

// vaultItemVersionRow stores one snapshot of an item's prior state.
// Composite primary key (item_id, version) via a custom KeyFn — see
// vaultItemVersionKey. user_id is indexed so DeleteAllByUser is a
// fast secondary scan; item_id is indexed so ListByItem doesn't have
// to walk the whole bucket.
//
// EncryptedTitle mirrors the live row's title encryption so history
// entries do not leak the readable title — see schema v2.
type vaultItemVersionRow struct {
	ItemID           string    `bw:"item_id,index"`
	UserID           string    `bw:"user_id,index"`
	Version          int64     `bw:"version"`
	EncryptedTitle   []byte    `bw:"encrypted_title"`
	EncryptedPayload []byte    `bw:"encrypted_payload"`
	UpdatedAt        time.Time `bw:"updated_at"`
	Author           string    `bw:"author"`
}

func (r *vaultItemVersionRow) toService() *service.VaultItemVersion {
	return &service.VaultItemVersion{
		ItemID:           r.ItemID,
		Version:          r.Version,
		EncryptedTitle:   append([]byte(nil), r.EncryptedTitle...),
		EncryptedPayload: append([]byte(nil), r.EncryptedPayload...),
		UpdatedAt:        r.UpdatedAt,
		Author:           r.Author,
	}
}

func vaultItemVersionRowFromService(userID string, v *service.VaultItemVersion) *vaultItemVersionRow {
	return &vaultItemVersionRow{
		ItemID:           v.ItemID,
		UserID:           userID,
		Version:          v.Version,
		EncryptedTitle:   append([]byte(nil), v.EncryptedTitle...),
		EncryptedPayload: append([]byte(nil), v.EncryptedPayload...),
		UpdatedAt:        v.UpdatedAt,
		Author:           v.Author,
	}
}

// settingsRow is a singleton — there's only ever one row, keyed by the
// fixed string "default", matching the SQLite layout.
type settingsRow struct {
	ID       string                       `bw:"id,pk"`
	External map[string]external.External `bw:"external"`
	// EncryptionVerifier carries the at-rest verifier ciphertext for
	// the server encryption key. Stored as raw bytes (no base64) since
	// bw handles []byte natively. Empty/nil means the server has not
	// been initialized yet — the first POST /api/v1/key/initialize
	// fills it.
	EncryptionVerifier  []byte                               `bw:"encryption_verifier"`
	RawMounts           []service.RawMountEntry              `bw:"raw_mounts"`
	FTPShares           []service.FTPShareEntry              `bw:"ftp_shares"`
	FTPUsers            []service.FTPUserEntry               `bw:"ftp_users"`
	FTPServe            *service.FTPServeSettings            `bw:"ftp_serve"`
	SFTPServe           *service.SFTPServeSettings           `bw:"sftp_serve"`
	TFTPServe           *service.TFTPServeSettings           `bw:"tftp_serve"`
	WebDAVServe         *service.WebDAVServeSettings         `bw:"webdav_serve"`
	Hooks               []hook.Hook                          `bw:"hooks"`
	// ProxyServers replaces the legacy PublicPort + Compat fields.
	// Each entry is a full kaykay graph plus pipeline metadata.
	ProxyServers        []service.ProxyServer                `bw:"proxy_servers"`
	ExternalPermissions *service.ExternalPermissionsSettings `bw:"external_permissions"`
	ForwardAuth         *service.ForwardAuthSettings         `bw:"forward_auth"`
	Auth                *service.AuthSettings                `bw:"auth"`
	UserSync            *service.UserSyncSettings            `bw:"user_sync"`
	Vault               *service.VaultSettings               `bw:"vault"`
	Proxy               *service.ProxySettings               `bw:"proxy"`
	// Registry carries the artifact-registry namespace + repository
	// tree plus the deployment-wide feature flag. Optional pointer so
	// rows written before the registry feature shipped decode to nil
	// (treated as "feature enabled, no namespaces configured" by the
	// service layer).
	Registry            *service.RegistrySettings            `bw:"registry"`
	// SensitivePayload mirrors service.Settings.SensitivePayload —
	// the encrypted blob holding all secret-bearing values that the
	// secret.Storage wrapper unpacks during Get. bw stores it as raw
	// bytes; nothing in the bw layer interprets the contents.
	SensitivePayload []byte `bw:"sensitive_payload"`
}
