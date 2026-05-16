package keymgr_test

import (
	"crypto/rand"
	"runtime"
	"sync"
	"testing"

	"github.com/rakunlabs/pika/internal/secret/crypto"
	"github.com/rakunlabs/pika/internal/secret/keymgr"
)

// makeKey returns a random 32-byte key. Used throughout the tests so
// each unlock cycle gets fresh material; we never assert on cipher
// output, only on Manager state transitions.
func makeKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, crypto.KeySize)
	if _, err := rand.Read(k); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return k
}

func newEnc(t *testing.T) crypto.Encryptor {
	t.Helper()
	enc, err := crypto.NewChaCha20(makeKey(t))
	if err != nil {
		t.Fatalf("new chacha: %v", err)
	}
	return enc
}

// TestZeroValueIsLocked guards the documented invariant: a fresh
// manager reports both flags as false. Anything else would mean
// callers see a "ready" server before any unlock has happened.
func TestZeroValueIsLocked(t *testing.T) {
	m := keymgr.New()

	st := m.State()
	if st.Initialized || st.Unlocked {
		t.Fatalf("fresh manager should be locked & uninitialized, got %+v", st)
	}
	if _, ok := m.Encryptor(); ok {
		t.Fatalf("Encryptor() returned ok=true on locked manager")
	}
	if m.IsUnlocked() {
		t.Fatalf("IsUnlocked()=true on fresh manager")
	}
}

// TestUnlockExposesEncryptor verifies the round-trip: Unlock places
// the encryptor where Encryptor() can find it, State reports the
// transition, and the same instance comes back (identity, not a
// re-construction).
func TestUnlockExposesEncryptor(t *testing.T) {
	m := keymgr.New()
	enc := newEnc(t)

	if err := m.Unlock(enc); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	got, ok := m.Encryptor()
	if !ok {
		t.Fatalf("Encryptor() ok=false after Unlock")
	}
	if got != enc {
		t.Fatalf("Encryptor() returned different instance")
	}
	if !m.State().Unlocked {
		t.Fatalf("State().Unlocked=false after Unlock")
	}
}

// TestUnlockNilRejected — passing nil is a programming error; the
// manager must refuse rather than silently locking. A silent lock
// here would let a misbehaving handler "unlock" the server in the
// wrong state.
func TestUnlockNilRejected(t *testing.T) {
	m := keymgr.New()
	if err := m.Unlock(nil); err == nil {
		t.Fatalf("Unlock(nil) returned no error")
	}
	if m.IsUnlocked() {
		t.Fatalf("Unlock(nil) somehow unlocked the manager")
	}
}

// TestLockClearsEncryptor — Lock() must make Encryptor() return
// (nil, false). This is the safety property the storage layer
// depends on for its 503 path.
func TestLockClearsEncryptor(t *testing.T) {
	m := keymgr.New()
	if err := m.Unlock(newEnc(t)); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	m.Lock()
	if _, ok := m.Encryptor(); ok {
		t.Fatalf("Encryptor() ok=true after Lock")
	}
	if m.IsUnlocked() {
		t.Fatalf("IsUnlocked()=true after Lock")
	}
	// Idempotent — locking again must not panic or hang.
	m.Lock()
}

// TestMarkInitialized — initialization is monotonic; once a
// verifier exists in the DB the manager reflects that for the
// process lifetime, regardless of unlock state. We test that
// MarkInitialized() doesn't touch the unlocked bit (they're
// orthogonal lifecycle dimensions).
func TestMarkInitialized(t *testing.T) {
	m := keymgr.New()
	m.MarkInitialized()

	st := m.State()
	if !st.Initialized {
		t.Fatalf("State().Initialized=false after MarkInitialized")
	}
	if st.Unlocked {
		t.Fatalf("MarkInitialized leaked into Unlocked bit")
	}
}

// TestRotationSwap — Unlock-while-unlocked replaces the active
// encryptor cleanly (this is the rotation path; the rotation
// service swaps to a RotatedEncryptor that holds both old and new
// for the duration of the rewrap, then a final Unlock with the
// pure new encryptor finalizes).
func TestRotationSwap(t *testing.T) {
	m := keymgr.New()
	first := newEnc(t)
	second := newEnc(t)

	_ = m.Unlock(first)
	_ = m.Unlock(second)

	got, _ := m.Encryptor()
	if got != second {
		t.Fatalf("Encryptor() did not swap to the second instance")
	}
}

// TestConcurrentReaders — the documented hot-path guarantee: many
// goroutines call Encryptor() while Lock/Unlock churn in the
// background. We pre-build the two encryptors so the writer loop
// doesn't pay chacha20poly1305.NewX cost on every iteration (which
// is multiple ms under -race and would dominate the test). The
// assertion is structural: no panic and no race detector trip.
func TestConcurrentReaders(t *testing.T) {
	m := keymgr.New()
	encA := newEnc(t)
	encB := newEnc(t)
	_ = m.Unlock(encA)

	const readers = 16
	const iters = 200

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				// ok may be either value; the writer is racing us.
				_, _ = m.Encryptor()
				// Yield so the writer goroutine actually gets
				// scheduled — without this the readers spin in
				// a tight atomic-load loop and starve the
				// transitionMu-acquiring writer under -race.
				runtime.Gosched()
			}
		}()
	}

	// Writer: alternate lock/unlock and swap between two pre-built
	// encryptors so readers see all three pointer states (nil, A, B).
	for i := 0; i < iters; i++ {
		m.Lock()
		if i%2 == 0 {
			_ = m.Unlock(encA)
		} else {
			_ = m.Unlock(encB)
		}
	}

	close(stop)
	wg.Wait()
}
