package bw

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"

	"github.com/dgraph-io/badger/v4"
)

// vaultSchemaVersion is the *current* schema version baked into the
// Go struct definitions in rows.go. Bumping this here AND on the
// bw.WithVersion[T](...) call in buckets.go triggers the wipe path
// below for any DB that was opened under an older schema.
//
// History:
//   - v1 (initial): title, tags, url_hostnames stored in cleartext.
//   - v2: title/tags/url_hostnames moved into opaque ciphertext
//     columns (encrypted_title, encrypted_tags, encrypted_hostnames).
//     The transition is not reversible server-side because only the
//     client holds the vault key; we cannot re-encrypt v1 cleartext
//     blobs in place. The feature has not shipped yet, so the only
//     v1 data in the wild lives on developer workstations and CI
//     boxes — we wipe it on first boot under v2 and log a warning.
const vaultSchemaVersion uint64 = 2

// MigrateVaultV2 wipes legacy vault data from a Badger store when its
// recorded schema version is below v2. It MUST be called BEFORE the
// bw bucket handles are registered for vault_items / vault_item_versions
// because bw's per-bucket auto-migrate only adjusts indexes — the
// previously-encoded row blobs would otherwise remain on disk in the
// v1 shape and decode either to garbage or to runtime errors when the
// new struct shape tries to read them.
//
// The function is idempotent: when every targeted bucket is already
// at v2 (or absent entirely on a fresh install), the call is a no-op
// and writes nothing to the database.
//
// We rely on bw's internal key layout (documented in
// rakunlabs/bw/keys.go) to wipe each bucket completely:
//
//	data:   "<bucket>\x00..."
//	index:  "\x00idx\x00<bucket>\x00..."
//	uniq:   "\x00uniq\x00<bucket>\x00..."
//	meta:   "\x00meta\x00<bucket>"
//	fts:    "\x00fts\x00<bucket>\x00..." (none today but cheap to cover)
//
// We do NOT touch vault_accounts here: its schema didn't change in
// v2, so its rows are still readable. The account row's
// wrapped_vault_key, however, is useless once every item under it is
// gone — the user would just see a permanently locked vault with no
// items. To avoid that confusing state we also drop the accounts
// bucket when a v1→v2 wipe happens; the SPA detects the missing
// account and routes the user back through Setup.
func MigrateVaultV2(ctx context.Context, s *Storage) error {
	if s == nil || s.db == nil {
		return nil
	}
	bdb := s.db.Badger()
	if bdb == nil {
		return nil
	}

	current, err := readBucketVersion(bdb, bucketVaultItems)
	if err != nil {
		return fmt.Errorf("vault migrate: read schema version: %w", err)
	}
	if current >= vaultSchemaVersion {
		return nil
	}
	// current < target. Two sub-cases:
	//
	//   - First-ever open of this database: no version sentinel, no
	//     data rows, no meta key. The "wipe" is a no-op but we'd
	//     emit a noisy log line every time. Skip explicitly.
	//   - Existing v1 install upgrading to v2: version sentinel is
	//     either absent (very old) or =1, and data rows exist. Wipe
	//     and log a warning.
	//
	// We tell them apart by probing for any byte under the data
	// prefix of the items bucket — that's the keyspace that holds
	// the value-bearing rows.
	hasLegacy, err := hasAnyKey(bdb, dataPrefixBytes(bucketVaultItems))
	if err != nil {
		return fmt.Errorf("vault migrate: probe legacy data: %w", err)
	}
	if !hasLegacy {
		// Fresh install — nothing to do. RegisterBucket will write
		// the v2 sentinels on its own.
		return nil
	}

	slog.Warn("vault: legacy schema detected, wiping personal vault data for v2 migration",
		"stored_version", current,
		"target_version", vaultSchemaVersion,
	)

	// Wipe all three buckets in one DropPrefix call. Badger drops
	// prefixes by re-writing the LSM with a "discard everything <
	// timestamp" tombstone, so passing every prefix together is far
	// cheaper than serial drops.
	//
	// Per bucket we hit five distinct prefixes that bw maintains:
	// data, index, unique, schema fingerprint (\x00meta\x00…),
	// version sentinel (\x00ver\x00…), and field manifest
	// (\x00mani\x00…). Dropping all of them puts the bucket back
	// into the same state RegisterBucket sees on a fresh install,
	// so the next call writes a clean v2 fingerprint with no
	// stale row blobs underneath.
	prefixes := make([][]byte, 0, 18)
	for _, name := range []string{bucketVaultAccounts, bucketVaultItems, bucketVaultItemVersions} {
		prefixes = append(prefixes,
			dataPrefixBytes(name),
			indexPrefixBytes(name),
			uniqPrefixBytes(name),
			metaKeyBytes(name),
			versionKeyBytes(name),
			manifestKeyBytes(name),
		)
	}
	if err := bdb.DropPrefix(prefixes...); err != nil {
		return fmt.Errorf("vault migrate: drop legacy prefixes: %w", err)
	}
	_ = ctx
	return nil
}

