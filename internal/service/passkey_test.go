package service_test

import (
	"errors"
	"testing"
	"time"

	"github.com/rakunlabs/ada/middleware/auth/strategy/passkey"

	"github.com/rakunlabs/pika/internal/service"
)

// newTestPasskeyService builds a PasskeyService wired to a real
// in-memory storage. The WebAuthn engine uses a localhost RP so any
// test that needs to walk through a full ceremony can use the helpers
// from ada/passkey's integration_test for the authenticator side.
// Tests in this file focus on the pika-side bookkeeping (ownership,
// persistence, list ordering, lookup error paths) — full crypto
// round-trip is already covered in ada/passkey/integration_test.go.
func newTestPasskeyService(t *testing.T) (*service.Service, *service.PasskeyService) {
	t.Helper()
	svc := newTestService(t)

	engine, err := passkey.New(&passkey.Config{
		RPID:          "localhost",
		RPDisplayName: "pika test",
		RPOrigins:     []string{"http://localhost"},
	})
	if err != nil {
		t.Fatalf("passkey.New: %v", err)
	}
	ps := service.NewPasskeyService(svc, engine, 5*time.Minute)
	svc.SetPasskeyService(ps)
	return svc, ps
}

// seedPasskeyRow inserts a credential row straight through the
// storage interface, bypassing the ceremony. Useful when we want to
// test list/rename/delete/lookup without performing real WebAuthn
// dance.
func seedPasskeyRow(t *testing.T, svc *service.Service, userID, name string, credentialID, publicKey []byte) *service.PasskeyCredential {
	t.Helper()
	row := &service.PasskeyCredential{
		ID:           "row-" + name,
		UserID:       userID,
		CredentialID: credentialID,
		PublicKey:    publicKey,
		Name:         name,
		CreatedAt:    time.Now().UTC().Truncate(time.Microsecond),
	}
	if err := svc.PasskeyStore().Create(t.Context(), row); err != nil {
		t.Fatalf("seed passkey: %v", err)
	}
	return row
}

func TestPasskey_BeginEnroll_requiresValidUser(t *testing.T) {
	_, ps := newTestPasskeyService(t)

	_, _, err := ps.BeginEnroll(t.Context(), "", nil)
	if !errors.Is(err, service.ErrBadRequest) {
		t.Errorf("empty userID: got %v, want ErrBadRequest", err)
	}

	_, _, err = ps.BeginEnroll(t.Context(), "does-not-exist", nil)
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("unknown user: got %v, want ErrNotFound", err)
	}
}

func TestPasskey_BeginEnroll_returnsSessionAndOptions(t *testing.T) {
	svc, ps := newTestPasskeyService(t)
	uid := createUserHelper(t, svc, "alice")

	sessionID, opts, err := ps.BeginEnroll(t.Context(), uid, nil)
	if err != nil {
		t.Fatalf("BeginEnroll: %v", err)
	}
	if sessionID == "" {
		t.Error("session id empty")
	}
	if opts == nil || opts.Challenge == "" {
		t.Error("options missing challenge")
	}
	if opts.RP.ID != "localhost" {
		t.Errorf("RP.ID = %q, want localhost", opts.RP.ID)
	}
	if opts.User.Name != "alice" {
		t.Errorf("User.Name = %q, want alice", opts.User.Name)
	}
	// Default offer list must include at least ES256.
	foundES256 := false
	for _, p := range opts.PubKeyCredParams {
		if p.Alg == -7 {
			foundES256 = true
		}
	}
	if !foundES256 {
		t.Error("default algorithms missing ES256 (-7)")
	}
	// Default attachment is empty (browser chooser).
	if opts.AuthenticatorSelection.AuthenticatorAttachment != "" {
		t.Errorf("default attachment = %q, want empty", opts.AuthenticatorSelection.AuthenticatorAttachment)
	}
}

