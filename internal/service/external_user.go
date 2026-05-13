package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"
)

// ExternalIdentityInput carries what a strategy learned about a user from
// an external IdP (OAuth2, LDAP, Header). The service layer maps this onto
// a pika users row + user_identities link.
type ExternalIdentityInput struct {
	Provider      string // stable provider name, e.g. "google"
	Subject       string // provider's stable user ID (OIDC `sub`)
	Email         string // IdP-asserted email
	EmailVerified bool   // IdP assertion that the email is verified
	DisplayName   string // friendly name (e.g. OIDC `name` claim)
	Username      string // preferred username hint from the provider
}

// FindExternalUser resolves an external identity to an EXISTING pika
// user without provisioning a new one. This is the lookup-only sibling
// of FindOrCreateExternalUser, intended for the live authentication
// path (session save, capability resolver) where silent account
// creation is undesirable: an unrecognized identity should fail
// closed, not mint a brand-new users row.
//
// Resolution order:
//
//  1. (provider, subject) already linked → return that user. Refresh
//     the identity snapshot (email, display name) so a renamed
//     external account stays in sync on next login.
//  2. Auto-link by verified email — only when LinkByVerifiedEmail is
//     enabled, the IdP asserts email_verified=true and a local users
//     row already exists at that email. This adds a user_identities
//     row but never touches the users table itself, so it does not
//     violate the "no new users via auth" invariant.
//
// Returns ErrNotFound when neither path matches. Callers in the auth
// path use this to bind sessions only to pre-existing users; the
// user-sync engine is the only legitimate caller that goes further and
// invokes FindOrCreateExternalUser to provision missing users.
func (s *Service) FindExternalUser(ctx context.Context, in ExternalIdentityInput) (*UserInfo, error) {
	if in.Provider == "" || in.Subject == "" {
		return nil, fmt.Errorf("provider and subject are required: %w", ErrBadRequest)
	}

	normalizedEmail := normalizeEmail(in.Email)

	// Step 1: existing (provider, subject) link.
	if existingLink, err := s.store.UserIdentities().FindByProviderSubject(ctx, in.Provider, in.Subject); err == nil && existingLink != nil {
		user, err := s.store.Users().Get(ctx, existingLink.UserID)
		if err != nil {
			return nil, fmt.Errorf("load linked user %q: %w", existingLink.UserID, err)
		}
		if user.Disabled {
			return nil, fmt.Errorf("linked user is disabled: %w", ErrForbidden)
		}
		// Refresh the identity snapshot — the email or display name may
		// have changed at the IdP since last login.
		_, _ = s.store.UserIdentities().Upsert(ctx, &UserIdentity{
			ID:          existingLink.ID,
			UserID:      existingLink.UserID,
			Provider:    in.Provider,
			Subject:     in.Subject,
			Email:       normalizedEmail,
			DisplayName: in.DisplayName,
		})
		info := user.toInfo()
		return &info, nil
	} else if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("lookup identity: %w", err)
	}

	// Step 2: auto-link by verified email — only LINKS, never creates a user.
	authSettings := s.GetAuthSettings(ctx)
	if authSettings.LinkByVerifiedEmailEnabled() && in.EmailVerified && normalizedEmail != "" {
		if existingUser, err := s.store.Users().GetByEmail(ctx, normalizedEmail); err == nil && existingUser != nil {
			if existingUser.Disabled {
				return nil, fmt.Errorf("user matching verified email is disabled: %w", ErrForbidden)
			}
			if _, err := s.store.UserIdentities().Upsert(ctx, &UserIdentity{
				UserID:      existingUser.ID,
				Provider:    in.Provider,
				Subject:     in.Subject,
				Email:       normalizedEmail,
				DisplayName: in.DisplayName,
			}); err != nil {
				return nil, fmt.Errorf("link identity to existing user: %w", err)
			}
			slog.Info("auth: linked external identity to existing user by verified email",
				"provider", in.Provider,
				"subject", in.Subject,
				"user_id", existingUser.ID,
				"username", existingUser.Username,
			)
			info := existingUser.toInfo()
			return &info, nil
		} else if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("lookup by email: %w", err)
		}
	}

	return nil, ErrNotFound
}