// hasAnyKey returns true when at least one key exists under prefix.
// We iterate exactly one entry, which is enough to short-circuit the
// "is there any data?" probe without paying for a full scan.
func hasAnyKey(bdb *badger.DB, prefix []byte) (bool, error) {
	var found bool
	err := bdb.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.IteratorOptions{
			Prefix:         prefix,
			PrefetchValues: false,
		})
		defer it.Close()
		it.Rewind()
		if it.Valid() {
			found = true
		}
		return nil
	})
	return found, err
}

// readBucketVersion fetches the version sentinel bw stores per
// bucket. Returns 0 when the bucket has never been registered (fresh
// install) — that's distinguished from "registered at v0", which
// pika doesn't actually use.
//
// Key layout matches rakunlabs/bw's versionKey: "\x00ver\x00<bucket>".
// We replicate the encoding here rather than importing the unexported
// helper so the migration code stays operational even if bw's
// internal layout shifts under a future minor — the failure mode in
// that case is "we read 0 and wipe", which is still correct.
func readBucketVersion(bdb *badger.DB, bucket string) (uint64, error) {
	key := versionKeyBytes(bucket)

	var version uint64
	err := bdb.View(func(btx *badger.Txn) error {
		item, err := btx.Get(key)
		if err != nil {
			if err == badger.ErrKeyNotFound {
				return nil
			}
			return err
		}
		return item.Value(func(v []byte) error {
			if len(v) == 8 {
				version = binary.BigEndian.Uint64(v)
			}
			return nil
		})
	})
	return version, err
}

// dataPrefixBytes returns "<bucket>\x00", the prefix bw uses for every
// data row of a bucket. Kept local instead of importing bw's
// unexported helper.
func dataPrefixBytes(bucket string) []byte {
	out := make([]byte, 0, len(bucket)+1)
	out = append(out, bucket...)
	out = append(out, 0x00)
	return out
}

// indexPrefixBytes returns "\x00idx\x00<bucket>\x00", the prefix
// shared by every index entry for any field on the bucket. Dropping
// this wipes every secondary index in one shot.
func indexPrefixBytes(bucket string) []byte {
	out := make([]byte, 0, 6+len(bucket)+1)
	out = append(out, 0x00, 'i', 'd', 'x', 0x00)
	out = append(out, bucket...)
	out = append(out, 0x00)
	return out
}

// uniqPrefixBytes returns "\x00uniq\x00<bucket>\x00", the prefix
// covering every unique-reservation entry on the bucket.
func uniqPrefixBytes(bucket string) []byte {
	out := make([]byte, 0, 7+len(bucket)+1)
	out = append(out, 0x00, 'u', 'n', 'i', 'q', 0x00)
	out = append(out, bucket...)
	out = append(out, 0x00)
	return out
}

// metaKeyBytes returns "\x00meta\x00<bucket>", the single key that
// stores the schema fingerprint. Wiping it forces bw to treat the
// next RegisterBucket as a first-time registration.
func metaKeyBytes(bucket string) []byte {
	out := make([]byte, 0, 7+len(bucket))
	out = append(out, 0x00, 'm', 'e', 't', 'a', 0x00)
	out = append(out, bucket...)
	return out
}

// versionKeyBytes returns the per-bucket version sentinel key. bw
// uses "\x00ver\x00<bucket>" — separate prefix from the fingerprint
// (\x00meta\x00…) and manifest (\x00mani\x00…).
//
// Layout cross-checked against bw v0.2.0 migrate.go's versionKey.
func versionKeyBytes(bucket string) []byte {
	out := make([]byte, 0, 6+len(bucket))
	out = append(out, 0x00, 'v', 'e', 'r', 0x00)
	out = append(out, bucket...)
	return out
}

// manifestKeyBytes returns the per-bucket field-manifest key. bw
// uses "\x00mani\x00<bucket>". Wiping it is needed alongside the
// version + fingerprint so RegisterBucket re-emits a fresh manifest.
func manifestKeyBytes(bucket string) []byte {
	out := make([]byte, 0, 7+len(bucket))
	out = append(out, 0x00, 'm', 'a', 'n', 'i', 0x00)
	out = append(out, bucket...)
	return out
}
