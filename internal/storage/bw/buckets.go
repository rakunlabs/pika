package bw

import (
	"encoding/binary"
	"fmt"

	"github.com/rakunlabs/bw"
)

// Bucket names. Kept in one place so tests can reach in for raw scans.
const (
	bucketUsers             = "users"
	bucketUserIdentities    = "user_identities"
	bucketTokens            = "tokens"
	bucketSessions          = "sessions"
	bucketPermissions       = "permissions"
	bucketFolders           = "folders"
	bucketFiles             = "files"
	bucketFileVersions      = "file_versions"
	bucketSettings          = "settings"
	bucketUserPreferences   = "user_preferences"
	bucketPasskeys          = "passkey_credentials"
	bucketPasskeyChallenges = "passkey_challenges"
	bucketUserTOTP          = "user_totp"
	bucketVaultAccounts     = "vault_accounts"
	bucketVaultItems        = "vault_items"
	bucketVaultItemVersions = "vault_item_versions"
)

// settingsSingletonID is the only key ever written into the settings
// bucket. The SQLite layout did the same thing — there's only one row
// of settings per pika installation.
const settingsSingletonID = "default"

// fileRowKey builds the composite primary key for a fileRow as
// "<path>\x00<version>" where version is little-endian uint64. The split
// byte (\x00) is illegal in a pika config path (validated at the service
// layer) so collisions are impossible.
func fileRowKey(path string, version int64) []byte {
	out := make([]byte, 0, len(path)+1+8)
	out = append(out, path...)
	out = append(out, 0x00)
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(version))
	out = append(out, buf[:]...)
	return out
}

// vaultItemVersionKey builds the composite primary key for a
// vaultItemVersionRow as "<item_id>\x00<version>". The item_id is a
// 16-byte hex string (no NUL bytes) so the split byte is unambiguous;
// version is encoded big-endian so a prefix scan walks the history in
// chronological order — useful for ListByItem which wants the
// newest entries last (we reverse in code).
func vaultItemVersionKey(itemID string, version int64) []byte {
	out := make([]byte, 0, len(itemID)+1+8)
	out = append(out, itemID...)
	out = append(out, 0x00)
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(version))
	out = append(out, buf[:]...)
	return out
}

