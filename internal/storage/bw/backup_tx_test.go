package bw

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rakunlabs/pika/internal/service"
)

func seedToken(t *testing.T, s service.Storage, id string) {
	t.Helper()
	if err := s.Tokens().Create(t.Context(), &service.Token{
		ID: id, Name: "n-" + id, HashedKey: "h-" + id, CreatedAt: time.Now().UTC(), Active: true,
	}); err != nil {
		t.Fatalf("Create token %s: %v", id, err)
	}
}

func TestBackupRestore_RoundTrip(t *testing.T) {
	ctx := t.Context()
	src := newTestStore(t)

	seedToken(t, src, "t1")
	if err := src.Files().Set(ctx, "cfg/app", 1, &service.File{Data: []byte("hello")}); err != nil {
		t.Fatalf("Set file: %v", err)
	}
	if err := src.FileVersions().Set(ctx, "cfg/app", service.FileVersions{{Version: 1}}); err != nil {
		t.Fatalf("Set versions: %v", err)
	}

	var buf bytes.Buffer
	ver, err := src.Backup(&buf, 0)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if ver == 0 || buf.Len() == 0 {
		t.Fatalf("Backup produced version %d, %d bytes", ver, buf.Len())
	}

	dst := newTestStore(t)
	seedToken(t, dst, "stale") // must be dropped by Restore
	if err := dst.Restore(bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if _, err := dst.Tokens().Get(ctx, "t1"); err != nil {
		t.Errorf("token t1 missing after Restore: %v", err)
	}
	if got, err := dst.Tokens().FindByHash(ctx, "h-t1"); err != nil || got.ID != "t1" {
		t.Errorf("FindByHash after Restore = %v, %v", got, err)
	}
	if _, err := dst.Tokens().Get(ctx, "stale"); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("pre-existing token survived Restore: %v", err)
	}
	f, err := dst.Files().Get(ctx, "cfg/app", 1)
	if err != nil || string(f.Data) != "hello" {
		t.Errorf("file after Restore = %v, %v", f, err)
	}
	if v, err := dst.FileVersions().Get(ctx, "cfg/app"); err != nil || len(v) != 1 {
		t.Errorf("versions after Restore = %v, %v", v, err)
	}

	// Bucket handles must still be usable for writes after Restore.
	seedToken(t, dst, "t2")
	if _, total, err := dst.Tokens().List(ctx, nil); err != nil || total != 2 {
		t.Errorf("List after Restore+write = %d, %v; want 2", total, err)
	}
}

func TestApplyBackup_Merges(t *testing.T) {
	ctx := t.Context()
	src := newTestStore(t)
	seedToken(t, src, "from-backup")

	var buf bytes.Buffer
	if _, err := src.Backup(&buf, 0); err != nil {
		t.Fatalf("Backup: %v", err)
	}

	dst := newTestStore(t)
	seedToken(t, dst, "local")
	if err := dst.ApplyBackup(bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("ApplyBackup: %v", err)
	}

	for _, id := range []string{"local", "from-backup"} {
		if _, err := dst.Tokens().Get(ctx, id); err != nil {
			t.Errorf("token %q missing after ApplyBackup: %v", id, err)
		}
	}
	if got, err := dst.Tokens().FindByHash(ctx, "h-from-backup"); err != nil || got.ID != "from-backup" {
		t.Errorf("FindByHash(merged) = %v, %v", got, err)
	}
	seedToken(t, dst, "after")
	if _, total, err := dst.Tokens().List(ctx, nil); err != nil || total != 3 {
		t.Errorf("List after ApplyBackup = %d, %v; want 3", total, err)
	}
}

func TestBackup_Incremental(t *testing.T) {
	ctx := t.Context()
	src := newTestStore(t)
	seedToken(t, src, "a")

	var full bytes.Buffer
	since, err := src.Backup(&full, 0)
	if err != nil {
		t.Fatalf("Backup full: %v", err)
	}
	seedToken(t, src, "b")
	// since is exclusive (entries with version > since), so the captured
	// version is passed as-is; since+1 would skip the next commit.
	var inc bytes.Buffer
	if _, err := src.Backup(&inc, since); err != nil {
		t.Fatalf("Backup incremental: %v", err)
	}

	dst := newTestStore(t)
	if err := dst.Restore(bytes.NewReader(full.Bytes())); err != nil {
		t.Fatalf("Restore full: %v", err)
	}
	if _, err := dst.Tokens().Get(ctx, "b"); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("token b present before incremental: %v", err)
	}
	if err := dst.ApplyBackup(bytes.NewReader(inc.Bytes())); err != nil {
		t.Fatalf("ApplyBackup incremental: %v", err)
	}
	for _, id := range []string{"a", "b"} {
		if _, err := dst.Tokens().Get(ctx, id); err != nil {
			t.Errorf("token %q missing after incremental apply: %v", id, err)
		}
	}
}

