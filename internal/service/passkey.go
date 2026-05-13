package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rakunlabs/ada/middleware/auth/identity"
	"github.com/rakunlabs/ada/middleware/auth/strategy/passkey"
)

// PasskeyService coordinates WebAuthn flows on top of the underlying
// passkey storage. It is the translator between pika identities ↔
// passkey-package types, and the home of the registration ceremony's
// state (login-side challenges live inside the ada strategy itself).
//
// Ceremony state — both enrollment and login challenges — is
// persisted through the bw cluster (via PasskeyChallengeStorage) so a
// begin call on one node and the corresponding finish on a different
// node both see the same row. Per-instance in-memory maps were the
// previous design and didn't work in multi-instance deployments.
//
// The split between *Service (general pika service) and PasskeyService
// keeps the WebAuthn-specific dependencies (ada/passkey) contained:
// the rest of the service layer never imports the passkey package
// directly. Wire-up happens in authx.BuildPasskey.
type PasskeyService struct {
	svc    *Service
	engine *passkey.WebAuthn

	// challengeTTL is how long a begin-issued challenge stays valid.
	// Mirrored from the WebAuthn config so the GC tick and the
	// enrollment-session save both pick up the same value.
	challengeTTL time.Duration

	// lastUsedCh is a buffered queue of LastUsedAt bumps that the
	// hot login path hands off to the lastUsedLoop goroutine. Doing
	// the DB write inline would add a synchronous round-trip to
	// every passkey login; coalescing per-row in the background
	// keeps SQLite contention down and trims login latency at the
	// cost of a small staleness window (≤ 2 s, see lastUsedLoop).
	// The channel is buffered and we drop on full: losing one
	// LastUsedAt update is preferable to blocking a login on a
	// slow background flusher.
	lastUsedCh chan lastUsedEvent

	// flushReqCh lets callers (mainly tests, but also a future
	// shutdown hook) drain the pending batch synchronously.
	flushReqCh chan flushReq

	// gcOnce gates the lazy-launch of the periodic sweep so tests
	// that spin up many short-lived PasskeyService instances don't
	// fork a new goroutine each time. Background sweeps still run
	// on every instance but only one per process per service.
	gcOnce sync.Once
}

// challengeKind values distinguish enrollment from login challenges
// in the persisted bucket. Both flows use the same row shape; the
// kind tag is metadata for audit logs and the cross-user-smuggling
// check in FinishEnroll.
const (
	challengeKindEnroll = "enroll"
	challengeKindLogin  = "login"
)

// lastUsedEvent is a single LastUsedAt bump enqueued by LookupForLogin.
// Coalesced by rowID inside the loop, so two logins to the same
// credential within a flush interval cost one DB write, not two.
type lastUsedEvent struct {
	rowID string
	at    time.Time
}

// flushReq is the message FlushLastUsed sends to drain the pending
// batch synchronously. The loop closes done once persistence is
// complete.
type flushReq struct {
	done chan struct{}
}

const (
	// lastUsedBufferSize bounds the in-flight LastUsedAt queue. At
	// 1024 we'd need >>500 logins/second to saturate it; under that
	// rate the queue is always nearly empty.
	lastUsedBufferSize = 1024
	// lastUsedBatchSize triggers an eager flush when the pending
	// map grows past this many distinct rows. Without it a slow
	// trickle would never hit the timer-based flush ceiling either,
	// but a burst of unique-credential logins would otherwise pile
	// up for the full 2 s.
	lastUsedBatchSize = 128
	// lastUsedFlushInterval is the periodic batch tick. Keeping it
	// short (≤ a few seconds) means the "last seen" column in the
	// security UI lags reality by at most that long.
	lastUsedFlushInterval = 2 * time.Second
)