// TestPasskey_BeginEnroll_persistsAcrossServiceInstances is the
// regression test for the cluster-aware challenge storage. In a
// multi-instance pika deployment behind a load balancer, begin and
// finish can land on different nodes. We simulate that by issuing
// begin on one PasskeyService instance and finish on a second
// instance backed by the same storage — if challenges still lived in
// an in-process map this would fail with ErrUnauthorized.
//
// We can't drive a full FinishEnroll without a real WebAuthn
// response, so the assertion is "the session exists in the second
// instance's view" — proxied through a known-bad finish that should
// still get past the session-exists check.
func TestPasskey_BeginEnroll_persistsAcrossServiceInstances(t *testing.T) {
	svc, ps := newTestPasskeyService(t)
	uid := createUserHelper(t, svc, "cluster-alice")

	sid, _, err := ps.BeginEnroll(t.Context(), uid, nil)
	if err != nil {
		t.Fatalf("BeginEnroll: %v", err)
	}

	// Spin up a second coordinator pointed at the same backing
	// store — analogous to a different pika instance reading the
	// challenge after bw replicated it. The PasskeyService's own
	// state (lastUsedCh, gcLoop) is intentionally local, but the
	// challenge bucket is shared.
	engine, err := passkey.New(&passkey.Config{
		RPID: "localhost", RPDisplayName: "pika test",
		RPOrigins: []string{"http://localhost"},
	})
	if err != nil {
		t.Fatalf("passkey.New: %v", err)
	}
	ps2 := service.NewPasskeyService(svc, engine, 5*time.Minute)

	row, err := svc.PasskeyChallengeStore().Get(t.Context(), sid)
	if err != nil {
		t.Fatalf("challenge missing from shared store: %v", err)
	}
	if row.UserID != uid {
		t.Errorf("challenge user_id = %q, want %q", row.UserID, uid)
	}
	if row.Kind != "enroll" {
		t.Errorf("challenge kind = %q, want enroll", row.Kind)
	}

	// FinishEnroll from the second instance should at least clear
	// the "unknown session" path — the body is empty so it'll fail
	// later, but the failure must NOT be ErrUnauthorized for
	// session-not-found.
	_, err = ps2.FinishEnroll(t.Context(), uid, sid, "", []byte(`{}`))
	if errors.Is(err, service.ErrUnauthorized) && err.Error() == "unknown session: "+service.ErrUnauthorized.Error() {
		// We only fail if the error specifically says "unknown
		// session" — any other error (parse failure, etc.) means
		// the session lookup passed, which is what this test cares
		// about.
		t.Errorf("second instance treats session as missing: %v", err)
	}
}

