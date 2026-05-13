package authx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rakunlabs/ada/middleware/auth/identity"
	"github.com/rakunlabs/ada/middleware/auth/strategy/passkey"

	"github.com/rakunlabs/pika/internal/service"
)

// BuildPasskeyEngine constructs the ada/passkey.WebAuthn engine from
// the auth settings. Returns (nil, nil) when passkey support is
// effectively disabled — either explicitly turned off, no RPID
// configured, or no origins listed. The caller treats nil as
// "feature off" and skips wiring the strategy and the PasskeyService.
//
// We intentionally do NOT derive RPID/origins from request headers:
// that invites confusion when pika sits behind a reverse proxy whose
// Host header differs from the public-facing FQDN. Operators set
// these explicitly under Settings → Authentication.
func BuildPasskeyEngine(s *service.AuthSettings) (*passkey.WebAuthn, error) {
	if s == nil || s.Passkey == nil || !s.Passkey.Enabled {
		return nil, nil
	}
	cfg := s.Passkey
	rpID := strings.TrimSpace(cfg.RPID)
	if rpID == "" {
		return nil, nil
	}
	origins := dedupNonEmpty(cfg.RPOrigins)
	if len(origins) == 0 {
		return nil, nil
	}

	displayName := strings.TrimSpace(cfg.RPDisplayName)
	if displayName == "" {
		if t := strings.TrimSpace(s.UI.Title); t != "" {
			displayName = t
		} else {
			displayName = "Pika"
		}
	}

	ttl := cfg.ChallengeTTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}

	uv := passkey.UVPreferred
	switch strings.ToLower(strings.TrimSpace(cfg.UserVerification)) {
	case "required":
		uv = passkey.UVRequired
	case "discouraged":
		uv = passkey.UVDiscouraged
	}

	return passkey.New(&passkey.Config{
		RPID:             rpID,
		RPDisplayName:    displayName,
		RPOrigins:        origins,
		UserVerification: uv,
		ChallengeTTL:     ttl,
	})
}

// BuildPasskeyStrategy adapts the PasskeyService into the
// ada/passkey.Strategy. The strategy is then registered into the
// auth manager's strategy list so /login/pass/<name> dispatches
// to it. Returns nil when either dependency is missing (feature
// off).
//
// The svc parameter is required because the strategy needs access
// to the cluster-aware challenge bucket — passing it in (instead of
// deriving it from ps) keeps the dependency explicit and lets tests
// that wire only the strategy still mock the bucket.
func BuildPasskeyStrategy(engine *passkey.WebAuthn, ps *service.PasskeyService, svc *service.Service, name, label string, ttl time.Duration) (*passkey.Strategy, error) {
	if engine == nil || ps == nil || svc == nil {
		return nil, nil
	}
	if name == "" {
		name = "passkey"
	}
	if label == "" {
		label = "Passkey"
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}

	// CredentialLookup signature: (ctx, credentialID) → (Credential,
	// Identity, error). We delegate to PasskeyService which owns the
	// translation between persisted rows and the typed shapes the
	// passkey package expects.
	lookup := passkey.CredentialLookup(func(ctx context.Context, credentialID []byte) (*passkey.Credential, *identity.Identity, error) {
		return ps.LookupForLogin(ctx, credentialID)
	})

	return passkey.NewStrategy(name, engine, lookup,
		passkey.WithLabel(label),
		// Cluster-aware challenge store. Replaces the strategy's
		// default in-memory store so a begin call landing on one
		// pika instance and the matching finish on another both
		// see the same row. The bucket sits in the bw cluster — the
		// same replication path as sessions and users.
		passkey.WithChallengeStore(newBwChallengeStore(svc, ttl)),
		// Persist sign counter on every successful login. Without
		// this the replay-detection check in ada/passkey.FinishLogin
		// can't catch a cloned hardware key. PasskeyService.LookupForLogin
		// already updates LastUsedAt so we don't duplicate that here.
		passkey.WithSignCountUpdater(func(ctx context.Context, credentialID []byte, newCount uint32) error {
			return ps.UpdateSignCount(ctx, credentialID, newCount)
		}),
		// Resolve user hints (handle bytes or a typed username) to
		// the user's enrolled credential ids so the SPA can run the
		// username-first flow: the front-end POSTs { username }, the
		// strategy scopes allowCredentials to that user's passkeys,
		// and the platform UI presents only the matching credential.
		// Returns (nil, nil) on unresolvable hints so timing leaks
		// can't be used to enumerate users.
		passkey.WithUserCredentialsLookup(func(ctx context.Context, hint passkey.UserHint) ([][]byte, error) {
			return ps.LookupCredentialIDs(ctx, hint.Handle, hint.Username)
		}),
	)
}