func TestVersionAndWipe(t *testing.T) {
	ctx := t.Context()
	store := newTestStore(t)

	v0 := store.Version()
	seedToken(t, store, "a")
	v1 := store.Version()
	if v1 <= v0 {
		t.Errorf("Version did not increase on write: %d -> %d", v0, v1)
	}

	if err := store.Wipe(); err != nil {
		t.Fatalf("Wipe: %v", err)
	}
	if _, err := store.Tokens().Get(ctx, "a"); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("token survived Wipe: %v", err)
	}
	seedToken(t, store, "a") // unique index must be clear again
}

func TestNilStorageBackupHelpers(t *testing.T) {
	var s *Storage
	if v, err := s.Backup(&bytes.Buffer{}, 0); v != 0 || err != nil {
		t.Errorf("nil Backup = %d, %v", v, err)
	}
	if err := s.Restore(&bytes.Buffer{}); err != nil {
		t.Errorf("nil Restore = %v", err)
	}
	if err := s.ApplyBackup(&bytes.Buffer{}); err != nil {
		t.Errorf("nil ApplyBackup = %v", err)
	}
	if err := s.Wipe(); err != nil {
		t.Errorf("nil Wipe = %v", err)
	}
	if s.Version() != 0 {
		t.Errorf("nil Version = %d", s.Version())
	}
}

func TestTx_RollbackOnError(t *testing.T) {
	ctx := t.Context()
	store := newTestStore(t)
	seedToken(t, store, "existing")

	errBoom := errors.New("boom")
	err := store.Tx(ctx, func(ctx context.Context, tx service.Storage) error {
		seedToken(t, tx, "inside")
		if err := tx.Tokens().Delete(ctx, "existing"); err != nil {
			return err
		}
		if err := tx.Sessions().Create(ctx, &service.Session{
			ID: "sess", UserID: "u", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
		}); err != nil {
			return err
		}
		// Writes are visible inside the tx.
		if _, err := tx.Tokens().Get(ctx, "inside"); err != nil {
			t.Errorf("in-tx Get(inside): %v", err)
		}
		if _, err := tx.Tokens().Get(ctx, "existing"); !errors.Is(err, service.ErrNotFound) {
			t.Errorf("in-tx delete not visible: %v", err)
		}
		// Not visible outside until commit.
		if _, err := store.Tokens().Get(ctx, "inside"); !errors.Is(err, service.ErrNotFound) {
			t.Errorf("uncommitted write visible outside tx: %v", err)
		}
		return errBoom
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("Tx err = %v; want errBoom", err)
	}

	if _, err := store.Tokens().Get(ctx, "inside"); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("rolled-back insert visible: %v", err)
	}
	if _, err := store.Tokens().FindByHash(ctx, "h-inside"); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("rolled-back unique index entry visible: %v", err)
	}
	if _, err := store.Tokens().Get(ctx, "existing"); err != nil {
		t.Errorf("rolled-back delete took effect: %v", err)
	}
	if _, err := store.Sessions().Get(ctx, "sess"); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("rolled-back session visible: %v", err)
	}
	// The unique hash must be reusable after rollback.
	seedToken(t, store, "inside")
}

func TestTx_CommitAndNested(t *testing.T) {
	ctx := t.Context()
	store := newTestStore(t)

	err := store.Tx(ctx, func(ctx context.Context, tx service.Storage) error {
		seedToken(t, tx, "outer")
		return tx.Tx(ctx, func(ctx context.Context, inner service.Storage) error {
			if _, err := inner.Tokens().Get(ctx, "outer"); err != nil {
				t.Errorf("nested tx cannot see outer write: %v", err)
			}
			seedToken(t, inner, "inner")
			return nil
		})
	})
	if err != nil {
		t.Fatalf("Tx: %v", err)
	}
	for _, id := range []string{"outer", "inner"} {
		if _, err := store.Tokens().Get(ctx, id); err != nil {
			t.Errorf("committed token %q missing: %v", id, err)
		}
	}

	// A nested error rolls back the whole (flattened) tx.
	errBoom := errors.New("boom")
	err = store.Tx(ctx, func(ctx context.Context, tx service.Storage) error {
		seedToken(t, tx, "outer2")
		return tx.Tx(ctx, func(ctx context.Context, inner service.Storage) error {
			seedToken(t, inner, "inner2")
			return errBoom
		})
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("Tx err = %v; want errBoom", err)
	}
	for _, id := range []string{"outer2", "inner2"} {
		if _, err := store.Tokens().Get(ctx, id); !errors.Is(err, service.ErrNotFound) {
			t.Errorf("token %q visible after nested rollback: %v", id, err)
		}
	}
}

func TestTx_ForwardsVersion(t *testing.T) {
	ctx := t.Context()
	store := newTestStore(t)
	seedToken(t, store, "a")
	want := store.Version()
	if err := store.Tx(ctx, func(_ context.Context, tx service.Storage) error {
		if got := tx.Version(); got != want {
			t.Errorf("tx.Version() = %d; want %d", got, want)
		}
		return nil
	}); err != nil {
		t.Fatalf("Tx: %v", err)
	}
}
