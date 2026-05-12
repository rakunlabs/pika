package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rakunlabs/ada/middleware/auth/identity"
	"github.com/rakunlabs/ada/middleware/auth/strategy/passkey"
)

// PasskeyService coordinates WebAuthn flows on top of the underlying
// passkey storage. It owns the in-process challenge store used to bind
// a begin response to the matching finish call, and it knows how to
// translate pika identities ↔ passkey-package types.
//
// The split between *Service (general pika service) and PasskeyService
// keeps the WebAuthn-specific dependencies (ada/passkey) contained:
// the rest of the service layer never imports the passkey package
// directly. Wire-up happens in authx.BuildPasskey.
type PasskeyService struct {
	svc    *Service
	engine *passkey.WebAuthn

	// regChallenges holds enrollment sessions keyed by the opaque id
	// handed to the SPA. Separate from the login-side challenge store
	// (which lives inside the strategy) so enrollment and login flows
	// can't collide on session IDs.
	regMu         sync.Mutex
	regChallenges map[string]*passkeyRegEntry

	// challengeTTL is how long a begin-issued challenge stays valid.
	// Mirrored from the WebAuthn config so the GC tick can evict
	// without a separate config knob.
	challengeTTL time.Duration
}

type passkeyRegEntry struct {
	session *passkey.SessionData
	userID  string
	expires time.Time
}

// NewPasskeyService wires a WebAuthn engine onto the service. Callers
// that don't intend to use passkeys can leave this nil — the Service
// degrades gracefully (passkey endpoints return 503).
func NewPasskeyService(svc *Service, engine *passkey.WebAuthn, challengeTTL time.Duration) *PasskeyService {
	if challengeTTL <= 0 {
		challengeTTL = 5 * time.Minute
	}
	ps := &PasskeyService{
		svc:           svc,
		engine:        engine,
		regChallenges: make(map[string]*passkeyRegEntry),
		challengeTTL:  challengeTTL,
	}
	go ps.gcLoop()
	return ps
}

// SetPasskeyService attaches a PasskeyService to a parent Service.
// The helper keeps the wiring explicit at boot rather than relying
// on package-level globals.
func (s *Service) SetPasskeyService(ps *PasskeyService) {
	s.passkeys = ps
}

// PasskeyCoord returns the bound PasskeyService, or nil when
// WebAuthn is disabled for this deployment. Callers MUST nil-check.
// Named "Coord" (coordinator) to disambiguate from the storage-side
// Passkeys() on the Storage interface.
func (s *Service) PasskeyCoord() *PasskeyService {
	return s.passkeys
}

// PasskeyStore returns the underlying credential storage. Exposed so
// callers that need direct row access (tests, the user-delete cascade
// in storage layer doesn't go through this path) can reach the
// bucket without dragging in the full PasskeyService.
func (s *Service) PasskeyStore() PasskeyStorage {
	return s.store.Passkeys()
}

// BeginEnroll starts a registration ceremony for the given user. The
// returned options are JSON-encoded and sent to the SPA verbatim;
// sessionID must be echoed back on FinishEnroll.
//
// We pass the user's existing credentials as exclude list so the
// authenticator can refuse to enroll the same device twice. Some
// browsers honor this and show a friendly "already enrolled" UI;
// others ignore it and let the user re-enroll, in which case the
// finish path catches the collision on credential_id (unique
// constraint).
func (ps *PasskeyService) BeginEnroll(ctx context.Context, userID string) (sessionID string, opts *passkey.CredentialCreationOptions, err error) {
	if ps == nil || ps.engine == nil {
		return "", nil, ErrNoStorageBackend
	}
	if userID == "" {
		return "", nil, fmt.Errorf("user id required: %w", ErrBadRequest)
	}

	user, err := ps.svc.GetUser(ctx, userID)
	if err != nil {
		return "", nil, err
	}
	if user.Disabled {
		return "", nil, fmt.Errorf("user is disabled: %w", ErrForbidden)
	}

	existing, err := ps.svc.store.Passkeys().ListByUserID(ctx, userID)
	if err != nil {
		return "", nil, err
	}
	exclude := make([]passkey.PublicKeyCredentialDescriptor, 0, len(existing))
	for _, c := range existing {
		exclude = append(exclude, passkey.PublicKeyCredentialDescriptor{
			Type:       "public-key",
			ID:         passkey.Base64URLEncode(c.CredentialID),
			Transports: c.Transports,
		})
	}

	opts, session, err := ps.engine.BeginRegistration(passkey.User{
		Handle:      userIDToHandle(userID),
		Name:        user.Username,
		DisplayName: displayNameOrUsername(user),
	}, exclude)
	if err != nil {
		return "", nil, fmt.Errorf("passkey begin enroll: %w", err)
	}

	sessionID = newSessionID()
	ps.regMu.Lock()
	ps.regChallenges[sessionID] = &passkeyRegEntry{
		session: session,
		userID:  userID,
		expires: time.Now().Add(ps.challengeTTL),
	}
	ps.regMu.Unlock()

	return sessionID, opts, nil
}

