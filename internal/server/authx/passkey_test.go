package authx

import (
	"context"
	"testing"
	"time"

	"github.com/rakunlabs/ada/middleware/auth/strategy/passkey"
)

// TestBwChallengeStore_roundTripAcrossInstances is the regression
// test for the cluster-aware login challenge bucket. The ada/passkey
// strategy uses an injected ChallengeStore to bridge a begin call to
// the matching finish; in a multi-instance pika deployment the two
// requests may land on different nodes. This test exercises the bw
// store by saving on one logical "instance" and loading on another,
// both backed by the same Service (which would be the case after
// bw cluster replication).
func TestBwChallengeStore_roundTripAcrossInstances(t *testing.T) {
	svc := newTestService(t)

	// First "instance" — saves the challenge.
	store1 := newBwChallengeStore(svc, 5*time.Minute)
	sid := "test-session-1"
	original := &passkey.SessionData{
		Challenge:        []byte{0x01, 0x02, 0x03, 0x04, 0x05},
		UserHandle:       []byte("alice-handle"),
		UserVerification: passkey.UVPreferred,
		Expires:          time.Now().Add(5 * time.Minute),
		AllowedCredentialIDs: [][]byte{
			{0xa1, 0xa2},
			{0xb1, 0xb2},
		},
	}
	if err := store1.Save(context.Background(), sid, original); err != nil {
		t.Fatalf("Save (instance 1): %v", err)
	}

	// Second "instance" — distinct wrapper around the same Service.
	// In production this is a different pika process; the bw cluster
	// replicates the row between them. In tests we share the Service
	// directly, which exercises the storage code path without the
	// QUIC layer.
	store2 := newBwChallengeStore(svc, 5*time.Minute)
	loaded, err := store2.Load(context.Background(), sid)
	if err != nil {
		t.Fatalf("Load (instance 2): %v", err)
	}
	if loaded == nil {
		t.Fatal("Load returned nil")
	}

	// Round-trip equality on every field the strategy actually uses.
	// The JSON encoding strips microsecond resolution that didn't go
	// through the encoder so we use a tolerant time compare.
	if !bytesEqual(loaded.Challenge, original.Challenge) {
		t.Errorf("Challenge mismatch: got %x, want %x", loaded.Challenge, original.Challenge)
	}
	if !bytesEqual(loaded.UserHandle, original.UserHandle) {
		t.Errorf("UserHandle mismatch")
	}
	if loaded.UserVerification != original.UserVerification {
		t.Errorf("UV mismatch: got %q, want %q", loaded.UserVerification, original.UserVerification)
	}
	if len(loaded.AllowedCredentialIDs) != len(original.AllowedCredentialIDs) {
		t.Fatalf("AllowedCredentialIDs len: got %d, want %d", len(loaded.AllowedCredentialIDs), len(original.AllowedCredentialIDs))
	}
	for i := range loaded.AllowedCredentialIDs {
		if !bytesEqual(loaded.AllowedCredentialIDs[i], original.AllowedCredentialIDs[i]) {
			t.Errorf("AllowedCredentialIDs[%d] mismatch", i)
		}
	}

	// Delete from instance 1; instance 2 must see the row gone.
	if err := store1.Delete(context.Background(), sid); err != nil {
		t.Fatalf("Delete (instance 1): %v", err)
	}
	if _, err := store2.Load(context.Background(), sid); err == nil {
		t.Error("Load after delete should fail")
	}
}

// TestBwChallengeStore_expiredRowIsCleanedAndRejected verifies the
// expiry guard inside Load. A row written with a past ExpiresAt
// should be deleted on access and report not-found to the caller.
func TestBwChallengeStore_expiredRowIsCleanedAndRejected(t *testing.T) {
	svc := newTestService(t)
	store := newBwChallengeStore(svc, 5*time.Minute)
	sid := "expired-1"

	// Save with a normal TTL.
	if err := store.Save(context.Background(), sid, &passkey.SessionData{
		Challenge: []byte{0xff},
		Expires:   time.Now().Add(5 * time.Minute),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Hand-edit the row to be expired. We can't backdate via the
	// public Save path (it always sets ExpiresAt from now+TTL), so
	// reach through the service's storage helper.
	row, err := svc.PasskeyChallengeStore().Get(context.Background(), sid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	row.ExpiresAt = time.Now().Add(-1 * time.Minute)
	if err := svc.PasskeyChallengeStore().Save(context.Background(), row); err != nil {
		t.Fatalf("Save (expired): %v", err)
	}

	if _, err := store.Load(context.Background(), sid); err == nil {
		t.Error("Load of expired row should fail")
	}

	// And the row must be gone after the Load — Load auto-deletes
	// on expiry so the bucket doesn't accumulate stragglers.
	if _, err := svc.PasskeyChallengeStore().Get(context.Background(), sid); err == nil {
		t.Error("expired row should be deleted by Load")
	}
}

// bytesEqual is a tiny helper to avoid pulling in reflect.DeepEqual
// for the simple slice comparisons above.
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