// FindOrCreateExternalUser is the provisioning variant of
// FindExternalUser. It first attempts the lookup-only resolution and,
// only on ErrNotFound, provisions a brand-new external users row plus
// identity link. The call is idempotent: calling it twice for the same
// (provider, subject) returns the same user.
//
// Provisioning step: username is generated from the provider's hints
// (preferred_username → email local part → "<provider>:<subject>")
// with collision-suffixing.
//
// IMPORTANT: this method is the only auth-path entry point that can
// create users, and is reserved for the user-sync engine
// (`internal/usersync`). The live auth flow (session save, login
// callbacks) MUST use FindExternalUser so that unrecognized identities
// fail closed instead of silently provisioning. See sessionstore.go
// resolveSessionUser for the call site that enforces this.
func (s *Service) FindOrCreateExternalUser(ctx context.Context, in ExternalIdentityInput) (*UserInfo, error) {
	if info, err := s.FindExternalUser(ctx, in); err == nil {
		return info, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	// Provision: only reachable when FindExternalUser returned
	// ErrNotFound. Both identifier validation and the (provider,
	// subject) / verified-email lookups already happened inside the
	// nested call.
	normalizedEmail := normalizeEmail(in.Email)

	username, err := s.chooseExternalUsername(ctx, in)
	if err != nil {
		return nil, err
	}

	userID, err := generateUserID()
	if err != nil {
		return nil, fmt.Errorf("generate user id: %w", err)
	}
	now := time.Now()

	user := User{
		ID:           userID,
		Username:     username,
		PasswordHash: "", // external-only users have no local password
		Email:        normalizedEmail,
		DisplayName:  in.DisplayName,
		External:     true,
		Disabled:     false,
		IsSuperadmin: false,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := s.store.Users().Create(ctx, &user); err != nil {
		return nil, fmt.Errorf("create external user: %w", err)
	}

	if _, err := s.store.UserIdentities().Upsert(ctx, &UserIdentity{
		UserID:      user.ID,
		Provider:    in.Provider,
		Subject:     in.Subject,
		Email:       normalizedEmail,
		DisplayName: in.DisplayName,
	}); err != nil {
		// Identity insert failed but user was created; orphan row remains.
		// Deliberately not rolling back — leaving the user in place means
		// a retry will take the FindByProviderSubject fast-path above
		// once the identity finally persists.
		return nil, fmt.Errorf("link identity to new user: %w", err)
	}

	slog.Info("auth: provisioned new external user",
		"provider", in.Provider,
		"subject", in.Subject,
		"user_id", user.ID,
		"username", user.Username,
	)

	info := user.toInfo()
	return &info, nil
}

// GetUserIdentities returns every identity linked to a user.
func (s *Service) GetUserIdentities(ctx context.Context, userID string) ([]UserIdentity, error) {
	return s.store.UserIdentities().ListByUserID(ctx, userID)
}

// ListIdentitiesByProvider returns every identity issued by a given provider
// (typically a sync source ID). Used by the user-sync reconciliation pass.
func (s *Service) ListIdentitiesByProvider(ctx context.Context, provider string) ([]UserIdentity, error) {
	return s.store.UserIdentities().ListByProvider(ctx, provider)
}

// GetUserByID is the public-shaped (UserInfo) accessor for the typed user
// row. Used by the sync engine and any other callers that need the
// projection without having to call store.Users() directly.
func (s *Service) GetUserByID(ctx context.Context, id string) (*UserInfo, error) {
	user, err := s.store.Users().Get(ctx, id)
	if err != nil {
		return nil, err
	}
	info := user.toInfo()
	return &info, nil
}

// SetUserPermissionsBySource replaces only the user_permissions rows
// tagged with the given source. Used by the sync engine so a sync run
// owns its own grants without trampling admin-curated 'local' rows.
func (s *Service) SetUserPermissionsBySource(ctx context.Context, userID, source string, permissionIDs []string) error {
	return s.store.Permissions().SetUserPermissionsBySource(ctx, userID, source, permissionIDs)
}

// GetUserByIdentity resolves a (provider, subject) pair to the pika user
// it's linked to. Returns ErrNotFound when no link exists. Used by the
// capability resolver on protected-request paths — strictly lookup-only,
// like FindExternalUser, with no auto-link or provisioning side effects.
func (s *Service) GetUserByIdentity(ctx context.Context, provider, subject string) (*UserInfo, error) {
	if provider == "" || subject == "" {
		return nil, ErrNotFound
	}
	link, err := s.store.UserIdentities().FindByProviderSubject(ctx, provider, subject)
	if err != nil {
		return nil, err
	}
	user, err := s.store.Users().Get(ctx, link.UserID)
	if err != nil {
		return nil, err
	}
	info := user.toInfo()
	return &info, nil
}

// UnlinkUserIdentity removes a single (provider, subject) link from a user.
// Used by admin "remove linked account" operations. Does NOT delete the
// underlying user row even if it leaves the user with zero identities —
// that is the operator's call.
func (s *Service) UnlinkUserIdentity(ctx context.Context, identityID string) error {
	return s.store.UserIdentities().Delete(ctx, identityID)
}

// chooseExternalUsername picks a non-colliding username for a freshly-
// provisioned external user. Preference order:
//  1. Provider-asserted preferred_username / display hint (in.Username)
//  2. Email local-part
//  3. "<provider>:<subject>" last-resort
//
// The base is sanitized (lowercased, non-[a-z0-9_-] replaced with "_") then
// suffixed with a counter until it's unique. Hard cap at 32 characters so
// UIs don't overflow.
func (s *Service) chooseExternalUsername(ctx context.Context, in ExternalIdentityInput) (string, error) {
	candidates := []string{
		sanitizeUsername(in.Username),
		sanitizeUsername(emailLocalPart(in.Email)),
		sanitizeUsername(in.Provider + ":" + in.Subject),
	}

	var base string
	for _, c := range candidates {
		if c != "" {
			base = c
			break
		}
	}
	if base == "" {
		// extremely defensive — both sanitize paths stripped everything
		base = "user"
	}

	for i := 0; i < 100; i++ {
		candidate := base
		if i > 0 {
			candidate = fmt.Sprintf("%s_%d", base, i+1)
		}
		if len(candidate) > 32 {
			candidate = candidate[:32]
		}
		_, err := s.store.Users().GetByUsername(ctx, candidate)
		if errors.Is(err, ErrNotFound) {
			return candidate, nil
		}
		if err != nil {
			return "", fmt.Errorf("check username availability: %w", err)
		}
	}
	return "", fmt.Errorf("could not find free username within 100 attempts: %w", ErrConflict)
}

// sanitizeUsername lowercases and strips characters that aren't [a-z0-9_-],
// collapsing runs of replaced characters into a single underscore.
func sanitizeUsername(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return ""
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_'
		if ok {
			b.WriteRune(r)
			lastUnderscore = false
		} else if !lastUnderscore && unicode.IsPrint(r) {
			b.WriteRune('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_-")
}

// emailLocalPart returns everything before the "@" of an email, or "" if
// the input doesn't contain one.
func emailLocalPart(email string) string {
	at := strings.IndexByte(email, '@')
	if at <= 0 {
		return ""
	}
	return email[:at]
}

// normalizeEmail trims and lowercases. Email RFC lets the local part be
// case-sensitive, but treating it case-insensitively is the pragmatic
// default for user-matching and matches what every mainstream IdP does.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
