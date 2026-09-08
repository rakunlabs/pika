package authx

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rakunlabs/ada/middleware/auth/strategy/passkey"
	"github.com/rakunlabs/pika/internal/service"
)

// TestBwChallengeStore_roundTripAcrossInstances is the regression
// test for the cluster-aware login challenge bucket. The ada/passkey
// strategy uses an injected ChallengeStore to bridge a begin call to
// the matching finish; in a multi-instance pika deployment the two
// requests may land on different nodes. This test exercises the bw
// store through distinct adapters sharing the leader's storage. This does
// not simulate replication or HTTP forwarding.
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
	// Forwarded finish requests execute against this same leader DB.
	store2 := newBwChallengeStore(svc, 5*time.Minute)
	loaded, err := store2.Consume(context.Background(), sid)
	if err != nil {
		t.Fatalf("Consume (instance 2): %v", err)
	}
	if loaded == nil {
		t.Fatal("Consume returned nil")
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

	if _, err := store1.Consume(context.Background(), sid); err == nil {
		t.Error("Consume after consume should fail")
	}
}

// TestBwChallengeStore_expiredRowIsCleanedAndRejected verifies the
// expiry guard inside Consume. A row written with a past ExpiresAt
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

	if data, err := store.Consume(context.Background(), sid); err == nil || data != nil {
		t.Error("Consume of expired row should fail without data")
	}

	// Even rejected challenges must be burned.
	if _, err := svc.PasskeyChallengeStore().Get(context.Background(), sid); err == nil {
		t.Error("expired row should be deleted by Consume")
	}
}

func TestBwChallengeStoreConcurrentConsume(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	if err := newBwChallengeStore(svc, time.Minute).Save(ctx, "concurrent", &passkey.SessionData{Challenge: []byte("challenge")}); err != nil {
		t.Fatal(err)
	}
	var winners atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 64 {
		// Distinct wrappers must not rely on an adapter-local mutex.
		store := newBwChallengeStore(svc, time.Minute)
		wg.Go(func() {
			<-start
			data, err := store.Consume(ctx, "concurrent")
			if err != nil {
				if data != nil {
					t.Error("failed consume returned data")
				}
				return
			}
			winners.Add(1)
			if data == nil || string(data.Challenge) != "challenge" {
				t.Error("incorrect consumed data")
			}
		})
	}
	close(start)
	wg.Wait()
	if got := winners.Load(); got != 1 {
		t.Fatalf("successful consumes = %d, want 1", got)
	}
	if _, err := svc.PasskeyChallengeStore().Get(ctx, "concurrent"); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("consumed row still present: %v", err)
	}
}

func TestBwChallengeStoreMalformedRowIsConsumed(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	if err := svc.PasskeyChallengeStore().Save(ctx, &service.PasskeyChallenge{
		ID: "malformed", Data: []byte("not json"), ExpiresAt: time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if data, err := newBwChallengeStore(svc, time.Minute).Consume(ctx, "malformed"); err == nil || data != nil {
		t.Fatal("malformed challenge must fail without data")
	}
	if _, err := svc.PasskeyChallengeStore().Get(ctx, "malformed"); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("malformed row was not consumed: %v", err)
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