// bwChallengeStore implements ada/passkey.ChallengeStore on top of
// pika's bw-backed PasskeyChallengeStorage. The ada interface keys
// rows by an opaque session id and exchanges *passkey.SessionData
// pointers; we marshal those to JSON for the bucket and unmarshal on
// the way out. JSON shape is stable per the SessionData type's own
// json tags — ada documents the struct as JSON-serializable for
// exactly this use case (Redis blobs, bw rows, …).
//
// Cluster contract: every Save/Delete here funnels through the bw
// write path (forwarded to the leader, replicated to followers,
// blocks until the originator's instance is caught up). Reads hit
// the local replica, so a finish call coming in milliseconds after
// the matching begin still sees the row.
type bwChallengeStore struct {
	store service.PasskeyChallengeStorage
	ttl   time.Duration
}

func newBwChallengeStore(svc *service.Service, ttl time.Duration) *bwChallengeStore {
	return &bwChallengeStore{store: svc.PasskeyChallengeStore(), ttl: ttl}
}

// Save persists the challenge under the supplied session id. We set
// ExpiresAt from our own TTL rather than reading it off
// SessionData.Expires because the ada package's SessionData and the
// store's expiry policy are conceptually distinct — letting the
// store-side TTL win means an operator who tunes ChallengeTTL
// upward in the engine sees the longer window respected here even
// if SessionData.Expires was set against the old value.
func (b *bwChallengeStore) Save(ctx context.Context, sessionID string, data *passkey.SessionData) error {
	if data == nil {
		return errors.New("passkey challenge: nil session data")
	}
	blob, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("passkey challenge marshal: %w", err)
	}
	return b.store.Save(ctx, &service.PasskeyChallenge{
		ID:        sessionID,
		Kind:      "login",
		Data:      blob,
		ExpiresAt: time.Now().Add(b.ttl),
	})
}

// Load fetches the challenge row and decodes the JSON blob back into
// a *passkey.SessionData. Returns a generic "not found" error when
// the row is absent or expired — the ada strategy translates that
// into a uniform 401 with no signal about why.
func (b *bwChallengeStore) Load(ctx context.Context, sessionID string) (*passkey.SessionData, error) {
	row, err := b.store.Get(ctx, sessionID)
	if err != nil {
		return nil, errors.New("passkey challenge: not found")
	}
	if !row.ExpiresAt.IsZero() && row.ExpiresAt.Before(time.Now()) {
		// Expired rows shouldn't reach the verifier — clean up and
		// surface the same generic error.
		_ = b.store.Delete(ctx, sessionID)
		return nil, errors.New("passkey challenge: expired")
	}
	var data passkey.SessionData
	if err := json.Unmarshal(row.Data, &data); err != nil {
		return nil, fmt.Errorf("passkey challenge unmarshal: %w", err)
	}
	return &data, nil
}

// Delete removes the challenge row. Idempotent at the storage layer
// (PasskeyChallengeStorage.Delete masks not-found) so the ada
// strategy's eager-delete-on-finish pattern doesn't error on a row
// that was already swept.
func (b *bwChallengeStore) Delete(ctx context.Context, sessionID string) error {
	return b.store.Delete(ctx, sessionID)
}

// dedupNonEmpty returns a fresh slice containing each non-empty,
// trimmed entry exactly once (first occurrence wins). Used to
// sanitize the RPOrigins config — a typo'd duplicate or stray
// whitespace shouldn't break the origin check at login time.
func dedupNonEmpty(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