// NewPasskeyService wires a WebAuthn engine onto the service. Callers
// that don't intend to use passkeys can leave this nil — the Service
// degrades gracefully (passkey endpoints return 503).
func NewPasskeyService(svc *Service, engine *passkey.WebAuthn, challengeTTL time.Duration) *PasskeyService {
	if challengeTTL <= 0 {
		challengeTTL = 5 * time.Minute
	}
	ps := &PasskeyService{
		svc:          svc,
		engine:       engine,
		challengeTTL: challengeTTL,
		lastUsedCh:   make(chan lastUsedEvent, lastUsedBufferSize),
		flushReqCh:   make(chan flushReq),
	}
	go ps.gcLoop()
	go ps.lastUsedLoop()
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

// PasskeyChallengeStore returns the cluster-aware bucket the ada
// passkey strategy uses for in-flight login ceremony sessions. The
// authx wiring layer adapts this onto the ada ChallengeStore
// interface; nothing else should need to reach into the bucket
// directly.
func (s *Service) PasskeyChallengeStore() PasskeyChallengeStorage {
	return s.store.PasskeyChallenges()
}

// EnrollOptions tunes a single BeginEnroll call. All fields are
// optional; zero values produce the default behavior (any
// authenticator).
type EnrollOptions struct {
	// AuthenticatorAttachment scopes the ceremony to a class of
	// device — "platform" (built-in: Touch ID / Hello / Android
	// keystore) or "cross-platform" (roaming: USB/NFC/BLE security
	// key). Any other value (including the empty string) lets the
	// browser show the chooser. We don't return an error for
	// invalid input here — the ada layer normalizes it to "" so
	// the ceremony still works.
	AuthenticatorAttachment string
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
//
// opts is optional — passing nil yields the same behavior as
// before. It's a struct rather than variadic options so future
// fields (e.g. ResidentKey) don't require adding another With…
// helper for every caller layer.
func (ps *PasskeyService) BeginEnroll(ctx context.Context, userID string, opts *EnrollOptions) (sessionID string, options *passkey.CredentialCreationOptions, err error) {
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

	var regOpts []passkey.RegistrationOption
	if opts != nil && opts.AuthenticatorAttachment != "" {
		regOpts = append(regOpts, passkey.WithAuthenticatorAttachment(opts.AuthenticatorAttachment))
	}

	options, session, err := ps.engine.BeginRegistration(passkey.User{
		Handle:      userIDToHandle(userID),
		Name:        user.Username,
		DisplayName: displayNameOrUsername(user),
	}, exclude, regOpts...)
	if err != nil {
		return "", nil, fmt.Errorf("passkey begin enroll: %w", err)
	}

	// Persist the enrollment session through the cluster-aware
	// bucket so the matching finish call can land on any node.
	sessionID = newSessionID()
	blob, err := json.Marshal(session)
	if err != nil {
		return "", nil, fmt.Errorf("passkey marshal session: %w", err)
	}
	if err := ps.svc.store.PasskeyChallenges().Save(ctx, &PasskeyChallenge{
		ID:        sessionID,
		Kind:      challengeKindEnroll,
		UserID:    userID,
		Data:      blob,
		ExpiresAt: time.Now().Add(ps.challengeTTL),
	}); err != nil {
		return "", nil, fmt.Errorf("passkey save challenge: %w", err)
	}

	return sessionID, options, nil
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

	entry, err := ps.svc.store.PasskeyChallenges().Get(ctx, sessionID)
	if err != nil {
		// bw returns ErrNotFound for an unknown id; map to a generic
		// 401 so an attacker can't probe valid session ids by
		// timing.
		return nil, fmt.Errorf("unknown session: %w", ErrUnauthorized)
	}
	// One-shot: drop the row eagerly so a replay can't reuse it even
	// if the rest of verification fails. We delete before any other
	// check both to minimize the replay window and to keep the
	// cleanup unconditional — even a cross-user-smuggling attempt
	// burns the session id.
	_ = ps.svc.store.PasskeyChallenges().Delete(ctx, sessionID)

	if entry.Kind != challengeKindEnroll {
		// Cross-purpose smuggling (e.g. login challenge handed to
		// the finish-enroll endpoint). Reject with the same generic
		// message as "unknown session" to avoid leaking shape.
		return nil, fmt.Errorf("session is not an enrollment: %w", ErrUnauthorized)
	}
	if entry.UserID != userID {
		// Session belongs to a different user — likely cross-user
		// session smuggling. Reject with a generic message.
		return nil, fmt.Errorf("session does not belong to user: %w", ErrUnauthorized)
	}
	if time.Now().After(entry.ExpiresAt) {
		return nil, fmt.Errorf("session expired: %w", ErrUnauthorized)
	}

	var session passkey.SessionData
	if err := json.Unmarshal(entry.Data, &session); err != nil {
		// Persisted blob is corrupt. Surface as "unknown session" to
		// the client; log loudly so operators can investigate.
		slog.Warn("passkey: corrupt enrollment session", "id", sessionID, "error", err)
		return nil, fmt.Errorf("unknown session: %w", ErrUnauthorized)
	}

	cred, attResult, err := ps.engine.FinishRegistration(&session, body)
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
	// backend ordering. sort.Slice keeps the cost at O(n log n)
	// rather than the O(n²) pairwise compare we had originally —
	// most users have only a handful of passkeys, but doing the
	// right thing here is free.
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].CreatedAt.After(rows[j].CreatedAt)
	})
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
	//
	// The bump is enqueued to the background batch flusher instead
	// of issued inline so we don't add a DB write to every login.
	// Dropping on full (default branch) preserves login throughput
	// even if the flusher temporarily backs up.
	select {
	case ps.lastUsedCh <- lastUsedEvent{rowID: row.ID, at: time.Now().UTC().Truncate(time.Microsecond)}:
	default:
		// Queue full — fine; the next login will requeue.
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

// LookupCredentialIDs returns the credential ids enrolled for the
// user identified by handle or username. Used by the ada/passkey
// strategy to scope the assertion ceremony to the user's own
// passkeys (the "username-first" login flow): the SPA sends
// { username: "alice" }, this method resolves alice → user_id →
// credential ids, and the strategy puts them in allowCredentials so
// the platform UI presents only the matching passkey.
//
// Returns (nil, nil) for any unresolvable hint (unknown user,
// disabled account, etc.) so the ceremony falls back to discoverable
// without leaking the mapping shape — an attacker submitting
// arbitrary usernames can't tell which exist by timing the response.
// Genuine storage failures bubble up as errors; the strategy logs
// them at warn and still falls back to discoverable rather than
// failing the login outright.
func (ps *PasskeyService) LookupCredentialIDs(ctx context.Context, handle []byte, username string) ([][]byte, error) {
	if ps == nil || ps.engine == nil {
		return nil, nil
	}

	var userID string
	switch {
	case len(handle) > 0:
		// userIDToHandle stores the userID hex string verbatim as
		// raw bytes, so reversing the encoding is a plain cast.
		// Any future change to userIDToHandle must update this side
		// in lockstep.
		userID = string(handle)
	case username != "":
		u, err := ps.svc.GetUserByUsername(ctx, username)
		if err != nil {
			// Most likely "user not found". Treat as soft-miss so
			// the front-end can't enumerate the user table.
			return nil, nil
		}
		if u.Disabled {
			return nil, nil
		}
		userID = u.ID
	default:
		return nil, nil
	}

	rows, err := ps.svc.store.Passkeys().ListByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	out := make([][]byte, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.CredentialID)
	}
	return out, nil
}

