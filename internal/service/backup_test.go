package service_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/rakunlabs/pika/internal/service"
	bwstore "github.com/rakunlabs/pika/internal/storage/bw"
)

// newBackupTestService returns an in-memory bw-backed Service. Kept
// local so the test file is self-contained and doesn't depend on the
// permission_test helper being public.
func newBackupTestService(t *testing.T) *service.Service {
	t.Helper()
	store, err := bwstore.New(t.Context(), &bwstore.Config{InMemory: true})
	if err != nil {
		t.Fatalf("bw.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return service.New(store)
}

// seedBackupFixture writes a small amount of data so the backup
// stream is non-empty and we can verify the restored DB looks the
// same.
func seedBackupFixture(t *testing.T, svc *service.Service) {
	t.Helper()
	if _, err := svc.CreateUser(t.Context(), &service.CreateUserRequest{
		Username: "alice",
		Password: "test-password-1234",
	}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
}

func TestBackupRoundTripPlain(t *testing.T) {
	src := newBackupTestService(t)
	seedBackupFixture(t, src)

	var buf bytes.Buffer
	hdr, err := src.Backup(t.Context(), &buf, service.BackupOptions{})
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if hdr.Encrypted {
		t.Fatalf("plain backup reported encrypted=true")
	}
	if hdr.PayloadSize == 0 {
		t.Fatalf("plain backup payload is empty")
	}
	if hdr.DBVersion == 0 {
		t.Fatalf("plain backup db_version is zero")
	}

	dst := newBackupTestService(t)
	if err := dst.Restore(t.Context(), bytes.NewReader(buf.Bytes()), service.RestoreOptions{}); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	got, err := dst.GetUserByUsername(t.Context(), "alice")
	if err != nil {
		t.Fatalf("GetUserByUsername after restore: %v", err)
	}
	if got == nil || got.Username != "alice" {
		t.Fatalf("restored user mismatch: %+v", got)
	}
}

func TestBackupRoundTripEncrypted(t *testing.T) {
	src := newBackupTestService(t)
	seedBackupFixture(t, src)

	var buf bytes.Buffer
	hdr, err := src.Backup(t.Context(), &buf, service.BackupOptions{
		EncryptionPassword: "correct horse battery staple",
	})
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if !hdr.Encrypted {
		t.Fatalf("encrypted backup reported encrypted=false")
	}

	dst := newBackupTestService(t)

	// Wrong password should fail with a clean ErrBadRequest, not a panic.
	err = dst.Restore(t.Context(), bytes.NewReader(buf.Bytes()), service.RestoreOptions{Password: "wrong"})
	if err == nil {
		t.Fatalf("Restore with wrong password returned nil error")
	}
	if !errors.Is(err, service.ErrBadRequest) {
		t.Fatalf("Restore wrong-password error = %v, want ErrBadRequest", err)
	}

	// Missing password should also be a clean ErrBadRequest.
	if err := dst.Restore(t.Context(), bytes.NewReader(buf.Bytes()), service.RestoreOptions{}); !errors.Is(err, service.ErrBadRequest) {
		t.Fatalf("Restore missing-password error = %v, want ErrBadRequest", err)
	}

	// Right password works.
	if err := dst.Restore(t.Context(), bytes.NewReader(buf.Bytes()), service.RestoreOptions{Password: "correct horse battery staple"}); err != nil {
		t.Fatalf("Restore with correct password: %v", err)
	}
	got, err := dst.GetUserByUsername(t.Context(), "alice")
	if err != nil {
		t.Fatalf("GetUserByUsername after encrypted restore: %v", err)
	}
	if got == nil || got.Username != "alice" {
		t.Fatalf("restored user mismatch: %+v", got)
	}
}

func TestBackupRejectsBadMagic(t *testing.T) {
	dst := newBackupTestService(t)
	bogus := []byte("definitely not a pika backup")
	err := dst.Restore(t.Context(), bytes.NewReader(bogus), service.RestoreOptions{})
	if err == nil {
		t.Fatalf("Restore on garbage returned nil error")
	}
	if !errors.Is(err, service.ErrBadRequest) {
		t.Fatalf("Restore on garbage error = %v, want ErrBadRequest", err)
	}
}

func TestBackupHeaderPeek(t *testing.T) {
	src := newBackupTestService(t)
	seedBackupFixture(t, src)

	var buf bytes.Buffer
	wantHdr, err := src.Backup(t.Context(), &buf, service.BackupOptions{})
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}

	gotHdr, err := service.PeekBackup(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("PeekBackup: %v", err)
	}
	if gotHdr != wantHdr {
		t.Fatalf("PeekBackup header mismatch: got %+v want %+v", gotHdr, wantHdr)
	}
}

func TestBackupSinceUntilMutuallyExclusive(t *testing.T) {
	// The service layer documents that until takes precedence; this
	// test is a contract check against future regressions.
	src := newBackupTestService(t)
	seedBackupFixture(t, src)

	var buf bytes.Buffer
	hdr, err := src.Backup(t.Context(), &buf, service.BackupOptions{
		Since: 1,
		Until: 1000,
	})
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if hdr.PayloadSize == 0 {
		t.Fatalf("backup with both since and until produced empty payload")
	}
}

// TestRestoreWipeReplacesExistingData verifies that Restore with
// Wipe=true removes keys that exist in the running DB but are absent
// from the backup. Without wipe, those stranded keys would remain
// (Restore is normally an upsert).
func TestRestoreWipeReplacesExistingData(t *testing.T) {
	src := newBackupTestService(t)
	seedBackupFixture(t, src) // creates "alice"

	var buf bytes.Buffer
	if _, err := src.Backup(t.Context(), &buf, service.BackupOptions{}); err != nil {
		t.Fatalf("Backup: %v", err)
	}

	dst := newBackupTestService(t)
	// Add a user that does NOT exist in the source DB. Without wipe,
	// "bob" survives the restore. With wipe, "bob" is gone.
	if _, err := dst.CreateUser(t.Context(), &service.CreateUserRequest{
		Username: "bob",
		Password: "test-password-1234",
	}); err != nil {
		t.Fatalf("CreateUser bob: %v", err)
	}

	if err := dst.Restore(t.Context(), bytes.NewReader(buf.Bytes()), service.RestoreOptions{Wipe: true}); err != nil {
		t.Fatalf("Restore with wipe: %v", err)
	}

	if _, err := dst.GetUserByUsername(t.Context(), "alice"); err != nil {
		t.Fatalf("alice should be present after wipe-and-restore: %v", err)
	}
	got, err := dst.GetUserByUsername(t.Context(), "bob")
	if err == nil {
		t.Fatalf("bob should be gone after wipe-and-restore, got %+v", got)
	}
	if !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("bob lookup error = %v, want ErrNotFound", err)
	}
}

// TestRestoreWipeWithoutWipePreservesExistingData is the negation of
// the above: confirms the default upsert path leaves unrelated data
// alone.
func TestRestoreWithoutWipePreservesExistingData(t *testing.T) {
	src := newBackupTestService(t)
	seedBackupFixture(t, src)

	var buf bytes.Buffer
	if _, err := src.Backup(t.Context(), &buf, service.BackupOptions{}); err != nil {
		t.Fatalf("Backup: %v", err)
	}

	dst := newBackupTestService(t)
	if _, err := dst.CreateUser(t.Context(), &service.CreateUserRequest{
		Username: "bob",
		Password: "test-password-1234",
	}); err != nil {
		t.Fatalf("CreateUser bob: %v", err)
	}

	if err := dst.Restore(t.Context(), bytes.NewReader(buf.Bytes()), service.RestoreOptions{}); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	for _, name := range []string{"alice", "bob"} {
		if _, err := dst.GetUserByUsername(t.Context(), name); err != nil {
			t.Fatalf("%s should still be present: %v", name, err)
		}
	}
}

// TestRestoreWipeRejectsBadBackupBeforeWiping is the safety guarantee:
// validation errors abort the restore BEFORE the wipe runs, so a
// botched backup file can't blow away production data.
func TestRestoreWipeRejectsBadBackupBeforeWiping(t *testing.T) {
	dst := newBackupTestService(t)
	if _, err := dst.CreateUser(t.Context(), &service.CreateUserRequest{
		Username: "bob",
		Password: "test-password-1234",
	}); err != nil {
		t.Fatalf("CreateUser bob: %v", err)
	}

	// Bad magic — should reject before wiping.
	bogus := []byte("not a backup file")
	err := dst.Restore(t.Context(), bytes.NewReader(bogus), service.RestoreOptions{Wipe: true})
	if err == nil {
		t.Fatalf("Restore on garbage with wipe returned nil")
	}
	if !errors.Is(err, service.ErrBadRequest) {
		t.Fatalf("error = %v, want ErrBadRequest", err)
	}

	// bob must still be there — the wipe should not have run.
	if _, err := dst.GetUserByUsername(t.Context(), "bob"); err != nil {
		t.Fatalf("bob disappeared after rejected wipe: %v", err)
	}
}

// TestRestoreWipeRejectsWrongPasswordBeforeWiping is the encrypted
// variant of the above: a wrong password also has to abort before
// any destruction.
func TestRestoreWipeRejectsWrongPasswordBeforeWiping(t *testing.T) {
	src := newBackupTestService(t)
	seedBackupFixture(t, src)

	var buf bytes.Buffer
	if _, err := src.Backup(t.Context(), &buf, service.BackupOptions{
		EncryptionPassword: "right-password",
	}); err != nil {
		t.Fatalf("Backup: %v", err)
	}

	dst := newBackupTestService(t)
	if _, err := dst.CreateUser(t.Context(), &service.CreateUserRequest{
		Username: "bob",
		Password: "test-password-1234",
	}); err != nil {
		t.Fatalf("CreateUser bob: %v", err)
	}

	err := dst.Restore(t.Context(), bytes.NewReader(buf.Bytes()), service.RestoreOptions{
		Wipe:     true,
		Password: "wrong-password",
	})
	if err == nil {
		t.Fatalf("Restore with wrong password + wipe returned nil")
	}
	if !errors.Is(err, service.ErrBadRequest) {
		t.Fatalf("error = %v, want ErrBadRequest", err)
	}

	if _, err := dst.GetUserByUsername(t.Context(), "bob"); err != nil {
		t.Fatalf("bob disappeared after rejected wipe: %v", err)
	}
}
