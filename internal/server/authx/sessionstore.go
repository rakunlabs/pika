// Package authx wires github.com/rakunlabs/ada/middleware/auth into pika.
package authx

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/rakunlabs/ada/middleware/auth/sessionstore"

	"github.com/rakunlabs/pika/internal/service"
)

// SessionStore implements sessionstore.Store against pika's SQLite-backed
// session table. Used as the single session backend — ada's in-memory default
// is not exposed.
type SessionStore struct {
	svc         *service.Service
	defaultName string
}

// NewSessionStore returns a SessionStore bound to the given service. defaultName
// is the session cookie name configured for the auth middleware.
func NewSessionStore(svc *service.Service, defaultName string) *SessionStore {
	return &SessionStore{svc: svc, defaultName: defaultName}
}

// Get returns the session for the given cookie name, or a new empty session
// when no matching cookie or DB row is present.
//
// When a cookie is present but the backing DB row is missing or expired, the
// returned session still carries the cookie's value as its ID (with
// IsNew=true). This is required so that callers like ada's issuer backend —
// which synthesizes a request with the desired SessionID as a cookie, then
// expects Save to persist the pair under that same ID — can round-trip a
// known ID through Get/Save. Dropping the ID here causes Save to mint a new
// one, leaving the issuer's cookie pointing at a row that does not exist and
// producing spurious 401s on the very next request.
func (s *SessionStore) Get(r *http.Request, name string) (*sessionstore.Session, error) {
	sess := sessionstore.NewSession(s, name, nil)

	cookie, err := r.Cookie(name)
	if err != nil {
		// No cookie — return a fresh session; Save will mint an ID.
		return sess, nil
	}
	if cookie.Value == "" {
		return sess, nil
	}

	row, err := s.svc.GetRawSession(r.Context(), cookie.Value)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			// Row missing (e.g. issuer is writing a brand-new pair under
			// a caller-chosen SessionID). Preserve the cookie value as
			// the session ID so Save writes under the intended key.
			sess.ID = cookie.Value
			return sess, nil
		}
		return nil, err
	}
	if row.ExpiresAt.Before(time.Now()) {
		// Expired — delete and behave as not-found, but preserve the
		// cookie-provided ID so an immediate re-save reuses it.
		_ = s.svc.DeleteRawSession(r.Context(), row.ID)
		sess.ID = cookie.Value
		return sess, nil
	}

	values := make(map[string]any)
	if len(row.Payload) > 0 {
		if err := json.Unmarshal(row.Payload, &values); err != nil {
			return nil, err
		}
	}

	sess.ID = row.ID
	sess.Values = values
	sess.IsNew = false
	return sess, nil
}

// Save persists the session and writes the session cookie. When Options.MaxAge
// is negative the DB row is deleted and a cookie deletion is written.
//
// The row is keyed by sess.ID. If no ID is set (fresh session from Get), a
// new one is minted here. The session row also carries username and user_id
// extracted from the issuer's stored pair so that admin actions like kick
// (Sessions.DeleteByUserID) and the disabled-user sweep can find these rows.
func (s *SessionStore) Save(r *http.Request, w http.ResponseWriter, sess *sessionstore.Session) error {
	opts := sess.Options
	name := sess.Name()

	if opts != nil && opts.MaxAge < 0 {
		if sess.ID != "" {
			_ = s.svc.DeleteRawSession(r.Context(), sess.ID)
		}
		http.SetCookie(w, &http.Cookie{
			Name:   name,
			Value:  "",
			Path:   optPath(opts),
			MaxAge: -1,
		})
		return nil
	}

	if sess.ID == "" {
		id, err := service.NewSessionID()
		if err != nil {
			return err
		}
		sess.ID = id
	}

	ttl := 24 * time.Hour
	if opts != nil && opts.MaxAge > 0 {
		ttl = time.Duration(opts.MaxAge) * time.Second
	}

	// Derive the pika user the session belongs to. Admin flows (kick,
	// disable, list) all key off user_id, so this is the single point
	// where ada's Identity (provider-shaped) is mapped onto pika's
	// internal users.id once per save.
	username, userID := s.resolveSessionUser(r.Context(), sess.Values)

	// Stamp the resolved user_id INTO the serialized identity claims
	// before persisting. Downstream readers (CapResolver, future
	// middlewares) read it via identity.Claim[string]("pika_user_id")
	// instead of re-running the provider-based dispatch on every
	// request. Identity is the single source of truth: ada owns
	// Subject/Provider/Email/etc., pika owns the user_id claim under
	// a namespaced key. The decoration mutates sess.Values["pair"]
	// directly, so the marshaled payload below carries the claim for
	// every subsequent SessionStore.Get.
	if userID != "" {
		stampUserIDClaim(sess.Values, userID)
	}

	payload, err := json.Marshal(sess.Values)
	if err != nil {
		return err
	}

	if err := s.svc.PutRawSession(r.Context(), &service.RawSession{
		ID:        sess.ID,
		UserID:    userID,
		Username:  username,
		Payload:   payload,
		ExpiresAt: time.Now().Add(ttl),
	}); err != nil {
		return err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    sess.ID,
		Path:     optPath(opts),
		Domain:   optDomain(opts),
		MaxAge:   optMaxAge(opts),
		Secure:   opts != nil && opts.Secure,
		HttpOnly: opts != nil && opts.HttpOnly,
		SameSite: optSameSite(opts),
	})
	return nil
}