// gcLoop sweeps expired challenge rows from the bw-backed bucket.
// Both enrollment and login challenges land in the same bucket, so a
// single sweeper covers both. The interval is intentionally coarse
// (30 s) because each sweep is a bw write — the leader fans it out
// to every follower — and we'd rather pay one cluster round-trip
// every half-minute than one per minute per kind.
//
// On a multi-instance cluster every node runs its own gcLoop. They
// all converge on the same set of rows; bw makes the deletes
// idempotent so the redundancy is harmless. A future optimization
// would be to gate the sweep on cluster leadership, but that adds a
// dependency on the cluster package and is not worth the complexity
// for a 30-second job.
func (ps *PasskeyService) gcLoop() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for range t.C {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		n, err := ps.svc.store.PasskeyChallenges().DeleteExpired(ctx)
		cancel()
		if err != nil {
			slog.Warn("passkey: challenge sweep", "error", err)
			continue
		}
		if n > 0 {
			slog.Debug("passkey: challenge sweep", "removed", n)
		}
	}
}

// lastUsedLoop is the background coalescer for LastUsedAt bumps.
// Three sources can trigger a write:
//
//   - An incoming bump pushes the pending map past lastUsedBatchSize:
//     flush eagerly so a burst of unique-credential logins doesn't
//     pile up to the timer-based ceiling.
//   - The periodic tick fires: flush whatever has accumulated.
//   - A FlushLastUsed caller asks for a synchronous drain: flush and
//     signal completion before returning to the caller.
//
// We deliberately don't pull a context from the bumps themselves —
// the originating request may have completed long before we get to
// the row, and we don't want a cancelled context to drop a legitimate
// write. Each flush attaches its own short-lived background context.
func (ps *PasskeyService) lastUsedLoop() {
	pending := make(map[string]time.Time)
	flush := time.NewTicker(lastUsedFlushInterval)
	defer flush.Stop()

	for {
		select {
		case ev := <-ps.lastUsedCh:
			// Coalesce: a second login to the same credential within
			// the flush window overwrites the timestamp (we want the
			// most recent one).
			pending[ev.rowID] = ev.at
			if len(pending) >= lastUsedBatchSize {
				ps.persistLastUsed(pending)
				pending = make(map[string]time.Time)
			}
		case <-flush.C:
			if len(pending) > 0 {
				ps.persistLastUsed(pending)
				pending = make(map[string]time.Time)
			}
		case req := <-ps.flushReqCh:
			// Drain the channel before persisting so any events that
			// raced the FlushLastUsed call don't get left behind for
			// the next tick. Best-effort: a flood of in-flight bumps
			// could in theory keep us looping, but the caller asked
			// for "everything visible right now" semantics anyway.
		drain:
			for {
				select {
				case ev := <-ps.lastUsedCh:
					pending[ev.rowID] = ev.at
				default:
					break drain
				}
			}
			if len(pending) > 0 {
				ps.persistLastUsed(pending)
				pending = make(map[string]time.Time)
			}
			close(req.done)
		}
	}
}