// FinishEnroll verifies the registration response, stores the
// resulting credential, and returns the persisted row. name is
// optional — empty triggers a "Passkey N" auto-label.
func (ps *PasskeyService) FinishEnroll(ctx context.Context, userID, sessionID, name string, body []byte) (*PasskeyCredential, error) {
	if ps == nil || ps.engine == nil {
		return nil, ErrNoStorageBackend
	}
	if userID == "" || sessionID == "" {
		return nil, fmt.Errorf("user id and session id required: %w", ErrBadRequest)
	}

	ps.regMu.Lock()
	entry, ok := ps.regChallenges[sessionID]
	if ok {
		// One-shot: drop the entry eagerly so a replay can't reuse
		// it even if the rest of verification fails.
		delete(ps.regChallenges, sessionID)
	}
	ps.regMu.Unlock()

	if !ok {
		return nil, fmt.Errorf("unknown session: %w", ErrUnauthorized)
	}
	if entry.userID != userID {
		// Session belongs to a different user — likely cross-user
		// session smuggling. Reject with a generic message.
		return nil, fmt.Errorf("session does not belong to user: %w", ErrUnauthorized)
	}
	if time.Now().After(entry.expires) {
		return nil, fmt.Errorf("session expired: %w", ErrUnauthorized)
	}

	cred, attResult, err := ps.engine.FinishRegistration(entry.session, body)
	if err != nil {
		return nil, fmt.Errorf("passkey finish enroll: %w", err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)

	// Auto-label uses the count of existing rows so the label is
	// stable and human-friendly. If the user explicitly supplied a
	// name we trim and cap its length.
	finalName := strings.TrimSpace(name)
	if finalName == "" {
		existing, _ := ps.svc.store.Passkeys().ListByUserID(ctx, userID)
		finalName = fmt.Sprintf("Passkey %d", len(existing)+1)
	}
	if len(finalName) > 64 {
		finalName = finalName[:64]
	}

	row := &PasskeyCredential{
		ID:              newCredentialID(),
		UserID:          userID,
		CredentialID:    cred.ID,
		PublicKey:       cred.PublicKey,
		AAGUID:          cred.AAGUID,
		SignCount:       cred.SignCount,
		Transports:      cred.Transports,
		UserVerified:    cred.UserVerified,
		BackupEligible:  cred.BackupEligible,
		BackupState:     cred.BackupState,
		AttestationType: attResult.AttestationType,
		Name:            finalName,
		CreatedAt:       now,
	}
	if err := ps.svc.store.Passkeys().Create(ctx, row); err != nil {
		return nil, err
	}
	return row, nil
}

// ListUserPasskeys returns the user's enrolled credentials, newest
// first. The raw PublicKey bytes are zeroed before return — the API
// surface never needs them and the JSON tag is "-" anyway, but
// nilling here avoids accidentally serializing them via reflection.
func (ps *PasskeyService) ListUserPasskeys(ctx context.Context, userID string) ([]PasskeyCredential, error) {
	if userID == "" {
		return nil, fmt.Errorf("user id required: %w", ErrBadRequest)
	}
	rows, err := ps.svc.store.Passkeys().ListByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	// Sort newest-first. ListByUserID is currently unsorted; doing
	// the sort here keeps the contract stable regardless of storage
	// backend ordering.
	for i := 0; i < len(rows); i++ {
		for j := i + 1; j < len(rows); j++ {
			if rows[j].CreatedAt.After(rows[i].CreatedAt) {
				rows[i], rows[j] = rows[j], rows[i]
			}
		}
	}
	for i := range rows {
		rows[i].PublicKey = nil
	}
	return rows, nil
}

// RenamePasskey updates the user-visible label on a credential. The
// row's ownership is checked — a user can only rename their own
// credentials, not someone else's even if they know the id.
func (ps *PasskeyService) RenamePasskey(ctx context.Context, userID, credID, newName string) (*PasskeyCredential, error) {
	if userID == "" || credID == "" {
		return nil, fmt.Errorf("user id and credential id required: %w", ErrBadRequest)
	}
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return nil, fmt.Errorf("name must be non-empty: %w", ErrBadRequest)
	}
	if len(newName) > 64 {
		newName = newName[:64]
	}

	row, err := ps.svc.store.Passkeys().Get(ctx, credID)
	if err != nil {
		return nil, err
	}
	if row.UserID != userID {
		return nil, fmt.Errorf("credential does not belong to user: %w", ErrForbidden)
	}

	row.Name = newName
	if err := ps.svc.store.Passkeys().Update(ctx, row); err != nil {
		return nil, err
	}
	row.PublicKey = nil
	return row, nil
}

