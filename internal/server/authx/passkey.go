package authx

import (
	"context"
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
func BuildPasskeyStrategy(engine *passkey.WebAuthn, ps *service.PasskeyService, name, label string) (*passkey.Strategy, error) {
	if engine == nil || ps == nil {
		return nil, nil
	}
	if name == "" {
		name = "passkey"
	}
	if label == "" {
		label = "Passkey"
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
		// Persist sign counter on every successful login. Without
		// this the replay-detection check in ada/passkey.FinishLogin
		// can't catch a cloned hardware key. PasskeyService.LookupForLogin
		// already updates LastUsedAt so we don't duplicate that here.
		passkey.WithSignCountUpdater(func(ctx context.Context, credentialID []byte, newCount uint32) error {
			return ps.UpdateSignCount(ctx, credentialID, newCount)
		}),
	)
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