// registerBuckets opens every bucket the service needs. It is called once
// at New() time. Bumping `WithVersion` here is what triggers bw's
// incremental schema migration if we add or remove indexes later.
func (s *Storage) registerBuckets() error {
	var err error

	if s.users, err = bw.RegisterBucket[userRow](s.db, bucketUsers,
		bw.WithVersion[userRow](1),
	); err != nil {
		return fmt.Errorf("bw register %s: %w", bucketUsers, err)
	}

	if s.userIdentities, err = bw.RegisterBucket[userIdentityRow](s.db, bucketUserIdentities,
		bw.WithVersion[userIdentityRow](1),
	); err != nil {
		return fmt.Errorf("bw register %s: %w", bucketUserIdentities, err)
	}

	if s.tokens, err = bw.RegisterBucket[tokenRow](s.db, bucketTokens,
		bw.WithVersion[tokenRow](1),
	); err != nil {
		return fmt.Errorf("bw register %s: %w", bucketTokens, err)
	}

	if s.sessions, err = bw.RegisterBucket[sessionRow](s.db, bucketSessions,
		bw.WithVersion[sessionRow](1),
	); err != nil {
		return fmt.Errorf("bw register %s: %w", bucketSessions, err)
	}

	if s.permissions, err = bw.RegisterBucket[permissionRow](s.db, bucketPermissions,
		bw.WithVersion[permissionRow](1),
	); err != nil {
		return fmt.Errorf("bw register %s: %w", bucketPermissions, err)
	}

	if s.folders, err = bw.RegisterBucket[folderRow](s.db, bucketFolders,
		bw.WithVersion[folderRow](1),
	); err != nil {
		return fmt.Errorf("bw register %s: %w", bucketFolders, err)
	}

	// Version bumps for the files bucket:
	//
	//   v1 — initial schema.
	//   v2 — added GoTemplate bool field for per-file config template rendering.
	if s.files, err = bw.RegisterBucket[fileRow](s.db, bucketFiles,
		bw.WithKeyFn[fileRow](func(r *fileRow) ([]byte, error) {
			return fileRowKey(r.Path, r.Version), nil
		}),
		bw.WithVersion[fileRow](2),
	); err != nil {
		return fmt.Errorf("bw register %s: %w", bucketFiles, err)
	}

	if s.fileVersions, err = bw.RegisterBucket[fileVersionRow](s.db, bucketFileVersions,
		bw.WithVersion[fileVersionRow](1),
	); err != nil {
		return fmt.Errorf("bw register %s: %w", bucketFileVersions, err)
	}

	// Version bumps for the settings bucket:
	//
	//   v1 — initial schema.
	//   v2 — added Registry *service.RegistrySettings field
	//        (artifact-registry namespace/repo tree + feature flag).
	//        Untagged new field; existing rows decode with Registry
	//        == nil, treated by the service layer as "feature
	//        enabled, no namespaces configured yet". The boot-time
	//        EnsureDefaultRegistryNamespace path materialises the
	//        default tree on the next save.
	//   v3 — added ProxyListeners []service.ProxyListener field
	//        and ProxyServer.ListenerID / HostMatch (listener split).
	//        Untagged new field; existing rows decode with
	//        ProxyListeners == nil. The proxy manager's
	//        synthesizeLegacyListeners path runs on first reconcile
	//        and rewrites every legacy ProxyServer to point at a
	//        synthesized listener that preserves its Host:Port.
	if s.settings, err = bw.RegisterBucket[settingsRow](s.db, bucketSettings,
		bw.WithVersion[settingsRow](3),
	); err != nil {
		return fmt.Errorf("bw register %s: %w", bucketSettings, err)
	}

	if s.userPreferences, err = bw.RegisterBucket[userPreferencesRow](s.db, bucketUserPreferences,
		bw.WithVersion[userPreferencesRow](1),
	); err != nil {
		return fmt.Errorf("bw register %s: %w", bucketUserPreferences, err)
	}

	if s.passkeys, err = bw.RegisterBucket[passkeyCredentialRow](s.db, bucketPasskeys,
		bw.WithVersion[passkeyCredentialRow](1),
	); err != nil {
		return fmt.Errorf("bw register %s: %w", bucketPasskeys, err)
	}

	if s.passkeyChallenges, err = bw.RegisterBucket[passkeyChallengeRow](s.db, bucketPasskeyChallenges,
		bw.WithVersion[passkeyChallengeRow](1),
	); err != nil {
		return fmt.Errorf("bw register %s: %w", bucketPasskeyChallenges, err)
	}

	if s.userTOTP, err = bw.RegisterBucket[userTOTPRow](s.db, bucketUserTOTP,
		bw.WithVersion[userTOTPRow](1),
	); err != nil {
		return fmt.Errorf("bw register %s: %w", bucketUserTOTP, err)
	}

	if s.vaultAccounts, err = bw.RegisterBucket[vaultAccountRow](s.db, bucketVaultAccounts,
		bw.WithVersion[vaultAccountRow](1),
	); err != nil {
		return fmt.Errorf("bw register %s: %w", bucketVaultAccounts, err)
	}

	// vault_items / vault_item_versions are at schema version 2:
	// v1 stored title/tags/url_hostnames in cleartext, v2 moves them
	// into opaque ciphertext columns. MigrateVaultV2 (called from
	// server boot, before any vault op) wipes legacy v1 data because
	// the cleartext-to-ciphertext transition can't be performed
	// server-side without the per-user vault key — and the feature
	// has not yet shipped, so no production user data is at risk.
	if s.vaultItems, err = bw.RegisterBucket[vaultItemRow](s.db, bucketVaultItems,
		bw.WithVersion[vaultItemRow](2),
	); err != nil {
		return fmt.Errorf("bw register %s: %w", bucketVaultItems, err)
	}

	if s.vaultItemVersions, err = bw.RegisterBucket[vaultItemVersionRow](s.db, bucketVaultItemVersions,
		bw.WithKeyFn[vaultItemVersionRow](func(r *vaultItemVersionRow) ([]byte, error) {
			return vaultItemVersionKey(r.ItemID, r.Version), nil
		}),
		bw.WithVersion[vaultItemVersionRow](2),
	); err != nil {
		return fmt.Errorf("bw register %s: %w", bucketVaultItemVersions, err)
	}

	return nil
}
