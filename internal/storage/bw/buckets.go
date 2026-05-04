package bw

import (
	"encoding/binary"
	"fmt"

	"github.com/rakunlabs/bw"
)

// Bucket names. Kept in one place so tests can reach in for raw scans.
const (
	bucketUsers          = "users"
	bucketUserIdentities = "user_identities"
	bucketTokens         = "tokens"
	bucketSessions       = "sessions"
	bucketPermissions    = "permissions"
	bucketFolders        = "folders"
	bucketFiles          = "files"
	bucketFileVersions   = "file_versions"
	bucketSettings       = "settings"
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

	if s.files, err = bw.RegisterBucket[fileRow](s.db, bucketFiles,
		bw.WithKeyFn[fileRow](func(r *fileRow) ([]byte, error) {
			return fileRowKey(r.Path, r.Version), nil
		}),
		bw.WithVersion[fileRow](1),
	); err != nil {
		return fmt.Errorf("bw register %s: %w", bucketFiles, err)
	}

	if s.fileVersions, err = bw.RegisterBucket[fileVersionRow](s.db, bucketFileVersions,
		bw.WithVersion[fileVersionRow](1),
	); err != nil {
		return fmt.Errorf("bw register %s: %w", bucketFileVersions, err)
	}

	if s.settings, err = bw.RegisterBucket[settingsRow](s.db, bucketSettings,
		bw.WithVersion[settingsRow](1),
	); err != nil {
		return fmt.Errorf("bw register %s: %w", bucketSettings, err)
	}

	return nil
}