// TestPasskey_BeginEnroll_attachmentScopesCeremony covers the
// platform / cross-platform / garbage variants for the optional
// attachment hint. Garbage values must degrade to empty so a typoed
// SPA doesn't break the ceremony.
func TestPasskey_BeginEnroll_attachmentScopesCeremony(t *testing.T) {
	svc, ps := newTestPasskeyService(t)
	uid := createUserHelper(t, svc, "alice-attach")

	cases := []struct {
		in   string
		want string
	}{
		{"platform", "platform"},
		{"cross-platform", "cross-platform"},
		{"PLATFORM", ""}, // case-sensitive in spec
		{"garbage", ""},
		{"", ""},
	}
	for _, c := range cases {
		_, opts, err := ps.BeginEnroll(t.Context(), uid, &service.EnrollOptions{AuthenticatorAttachment: c.in})
		if err != nil {
			t.Fatalf("BeginEnroll(%q): %v", c.in, err)
		}
		got := opts.AuthenticatorSelection.AuthenticatorAttachment
		if got != c.want {
			t.Errorf("attachment %q: got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPasskey_BeginEnroll_excludesEnrolledCredentials(t *testing.T) {
	svc, ps := newTestPasskeyService(t)
	uid := createUserHelper(t, svc, "bob")

	// Seed two existing credentials.
	seedPasskeyRow(t, svc, uid, "first", []byte{0x01, 0x02}, []byte{0x10})
	seedPasskeyRow(t, svc, uid, "second", []byte{0x03, 0x04}, []byte{0x20})

	_, opts, err := ps.BeginEnroll(t.Context(), uid, nil)
	if err != nil {
		t.Fatalf("BeginEnroll: %v", err)
	}
	if len(opts.ExcludeCredentials) != 2 {
		t.Errorf("ExcludeCredentials len = %d, want 2", len(opts.ExcludeCredentials))
	}
}

func TestPasskey_BeginEnroll_rejectsDisabledUser(t *testing.T) {
	svc, ps := newTestPasskeyService(t)
	uid := createUserHelper(t, svc, "carol")

	// Disable the user.
	disabled := true
	if err := svc.UpdateUser(t.Context(), uid, &service.UpdateUserRequest{Disabled: &disabled}); err != nil {
		t.Fatalf("disable user: %v", err)
	}

	_, _, err := ps.BeginEnroll(t.Context(), uid, nil)
	if !errors.Is(err, service.ErrForbidden) {
		t.Errorf("disabled user: got %v, want ErrForbidden", err)
	}
}

func TestPasskey_FinishEnroll_rejectsUnknownSession(t *testing.T) {
	svc, ps := newTestPasskeyService(t)
	uid := createUserHelper(t, svc, "dan")

	_, err := ps.FinishEnroll(t.Context(), uid, "bogus-session", "name", []byte(`{}`))
	if !errors.Is(err, service.ErrUnauthorized) {
		t.Errorf("bogus session: got %v, want ErrUnauthorized", err)
	}
}

func TestPasskey_FinishEnroll_rejectsCrossUserSession(t *testing.T) {
	svc, ps := newTestPasskeyService(t)
	alice := createUserHelper(t, svc, "alice")
	bob := createUserHelper(t, svc, "bob")

	// Alice starts an enrollment.
	sid, _, err := ps.BeginEnroll(t.Context(), alice, nil)
	if err != nil {
		t.Fatalf("BeginEnroll: %v", err)
	}

	// Bob tries to finish Alice's session.
	_, err = ps.FinishEnroll(t.Context(), bob, sid, "stolen", []byte(`{}`))
	if !errors.Is(err, service.ErrUnauthorized) {
		t.Errorf("cross-user session: got %v, want ErrUnauthorized", err)
	}
}

func TestPasskey_List_ownershipAndOrdering(t *testing.T) {
	svc, ps := newTestPasskeyService(t)
	alice := createUserHelper(t, svc, "alice")
	bob := createUserHelper(t, svc, "bob")

	// Alice has 3 credentials, Bob has 1.
	a1 := seedPasskeyRow(t, svc, alice, "a1", []byte{0x01}, []byte{0x10})
	time.Sleep(2 * time.Millisecond) // ensure distinct CreatedAt
	a2 := seedPasskeyRow(t, svc, alice, "a2", []byte{0x02}, []byte{0x20})
	time.Sleep(2 * time.Millisecond)
	a3 := seedPasskeyRow(t, svc, alice, "a3", []byte{0x03}, []byte{0x30})
	seedPasskeyRow(t, svc, bob, "b1", []byte{0x04}, []byte{0x40})

	alist, err := ps.ListUserPasskeys(t.Context(), alice)
	if err != nil {
		t.Fatalf("ListUserPasskeys(alice): %v", err)
	}
	if len(alist) != 3 {
		t.Fatalf("alice should see 3 passkeys, got %d", len(alist))
	}
	// Newest first: a3, a2, a1.
	if alist[0].ID != a3.ID || alist[1].ID != a2.ID || alist[2].ID != a1.ID {
		t.Errorf("order = [%s, %s, %s], want [%s, %s, %s]",
			alist[0].ID, alist[1].ID, alist[2].ID, a3.ID, a2.ID, a1.ID)
	}
	// PublicKey must be stripped on read.
	for _, p := range alist {
		if p.PublicKey != nil {
			t.Errorf("public key leaked on list: %x", p.PublicKey)
		}
	}

	blist, err := ps.ListUserPasskeys(t.Context(), bob)
	if err != nil {
		t.Fatalf("ListUserPasskeys(bob): %v", err)
	}
	if len(blist) != 1 {
		t.Fatalf("bob should see 1 passkey, got %d", len(blist))
	}
}

func TestPasskey_Rename_enforcesOwnership(t *testing.T) {
	svc, ps := newTestPasskeyService(t)
	alice := createUserHelper(t, svc, "alice")
	bob := createUserHelper(t, svc, "bob")

	a1 := seedPasskeyRow(t, svc, alice, "a1", []byte{0x01}, []byte{0x10})

	// Bob cannot rename Alice's credential.
	_, err := ps.RenamePasskey(t.Context(), bob, a1.ID, "stolen")
	if !errors.Is(err, service.ErrForbidden) {
		t.Errorf("cross-user rename: got %v, want ErrForbidden", err)
	}

	// Alice can.
	updated, err := ps.RenamePasskey(t.Context(), alice, a1.ID, "Phone")
	if err != nil {
		t.Fatalf("alice rename: %v", err)
	}
	if updated.Name != "Phone" {
		t.Errorf("name = %q, want Phone", updated.Name)
	}

	// Empty name rejected.
	_, err = ps.RenamePasskey(t.Context(), alice, a1.ID, "   ")
	if !errors.Is(err, service.ErrBadRequest) {
		t.Errorf("empty name: got %v, want ErrBadRequest", err)
	}
}

func TestPasskey_Delete_enforcesOwnership(t *testing.T) {
	svc, ps := newTestPasskeyService(t)
	alice := createUserHelper(t, svc, "alice")
	bob := createUserHelper(t, svc, "bob")
	a1 := seedPasskeyRow(t, svc, alice, "a1", []byte{0x01}, []byte{0x10})

	// Bob cannot delete Alice's credential.
	err := ps.DeletePasskey(t.Context(), bob, a1.ID)
	if !errors.Is(err, service.ErrForbidden) {
		t.Errorf("cross-user delete: got %v, want ErrForbidden", err)
	}

	// Alice can; the row disappears.
	if err := ps.DeletePasskey(t.Context(), alice, a1.ID); err != nil {
		t.Fatalf("alice delete: %v", err)
	}
	list, _ := ps.ListUserPasskeys(t.Context(), alice)
	if len(list) != 0 {
		t.Errorf("after delete, alice still has %d", len(list))
	}
}

func TestPasskey_LookupForLogin_unknownCredentialIsGenericError(t *testing.T) {
	_, ps := newTestPasskeyService(t)

	_, _, err := ps.LookupForLogin(t.Context(), []byte{0xff, 0xfe, 0xfd})
	if !errors.Is(err, passkey.ErrCredentialNotFound) {
		t.Errorf("unknown credential: got %v, want passkey.ErrCredentialNotFound", err)
	}
}

func TestPasskey_LookupForLogin_disabledUserIsGenericError(t *testing.T) {
	svc, ps := newTestPasskeyService(t)
	uid := createUserHelper(t, svc, "alice")
	credID := []byte{0xaa, 0xbb, 0xcc}
	seedPasskeyRow(t, svc, uid, "a1", credID, []byte{0x10})

	// Disable user.
	disabled := true
	if err := svc.UpdateUser(t.Context(), uid, &service.UpdateUserRequest{Disabled: &disabled}); err != nil {
		t.Fatalf("disable user: %v", err)
	}

	_, _, err := ps.LookupForLogin(t.Context(), credID)
	if !errors.Is(err, passkey.ErrCredentialNotFound) {
		// We deliberately map ErrForbidden → ErrCredentialNotFound so
		// the strategy can't accidentally leak account-disabled state
		// via timing or response text.
		t.Errorf("disabled user lookup: got %v, want passkey.ErrCredentialNotFound", err)
	}
}

func TestPasskey_LookupForLogin_returnsIdentityAndUpdatesLastUsed(t *testing.T) {
	svc, ps := newTestPasskeyService(t)
	uid := createUserHelper(t, svc, "alice")
	credID := []byte{0xa1, 0xa2, 0xa3, 0xa4}
	row := seedPasskeyRow(t, svc, uid, "phone", credID, []byte{0x10, 0x20})
	if !row.LastUsedAt.IsZero() {
		t.Fatal("LastUsedAt should be zero before login")
	}

	cred, id, err := ps.LookupForLogin(t.Context(), credID)
	if err != nil {
		t.Fatalf("LookupForLogin: %v", err)
	}
	if cred == nil || id == nil {
		t.Fatal("missing credential or identity")
	}
	if id.Subject != "alice" {
		t.Errorf("identity subject = %q, want alice", id.Subject)
	}
	if id.Provider != "passkey" {
		t.Errorf("identity provider = %q, want passkey", id.Provider)
	}

	// LastUsedAt is bumped asynchronously by the background flusher.
	// In production it lands within ≤ 2 s; for tests we force a sync
	// drain so the assertion is deterministic.
	ps.FlushLastUsed()

	stored, err := svc.PasskeyStore().Get(t.Context(), row.ID)
	if err != nil {
		t.Fatalf("re-fetch: %v", err)
	}
	if stored.LastUsedAt.IsZero() {
		t.Error("LastUsedAt was not bumped after lookup")
	}
}

func TestPasskey_UpdateSignCount_persists(t *testing.T) {
	svc, ps := newTestPasskeyService(t)
	uid := createUserHelper(t, svc, "alice")
	credID := []byte{0xb1, 0xb2}
	row := seedPasskeyRow(t, svc, uid, "key", credID, []byte{0x10})

	if err := ps.UpdateSignCount(t.Context(), credID, 42); err != nil {
		t.Fatalf("UpdateSignCount: %v", err)
	}
	got, _ := svc.PasskeyStore().Get(t.Context(), row.ID)
	if got.SignCount != 42 {
		t.Errorf("sign count = %d, want 42", got.SignCount)
	}
}

func TestPasskey_UserDelete_cascadesCredentials(t *testing.T) {
	svc, ps := newTestPasskeyService(t)
	uid := createUserHelper(t, svc, "alice")
	credID := []byte{0xc1, 0xc2}
	seedPasskeyRow(t, svc, uid, "key", credID, []byte{0x10})

	// Confirm the row exists.
	list, _ := ps.ListUserPasskeys(t.Context(), uid)
	if len(list) != 1 {
		t.Fatalf("setup: list len = %d", len(list))
	}

	if err := svc.DeleteUser(t.Context(), uid); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	// Lookup by credential id must now miss.
	_, err := svc.PasskeyStore().FindByCredentialID(t.Context(), credID)
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("cascade did not remove credential: %v", err)
	}
}