// DeletePasskey removes one of the user's credentials. Ownership is
// enforced as in RenamePasskey. Deleting the last credential is
// allowed — pika doesn't currently force a minimum number of
// credentials per user.
func (ps *PasskeyService) DeletePasskey(ctx context.Context, userID, credID string) error {
	if userID == "" || credID == "" {
		return fmt.Errorf("user id and credential id required: %w", ErrBadRequest)
	}
	row, err := ps.svc.store.Passkeys().Get(ctx, credID)
	if err != nil {
		return err
	}
	if row.UserID != userID {
		return fmt.Errorf("credential does not belong to user: %w", ErrForbidden)
	}
	return ps.svc.store.Passkeys().Delete(ctx, credID)
}

// LookupForLogin resolves a credential id (the rawId field from a
// login assertion) to the passkey package's typed Credential plus
// the pika identity that should be issued when verification passes.
//
// Returns passkey.ErrCredentialNotFound when the row is missing or
// belongs to a disabled user — the strategy translates either into
// a uniform 401 so an attacker can't enumerate credentials by
// timing.
func (ps *PasskeyService) LookupForLogin(ctx context.Context, credentialID []byte) (*passkey.Credential, *identity.Identity, error) {
	row, err := ps.svc.store.Passkeys().FindByCredentialID(ctx, credentialID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil, passkey.ErrCredentialNotFound
		}
		return nil, nil, err
	}
	user, err := ps.svc.GetUser(ctx, row.UserID)
	if err != nil {
		return nil, nil, passkey.ErrCredentialNotFound
	}
	if user.Disabled {
		return nil, nil, passkey.ErrCredentialNotFound
	}

	cred := &passkey.Credential{
		ID:              row.CredentialID,
		UserHandle:      userIDToHandle(row.UserID),
		PublicKey:       row.PublicKey,
		AAGUID:          row.AAGUID,
		SignCount:       row.SignCount,
		Transports:      row.Transports,
		AttestationType: row.AttestationType,
		BackupEligible:  row.BackupEligible,
		BackupState:     row.BackupState,
		UserVerified:    row.UserVerified,
	}

	id := &identity.Identity{
		Subject:  user.Username,
		Name:     displayNameOrUsername(user),
		Email:    user.Email,
		Provider: "passkey",
	}

	// Side-effect: bump LastUsedAt opportunistically so the security
	// page can show "last used 3 days ago". We do this here (not at
	// the strategy's sign-count callback) because LastUsedAt updates
	// even when the counter doesn't change — many platform
	// authenticators stay at 0.
	row.LastUsedAt = time.Now().UTC().Truncate(time.Microsecond)
	if err := ps.svc.store.Passkeys().Update(ctx, row); err != nil {
		// Don't fail the login — losing one timestamp update is
		// not worth blocking auth.
		_ = err
	}

	return cred, id, nil
}

// UpdateSignCount persists a new sign counter after a successful
// login. The strategy invokes this via the SignCountUpdater option.
// Best-effort: errors are logged at the caller; we still return them
// for visibility.
func (ps *PasskeyService) UpdateSignCount(ctx context.Context, credentialID []byte, newCount uint32) error {
	row, err := ps.svc.store.Passkeys().FindByCredentialID(ctx, credentialID)
	if err != nil {
		return err
	}
	row.SignCount = newCount
	return ps.svc.store.Passkeys().Update(ctx, row)
}

// gcLoop sweeps expired registration challenges. The strategy has its
// own GC for login challenges; this one covers the enrollment side.
func (ps *PasskeyService) gcLoop() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for range t.C {
		now := time.Now()
		ps.regMu.Lock()
		for id, e := range ps.regChallenges {
			if now.After(e.expires) {
				delete(ps.regChallenges, id)
			}
		}
		ps.regMu.Unlock()
	}
}

// userIDToHandle maps a pika user id (hex string) to the byte slice
// used as the WebAuthn user handle. Stable across logins for the
// lifetime of the user row — that's what the WebAuthn spec requires.
//
// We pass the raw bytes (max 32 for a hex-32 user id) rather than
// decoded hex bytes, so a future migration to a different user-id
// shape doesn't break existing enrolled passkeys. The credential
// itself binds to these exact bytes inside the authenticator.
func userIDToHandle(userID string) []byte {
	return []byte(userID)
}

// displayNameOrUsername returns DisplayName when set, else Username —
// the WebAuthn user entity expects a human-meaningful displayName.
func displayNameOrUsername(u *UserInfo) string {
	if u.DisplayName != "" {
		return u.DisplayName
	}
	return u.Username
}

// newSessionID returns a 16-byte hex-encoded random opaque id used
// as the key for the in-process challenge store.
func newSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// rand failure is catastrophic — every other security
		// primitive in pika also panics here.
		panic(fmt.Errorf("passkey: rng failure: %w", err))
	}
	return hex.EncodeToString(b)
}

// newCredentialID returns a 16-byte hex-encoded random row id for a
// PasskeyCredential. Distinct from the WebAuthn credential_id; this
// is purely pika's storage PK so a credential can be renamed without
// disturbing the lookup key the authenticator emits.
func newCredentialID() string {
	return newSessionID()
}