// persistLastUsed writes the batched timestamps back to the store.
// Errors are intentionally swallowed (logged elsewhere if needed) —
// LastUsedAt is a display-only column and a failed write is no
// worse than a dropped queue entry. We re-fetch each row to avoid
// clobbering any concurrent changes (rename, sign-count updates).
func (ps *PasskeyService) persistLastUsed(pending map[string]time.Time) {
	if len(pending) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for id, t := range pending {
		row, err := ps.svc.store.Passkeys().Get(ctx, id)
		if err != nil {
			continue
		}
		row.LastUsedAt = t
		_ = ps.svc.store.Passkeys().Update(ctx, row)
	}
}

// FlushLastUsed forces a synchronous drain of the LastUsedAt batch.
// Returns once the in-memory queue is empty and the pending writes
// have been persisted. Primarily used by tests that need to assert
// on the column right after a login; it could also be wired into a
// future shutdown hook so the last batch isn't lost on process exit.
//
// Safe to call concurrently with the hot path (the queue and flush
// requests are independent channels). No-op if the service was
// constructed without the loop (e.g. an exotic test that nil'd the
// channel out).
func (ps *PasskeyService) FlushLastUsed() {
	if ps == nil || ps.flushReqCh == nil {
		return
	}
	done := make(chan struct{})
	ps.flushReqCh <- flushReq{done: done}
	<-done
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
