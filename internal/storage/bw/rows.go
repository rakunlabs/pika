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

// settingsRow is a singleton — there's only ever one row, keyed by the
// fixed string "default", matching the SQLite layout.
type settingsRow struct {
	ID                  string                               `bw:"id,pk"`
	External            map[string]external.External         `bw:"external"`
	AdminSecretHash     string                               `bw:"admin_secret_hash"`
	RawMounts           []service.RawMountEntry              `bw:"raw_mounts"`
	FTPShares           []service.FTPShareEntry              `bw:"ftp_shares"`
	FTPUsers            []service.FTPUserEntry               `bw:"ftp_users"`
	FTPServe            *service.FTPServeSettings            `bw:"ftp_serve"`
	SFTPServe           *service.SFTPServeSettings           `bw:"sftp_serve"`
	TFTPServe           *service.TFTPServeSettings           `bw:"tftp_serve"`
	WebDAVServe         *service.WebDAVServeSettings         `bw:"webdav_serve"`
	Hooks               []hook.Hook                          `bw:"hooks"`
	PublicPort          *service.PublicPortSettings          `bw:"public_port"`
	Compat              *service.CompatSettings              `bw:"compat"`
	ExternalPermissions *service.ExternalPermissionsSettings `bw:"external_permissions"`
	ForwardAuth         *service.ForwardAuthSettings         `bw:"forward_auth"`
	Auth                *service.AuthSettings                `bw:"auth"`
	UserSync            *service.UserSyncSettings            `bw:"user_sync"`
}