// extractedIdentity is the subset of ada's Identity we need at the session
// layer. Parsed from sess.Values["pair"] by extractIdentity below.
type extractedIdentity struct {
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	Provider      string
}

// extractIdentity parses the issuer's serialized Pair out of sess.Values
// and returns the fields pika needs for provisioning. The ada issuer
// stores its Pair under sess.Values["pair"] as a JSON-encoded string
// (see middleware/auth/issuer/backend/sessionstore.go:85-92 in ada). A
// legacy "username" value is honored for back-compat with rows written
// before the issuer-based session store existed.
func extractIdentity(values map[string]any) extractedIdentity {
	var out extractedIdentity

	if v, ok := values["username"].(string); ok && v != "" {
		out.Subject = v
	}

	raw, ok := values["pair"].(string)
	if !ok || raw == "" {
		return out
	}
	var pair struct {
		Identity struct {
			Subject       string `json:"subject"`
			Email         string `json:"email"`
			EmailVerified bool   `json:"email_verified"`
			Name          string `json:"name"`
			Provider      string `json:"provider"`
		} `json:"identity"`
	}
	if err := json.Unmarshal([]byte(raw), &pair); err != nil {
		return out
	}
	if pair.Identity.Subject != "" {
		out.Subject = pair.Identity.Subject
	}
	out.Email = pair.Identity.Email
	out.EmailVerified = pair.Identity.EmailVerified
	out.Name = pair.Identity.Name
	out.Provider = pair.Identity.Provider
	return out
}

// PikaUserIDClaim is the identity-claim key that carries the resolved
// pika users.id alongside the rest of ada's Identity. SessionStore.Save
// stamps it after resolving the (Provider, Subject) → users.id mapping
// once, so downstream consumers (capability resolver, audit hooks)
// never have to re-run the provider-based dispatch on every request.
// Namespaced with the "pika_" prefix so it can't collide with custom
// OIDC claims that an external IdP might emit.
const PikaUserIDClaim = "pika_user_id"

// stampUserIDClaim mutates the serialized "pair" inside sess.Values so
// that Identity.Claims["pika_user_id"] carries the resolved user_id.
// The pair is round-tripped through generic map[string]any — we avoid
// importing ada's Pair/Identity structs here so the session payload
// stays opaque to non-identity fields (access/refresh blobs etc.)
// that we have no business reshaping.
//
// Failure modes are intentionally silent: a missing or unparseable
// pair just doesn't get decorated, the DB row's user_id column still
// reflects the resolved user, and the next Save will retry. This
// avoids a session-save failure for what is effectively a cache
// optimization.
func stampUserIDClaim(values map[string]any, userID string) {
	raw, ok := values["pair"].(string)
	if !ok || raw == "" {
		return
	}
	var pair map[string]any
	if err := json.Unmarshal([]byte(raw), &pair); err != nil {
		return
	}
	ident, _ := pair["identity"].(map[string]any)
	if ident == nil {
		ident = map[string]any{}
		pair["identity"] = ident
	}
	claims, _ := ident["claims"].(map[string]any)
	if claims == nil {
		claims = map[string]any{}
		ident["claims"] = claims
	}
	claims[PikaUserIDClaim] = userID
	out, err := json.Marshal(pair)
	if err != nil {
		return
	}
	values["pair"] = string(out)
}

