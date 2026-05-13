package bw

import (
	"encoding/binary"
	"testing"

	"github.com/dgraph-io/badger/v4"
)

// TestMigrateVaultV2_WipesLegacyData simulates an old (v1) vault on
// disk by writing fabricated row blobs directly into the Badger
// keyspace before opening the bw store. After Open() the migration
// must have wiped those blobs; otherwise the new schema would try to
// decode them and either crash or silently surface garbage.
func TestMigrateVaultV2_WipesLegacyData(t *testing.T) {
	// Open the underlying Badger directly so we can plant v1 keys
	// without bw's schema check getting in the way.
	dir := t.TempDir()
	opts := badger.DefaultOptions(dir).WithLogger(nil)
	bdb, err := badger.Open(opts)
	if err != nil {
		t.Fatalf("badger.Open: %v", err)
	}

	// Plant a fake v1 data row under each of our three vault buckets
	// plus a fake v1 schema version sentinel. The data shape doesn't
	// matter — only that the keys exist in the right prefix. The
	// version sentinel is what flips MigrateVaultV2 into wipe mode;
	// without it the migration treats the DB as fresh.
	mustSetKey(t, bdb, dataPrefixBytes(bucketVaultAccounts), []byte("legacy-account-row"))
	mustSetKey(t, bdb, dataPrefixBytes(bucketVaultItems), []byte("legacy-item-row"))
	mustSetKey(t, bdb, dataPrefixBytes(bucketVaultItemVersions), []byte("legacy-version-row"))

	// Write a v1 fingerprint + version sentinel so the migration
	// has something to detect. Without these, readBucketVersion
	// returns 0 and we go straight to the wipe path anyway — but
	// covering the realistic case is more valuable.
	var v1 [8]byte
	binary.BigEndian.PutUint64(v1[:], 1)
	for _, name := range []string{bucketVaultAccounts, bucketVaultItems, bucketVaultItemVersions} {
		mustSetKey(t, bdb, versionKeyBytes(name), v1[:])
		mustSetKey(t, bdb, metaKeyBytes(name), []byte("legacy-fingerprint"))
	}
	if err := bdb.Close(); err != nil {
		t.Fatalf("badger.Close: %v", err)
	}

	// Now open through the bw layer — MigrateVaultV2 runs as a
	// pre-register step and should wipe every key we just wrote.
	store, err := New(t.Context(), &Config{Path: dir})
	if err != nil {
		t.Fatalf("bw.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Verify the legacy data rows are gone.
	for _, name := range []string{bucketVaultAccounts, bucketVaultItems, bucketVaultItemVersions} {
		assertPrefixEmpty(t, store.db.Badger(), dataPrefixBytes(name), "data:"+name)
	}

	// Verify the v1 fingerprints are gone (or replaced by v2 ones).
	// bw will have written fresh v2 sentinels by the time we read
	// them; what we care about is "not v1 anymore".
	for _, name := range []string{bucketVaultItems, bucketVaultItemVersions} {
		got, err := readBucketVersion(store.db.Badger(), name)
		if err != nil {
			t.Fatalf("readBucketVersion(%s): %v", name, err)
		}
		if got < vaultSchemaVersion {
			t.Errorf("after migration version for %s should be ≥ %d, got %d", name, vaultSchemaVersion, got)
		}
	}
}

// TestMigrateVaultV2_FreshDBIsNoop checks that opening a brand-new DB
// does not log a wipe (the marker absence path).
func TestMigrateVaultV2_FreshDBIsNoop(t *testing.T) {
	store, err := New(t.Context(), &Config{InMemory: true})
	if err != nil {
		t.Fatalf("bw.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// readBucketVersion should report the current schema after
	// RegisterBucket fires.
	got, err := readBucketVersion(store.db.Badger(), bucketVaultItems)
	if err != nil {
		t.Fatalf("readBucketVersion: %v", err)
	}
	if got != vaultSchemaVersion {
		t.Errorf("fresh DB version: got %d want %d", got, vaultSchemaVersion)
	}
}

func mustSetKey(t *testing.T, bdb *badger.DB, key, value []byte) {
	t.Helper()
	if err := bdb.Update(func(txn *badger.Txn) error {
		return txn.Set(key, value)
	}); err != nil {
		t.Fatalf("badger Set %x: %v", key, err)
	}
}

func assertPrefixEmpty(t *testing.T, bdb *badger.DB, prefix []byte, label string) {
	t.Helper()
	err := bdb.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.IteratorOptions{Prefix: prefix})
		defer it.Close()
		for it.Rewind(); it.Valid(); it.Next() {
			return &keyFoundError{key: it.Item().KeyCopy(nil)}
		}
		return nil
	})
	if err != nil {
		if k, ok := err.(*keyFoundError); ok {
			t.Errorf("%s: prefix %x not empty after migration: found key %x", label, prefix, k.key)
			return
		}
		t.Fatalf("%s: view: %v", label, err)
	}
}

type keyFoundError struct{ key []byte }

func (e *keyFoundError) Error() string { return "key found" }