// localEquivalentProviders enumerates the identity.provider values that
// resolve to a pika-managed local user (not an external IdP). Used
// exclusively by resolveSessionUser below to pick the right one-shot
// dispatch at login time — once user_id is resolved and stamped into
// Identity.Claims[PikaUserIDClaim] via stampUserIDClaim, downstream
// per-request consumers (CapResolver) lookup the user by id and never
// re-run this dispatch.
//
//   - "" and "local": classic password login. Empty is the legacy
//     value for sessions persisted before the provider field existed.
//   - "passkey": ada's passkey strategy stamps id.Provider with the
//     configured strategy name, defaulting to "passkey". A successful
//     passkey assertion has already authenticated against an existing
//     pika user (the credential row in `passkeys` is FK'd to
//     `users.id`); there is no external identity to provision. If an
//     operator renames the passkey strategy in settings, sessions for
//     that strategy will fall through to the external-lookup branch
//     and fail closed — which is preferable to silently creating a
//     duplicate user.
var localEquivalentProviders = map[string]struct{}{
	"":        {},
	"local":   {},
	"passkey": {},
}

// isLocalEquivalentProvider is the readable form of the set membership
// check. Kept as a helper so a future need to consult the set from
// outside resolveSessionUser doesn't proliferate inline lookups.
func isLocalEquivalentProvider(provider string) bool {
	_, ok := localEquivalentProviders[provider]
	return ok
}

// resolveSessionUser returns (username, user_id) for the session row. It
// handles both local and external identities, but it never provisions
// new users — that is reserved for the user-sync engine. Unrecognized
// external identities fail closed (session persisted without a
// user_id), surfacing a warning so operators can investigate.
//
//   - Local (provider in localEquivalentProviders): look up the user
//     by username. Missing → return Subject with empty user_id; the
//     session still persists so the cookie remains valid, but admin
//     flows that depend on user_id will treat it as unbound.
//
//   - External: call FindExternalUser, which resolves (provider,
//     subject) to an existing link or auto-links by verified email
//     against an existing local user. ErrNotFound means no
//     pre-existing user — DO NOT create one. Operators must
//     pre-provision external users via user sync or the admin Users
//     page.
//
// A best-effort failure (e.g. identity has no Subject or the DB lookup
// errors) yields empty strings so the session still persists. The auth
// middleware will still authenticate the user on next request (the cookie
// and stored pair are intact); only admin flows are affected.
func (s *SessionStore) resolveSessionUser(ctx context.Context, values map[string]any) (string, string) {
	id := extractIdentity(values)
	if id.Subject == "" {
		return "", ""
	}

	// Local-equivalent: bind to the existing pika user by username.
	// No provisioning — local users are always created ahead of time
	// through the admin /users flow, and passkey users authenticate
	// against an already-existing local row by definition.
	if isLocalEquivalentProvider(id.Provider) {
		info, err := s.svc.GetUserByUsername(ctx, id.Subject)
		if err == nil && info != nil {
			return info.Username, info.ID
		}
		return id.Subject, ""
	}

	// External: lookup-only. An unrecognized identity is logged and
	// the session is persisted unbound — never auto-created. The
	// user-sync engine (internal/usersync) is the only path
	// permitted to materialize new users from external sources.
	info, err := s.svc.FindExternalUser(ctx, service.ExternalIdentityInput{
		Provider:      id.Provider,
		Subject:       id.Subject,
		Email:         id.Email,
		EmailVerified: id.EmailVerified,
		DisplayName:   id.Name,
		Username:      id.Name, // username hint, only consumed when verified-email auto-link fires
	})
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			// Fail-closed: surface a clear hint that the user must be
			// pre-provisioned, then let the session persist unbound.
			slog.Warn("authx: external identity has no matching pika user; sync the user first",
				"provider", id.Provider,
				"subject", id.Subject,
			)
		} else {
			slog.Warn("authx: failed to resolve external user",
				"provider", id.Provider,
				"subject", id.Subject,
				"error", err.Error(),
			)
		}
		return id.Subject, ""
	}
	return info.Username, info.ID
}

func optPath(o *sessionstore.Options) string {
	if o != nil && o.Path != "" {
		return o.Path
	}
	return "/"
}

func optDomain(o *sessionstore.Options) string {
	if o != nil {
		return o.Domain
	}
	return ""
}

func optMaxAge(o *sessionstore.Options) int {
	if o != nil {
		return o.MaxAge
	}
	return 0
}

func optSameSite(o *sessionstore.Options) http.SameSite {
	if o != nil {
		return o.SameSite
	}
	return http.SameSiteLaxMode
}

// ForContext is a convenience for tests.
func (s *SessionStore) ForContext(_ context.Context) *SessionStore { return s }

// Interface compliance.
var _ sessionstore.Store = (*SessionStore)(nil)
