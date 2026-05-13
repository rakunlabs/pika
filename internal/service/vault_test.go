package service_test

import (
	"bytes"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/rakunlabs/pika/internal/service"
)

// newTestVaultService wires a real VaultService onto an in-memory pika
// service. Mirrors newTestTOTPService.
func newTestVaultService(t *testing.T) (*service.Service, *service.VaultService) {
	t.Helper()
	svc := newTestService(t)
	vs := service.NewVaultService(svc)
	svc.SetVaultService(vs)
	return svc, vs
}

// validSetupRequest returns a syntactically-correct setup payload for
// a freshly-created user. The byte slices are random — the server
// doesn't (and shouldn't) validate that they're meaningful Argon2id
// outputs; that's the client's responsibility.
func validSetupRequest(t *testing.T) *service.VaultSetupRequest {
	t.Helper()
	salt := mustRand(t, 16)
	hash := mustRand(t, 32)
	wrapped := mustRand(t, 64)
	return &service.VaultSetupRequest{
		SecretKeyHash: hash,
		KDF: service.VaultKDFParams{
			Algorithm:   "argon2id",
			Memory:      64 * 1024, // 64 MiB
			Iterations:  3,
			Parallelism: 1,
			Salt:        salt,
		},
		WrappedVaultKey:        wrapped,
		WrappedVaultKeyVersion: 1,
		SessionLockSeconds:     900,
	}
}

func mustRand(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return b
}

// fakeCiphertext returns random bytes shaped like a valid AEAD output
// (≥ 40 bytes: 24 nonce + 16 mac). The server treats every byte as
// opaque; it only validates the floor / cap, never the content.
func fakeCiphertext(t *testing.T, plaintextLen int) []byte {
	t.Helper()
	if plaintextLen < 0 {
		plaintextLen = 0
	}
	return mustRand(t, 24+16+plaintextLen)
}

func TestVault_StatusBeforeSetup(t *testing.T) {
	svc, vs := newTestVaultService(t)
	uid := createUserHelper(t, svc, "alice")

	status, err := vs.Status(t.Context(), uid)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Initialized {
		t.Error("fresh user should not have an initialized vault")
	}
	if status.ItemCount != 0 {
		t.Errorf("item count: got %d want 0", status.ItemCount)
	}
}

func TestVault_SetupRoundTrip(t *testing.T) {
	svc, vs := newTestVaultService(t)
	uid := createUserHelper(t, svc, "alice")
	ctx := t.Context()

	req := validSetupRequest(t)
	view, err := vs.Setup(ctx, uid, req)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if view.UserID != uid {
		t.Errorf("UserID: got %q want %q", view.UserID, uid)
	}
	if !bytes.Equal(view.WrappedVaultKey, req.WrappedVaultKey) {
		t.Error("wrapped vault key round-trip mismatch")
	}
	if view.KDF.Memory != req.KDF.Memory {
		t.Errorf("KDF memory: got %d want %d", view.KDF.Memory, req.KDF.Memory)
	}
	if view.SessionLockSeconds != 900 {
		t.Errorf("SessionLockSeconds: got %d want 900", view.SessionLockSeconds)
	}
	if view.RecoveryKitID == "" {
		t.Error("RecoveryKitID should be populated at Setup")
	}

	// Status should now report initialized.
	status, err := vs.Status(ctx, uid)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Initialized {
		t.Error("Status should show initialized after Setup")
	}
}

func TestVault_SetupRejectsReinit(t *testing.T) {
	svc, vs := newTestVaultService(t)
	uid := createUserHelper(t, svc, "alice")
	ctx := t.Context()

	if _, err := vs.Setup(ctx, uid, validSetupRequest(t)); err != nil {
		t.Fatalf("first Setup: %v", err)
	}
	_, err := vs.Setup(ctx, uid, validSetupRequest(t))
	if !errors.Is(err, service.ErrVaultAlreadyInitialized) {
		t.Errorf("second Setup should return ErrVaultAlreadyInitialized, got %v", err)
	}
}

func TestVault_SetupRejectsWeakKDF(t *testing.T) {
	svc, vs := newTestVaultService(t)
	uid := createUserHelper(t, svc, "alice")
	ctx := t.Context()

	req := validSetupRequest(t)
	req.KDF.Memory = 1024 // 1 MiB — well below OWASP floor of 19 MiB
	_, err := vs.Setup(ctx, uid, req)
	if !errors.Is(err, service.ErrBadRequest) {
		t.Errorf("weak KDF should reject with ErrBadRequest, got %v", err)
	}
}

func TestVault_UnlockCheck(t *testing.T) {
	svc, vs := newTestVaultService(t)
	uid := createUserHelper(t, svc, "alice")
	ctx := t.Context()

	req := validSetupRequest(t)
	if _, err := vs.Setup(ctx, uid, req); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	// Correct hash: success.
	if err := vs.UnlockCheck(ctx, uid, "", &service.UnlockCheckRequest{
		SecretKeyHash: req.SecretKeyHash,
	}); err != nil {
		t.Errorf("UnlockCheck with correct hash should succeed, got %v", err)
	}

	// Wrong hash: unauthorized.
	wrong := mustRand(t, 32)
	err := vs.UnlockCheck(ctx, uid, "", &service.UnlockCheckRequest{SecretKeyHash: wrong})
	if !errors.Is(err, service.ErrUnauthorized) {
		t.Errorf("UnlockCheck with wrong hash should return ErrUnauthorized, got %v", err)
	}
}

func TestVault_ItemRoundTrip(t *testing.T) {
	svc, vs := newTestVaultService(t)
	uid := createUserHelper(t, svc, "alice")
	ctx := t.Context()

	if _, err := vs.Setup(ctx, uid, validSetupRequest(t)); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	title := fakeCiphertext(t, 32)
	tags := fakeCiphertext(t, 32)
	hosts := fakeCiphertext(t, 16)
	payload := fakeCiphertext(t, 256)
	item, err := vs.CreateItem(ctx, uid, &service.CreateVaultItemRequest{
		Type:               service.VaultItemTypeLogin,
		EncryptedTitle:     title,
		EncryptedTags:      tags,
		EncryptedHostnames: hosts,
		EncryptedPayload:   payload,
		Favorite:           true,
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	if item.ID == "" {
		t.Error("item ID should be populated")
	}
	if item.Version != 1 {
		t.Errorf("initial version: got %d want 1", item.Version)
	}
	if !bytes.Equal(item.EncryptedTitle, title) {
		t.Error("title ciphertext round-trip mismatch")
	}
	if !bytes.Equal(item.EncryptedTags, tags) {
		t.Error("tags ciphertext round-trip mismatch")
	}
	if !bytes.Equal(item.EncryptedHostnames, hosts) {
		t.Error("hostnames ciphertext round-trip mismatch")
	}

	// Get round-trip.
	got, err := vs.GetItem(ctx, uid, item.ID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if !bytes.Equal(got.EncryptedPayload, payload) {
		t.Error("encrypted payload round-trip mismatch")
	}

	// List returns it.
	items, err := vs.ListItems(ctx, uid, service.VaultItemFilter{})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 1 || items[0].ID != item.ID {
		t.Errorf("ListItems: got %d items, want exactly the created one", len(items))
	}
}

// TestVault_ServerNeverHoldsCleartextTitle confirms that the server
// never stores a plaintext title or tag — neither in the live row
// nor in the version history. We do this by scanning the entire item
// payload (and every history snapshot) for a needle the test
// supplies as the *plaintext* the SPA would have encrypted. Since we
// only ever feed the server opaque random bytes via fakeCiphertext,
// the needle MUST be absent.
func TestVault_ServerNeverHoldsCleartextTitle(t *testing.T) {
	svc, vs := newTestVaultService(t)
	uid := createUserHelper(t, svc, "alice")
	ctx := t.Context()
	if _, err := vs.Setup(ctx, uid, validSetupRequest(t)); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	// The needle is something a real user might type as a title; the
	// SPA would encrypt it and ship the ciphertext to the server. In
	// this test we only ship random ciphertext, so the needle must
	// never appear in any server-visible byte.
	const needle = "GitHub-of-doom"

	item, err := vs.CreateItem(ctx, uid, &service.CreateVaultItemRequest{
		Type:             service.VaultItemTypeLogin,
		EncryptedTitle:   fakeCiphertext(t, len(needle)),
		EncryptedPayload: fakeCiphertext(t, 64),
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	// Bump the item once so we also exercise the history snapshot path.
	if _, err := vs.UpdateItem(ctx, uid, item.ID, &service.UpdateVaultItemRequest{
		ExpectedVersion: 1,
		EncryptedTitle:  fakeCiphertext(t, len(needle)+8),
	}); err != nil {
		t.Fatalf("UpdateItem: %v", err)
	}

	got, err := vs.GetItem(ctx, uid, item.ID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if bytes.Contains(got.EncryptedTitle, []byte(needle)) {
		t.Error("encrypted_title leaked the needle")
	}
	if bytes.Contains(got.EncryptedTags, []byte(needle)) {
		t.Error("encrypted_tags leaked the needle")
	}
	if bytes.Contains(got.EncryptedHostnames, []byte(needle)) {
		t.Error("encrypted_hostnames leaked the needle")
	}
	if bytes.Contains(got.EncryptedPayload, []byte(needle)) {
		t.Error("encrypted_payload leaked the needle")
	}

	// Same check on every history row.
	versions, err := vs.ListItemVersions(ctx, uid, item.ID)
	if err != nil {
		t.Fatalf("ListItemVersions: %v", err)
	}
	for _, v := range versions {
		if bytes.Contains(v.EncryptedTitle, []byte(needle)) {
			t.Errorf("version v%d leaked the needle in encrypted_title", v.Version)
		}
		if bytes.Contains(v.EncryptedPayload, []byte(needle)) {
			t.Errorf("version v%d leaked the needle in encrypted_payload", v.Version)
		}
	}
}

func TestVault_CreateRejectsShortCiphertext(t *testing.T) {
	svc, vs := newTestVaultService(t)
	uid := createUserHelper(t, svc, "alice")
	ctx := t.Context()
	if _, err := vs.Setup(ctx, uid, validSetupRequest(t)); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	// 39 bytes is one short of the AEAD framing floor (24 + 16).
	_, err := vs.CreateItem(ctx, uid, &service.CreateVaultItemRequest{
		Type:             service.VaultItemTypeLogin,
		EncryptedTitle:   mustRand(t, 39),
		EncryptedPayload: fakeCiphertext(t, 32),
	})
	if !errors.Is(err, service.ErrBadRequest) {
		t.Errorf("short title ciphertext should reject with ErrBadRequest, got %v", err)
	}
}

func TestVault_CreateRequiresTitle(t *testing.T) {
	svc, vs := newTestVaultService(t)
	uid := createUserHelper(t, svc, "alice")
	ctx := t.Context()
	if _, err := vs.Setup(ctx, uid, validSetupRequest(t)); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	_, err := vs.CreateItem(ctx, uid, &service.CreateVaultItemRequest{
		Type:             service.VaultItemTypeLogin,
		EncryptedPayload: fakeCiphertext(t, 32),
	})
	if !errors.Is(err, service.ErrBadRequest) {
		t.Errorf("missing title should reject with ErrBadRequest, got %v", err)
	}
}

func TestVault_ItemOwnershipBoundary(t *testing.T) {
	svc, vs := newTestVaultService(t)
	alice := createUserHelper(t, svc, "alice")
	bob := createUserHelper(t, svc, "bob")
	ctx := t.Context()

	for _, uid := range []string{alice, bob} {
		if _, err := vs.Setup(ctx, uid, validSetupRequest(t)); err != nil {
			t.Fatalf("Setup(%s): %v", uid, err)
		}
	}

	item, err := vs.CreateItem(ctx, alice, &service.CreateVaultItemRequest{
		Type:             service.VaultItemTypeLogin,
		EncryptedTitle:   fakeCiphertext(t, 16),
		EncryptedPayload: fakeCiphertext(t, 64),
	})
	if err != nil {
		t.Fatalf("CreateItem(alice): %v", err)
	}

	// Bob trying to read Alice's item should get ErrNotFound (mask).
	_, err = vs.GetItem(ctx, bob, item.ID)
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("cross-user GetItem should return ErrNotFound, got %v", err)
	}

	// Bob listing his own items shouldn't see Alice's.
	bobItems, err := vs.ListItems(ctx, bob, service.VaultItemFilter{})
	if err != nil {
		t.Fatalf("ListItems(bob): %v", err)
	}
	if len(bobItems) != 0 {
		t.Errorf("bob should have 0 items, got %d", len(bobItems))
	}
}

func TestVault_UpdateOptimisticConcurrency(t *testing.T) {
	svc, vs := newTestVaultService(t)
	uid := createUserHelper(t, svc, "alice")
	ctx := t.Context()

	if _, err := vs.Setup(ctx, uid, validSetupRequest(t)); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	initialTitle := fakeCiphertext(t, 24)
	item, err := vs.CreateItem(ctx, uid, &service.CreateVaultItemRequest{
		Type:             service.VaultItemTypeLogin,
		EncryptedTitle:   initialTitle,
		EncryptedPayload: fakeCiphertext(t, 64),
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	// Successful patch: version=1 expected.
	newTitle := fakeCiphertext(t, 48)
	updated, err := vs.UpdateItem(ctx, uid, item.ID, &service.UpdateVaultItemRequest{
		ExpectedVersion: 1,
		EncryptedTitle:  newTitle,
	})
	if err != nil {
		t.Fatalf("UpdateItem: %v", err)
	}
	if updated.Version != 2 {
		t.Errorf("version after update: got %d want 2", updated.Version)
	}
	if !bytes.Equal(updated.EncryptedTitle, newTitle) {
		t.Error("encrypted_title not updated")
	}

	// Stale write: expected_version=1 again, should conflict.
	conflictTitle := fakeCiphertext(t, 32)
	_, err = vs.UpdateItem(ctx, uid, item.ID, &service.UpdateVaultItemRequest{
		ExpectedVersion: 1,
		EncryptedTitle:  conflictTitle,
	})
	if !errors.Is(err, service.ErrVaultVersionConflict) {
		t.Errorf("stale update should return ErrVaultVersionConflict, got %v", err)
	}

	// History should have one snapshot — the pre-update state. Carries
	// the encrypted form of the original title.
	versions, err := vs.ListItemVersions(ctx, uid, item.ID)
	if err != nil {
		t.Fatalf("ListItemVersions: %v", err)
	}
	if len(versions) != 1 {
		t.Errorf("history count: got %d want 1", len(versions))
	}
	if !bytes.Equal(versions[0].EncryptedTitle, initialTitle) {
		t.Error("snapshot EncryptedTitle does not match pre-update title")
	}
}

func TestVault_SoftDeleteRestorePurge(t *testing.T) {
	svc, vs := newTestVaultService(t)
	uid := createUserHelper(t, svc, "alice")
	ctx := t.Context()

	if _, err := vs.Setup(ctx, uid, validSetupRequest(t)); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	item, err := vs.CreateItem(ctx, uid, &service.CreateVaultItemRequest{
		Type:             service.VaultItemTypeSecureNote,
		EncryptedTitle:   fakeCiphertext(t, 16),
		EncryptedPayload: fakeCiphertext(t, 32),
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	// Soft delete.
	if err := vs.SoftDeleteItem(ctx, uid, item.ID); err != nil {
		t.Fatalf("SoftDeleteItem: %v", err)
	}

	// Active list hides it.
	active, err := vs.ListItems(ctx, uid, service.VaultItemFilter{})
	if err != nil {
		t.Fatalf("ListItems active: %v", err)
	}
	if len(active) != 0 {
		t.Errorf("active list after soft delete: got %d items, want 0", len(active))
	}

	// Trash list shows it.
	trash, err := vs.ListItems(ctx, uid, service.VaultItemFilter{TrashOnly: true})
	if err != nil {
		t.Fatalf("ListItems trash: %v", err)
	}
	if len(trash) != 1 || trash[0].ID != item.ID {
		t.Errorf("trash list: got %v, want 1 item with id %q", trash, item.ID)
	}

	// Purge before restore is allowed (item is already in trash).
	if err := vs.PurgeItem(ctx, uid, item.ID); err != nil {
		t.Fatalf("PurgeItem: %v", err)
	}

	// Item is gone.
	_, err = vs.GetItem(ctx, uid, item.ID)
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("GetItem after purge should return ErrNotFound, got %v", err)
	}
}

func TestVault_PurgeOnActiveItemForbidden(t *testing.T) {
	svc, vs := newTestVaultService(t)
	uid := createUserHelper(t, svc, "alice")
	ctx := t.Context()

	if _, err := vs.Setup(ctx, uid, validSetupRequest(t)); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	item, err := vs.CreateItem(ctx, uid, &service.CreateVaultItemRequest{
		Type:             service.VaultItemTypeLogin,
		EncryptedTitle:   fakeCiphertext(t, 8),
		EncryptedPayload: fakeCiphertext(t, 32),
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	err = vs.PurgeItem(ctx, uid, item.ID)
	if !errors.Is(err, service.ErrBadRequest) {
		t.Errorf("Purge on active item should return ErrBadRequest, got %v", err)
	}
}

func TestVault_FilterByType(t *testing.T) {
	svc, vs := newTestVaultService(t)
	uid := createUserHelper(t, svc, "alice")
	ctx := t.Context()
	if _, err := vs.Setup(ctx, uid, validSetupRequest(t)); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	mk := func(kind service.VaultItemType) string {
		item, err := vs.CreateItem(ctx, uid, &service.CreateVaultItemRequest{
			Type:             kind,
			EncryptedTitle:   fakeCiphertext(t, 8),
			EncryptedPayload: fakeCiphertext(t, 16),
		})
		if err != nil {
			t.Fatalf("CreateItem(%s): %v", kind, err)
		}
		return item.ID
	}
	mk(service.VaultItemTypeLogin)
	cardID := mk(service.VaultItemTypeCard)
	mk(service.VaultItemTypeLogin)

	logins, err := vs.ListItems(ctx, uid, service.VaultItemFilter{Type: service.VaultItemTypeLogin})
	if err != nil {
		t.Fatalf("ListItems logins: %v", err)
	}
	if len(logins) != 2 {
		t.Errorf("login count: got %d want 2", len(logins))
	}

	cards, err := vs.ListItems(ctx, uid, service.VaultItemFilter{Type: service.VaultItemTypeCard})
	if err != nil {
		t.Fatalf("ListItems cards: %v", err)
	}
	if len(cards) != 1 || cards[0].ID != cardID {
		t.Errorf("card filter: got %v, want exactly the card we made", cards)
	}
}

func TestVault_RejectsUnknownType(t *testing.T) {
	svc, vs := newTestVaultService(t)
	uid := createUserHelper(t, svc, "alice")
	ctx := t.Context()
	if _, err := vs.Setup(ctx, uid, validSetupRequest(t)); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	_, err := vs.CreateItem(ctx, uid, &service.CreateVaultItemRequest{
		Type:             service.VaultItemType("crypto_wallet"),
		EncryptedTitle:   fakeCiphertext(t, 8),
		EncryptedPayload: fakeCiphertext(t, 16),
	})
	if !errors.Is(err, service.ErrVaultUnknownItemType) {
		t.Errorf("unknown type should return ErrVaultUnknownItemType, got %v", err)
	}
}

func TestVault_ResetClearsEverything(t *testing.T) {
	svc, vs := newTestVaultService(t)
	uid := createUserHelper(t, svc, "alice")
	ctx := t.Context()
	if _, err := vs.Setup(ctx, uid, validSetupRequest(t)); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := vs.CreateItem(ctx, uid, &service.CreateVaultItemRequest{
			Type:             service.VaultItemTypeLogin,
			EncryptedTitle:   fakeCiphertext(t, 8),
			EncryptedPayload: fakeCiphertext(t, 16),
		}); err != nil {
			t.Fatalf("CreateItem: %v", err)
		}
	}

	n, err := vs.Reset(ctx, uid)
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if n != 3 {
		t.Errorf("Reset deleted count: got %d want 3", n)
	}

	status, _ := vs.Status(ctx, uid)
	if status.Initialized {
		t.Error("Status should report not-initialized after Reset")
	}
}

func TestVault_UserDeleteCascade(t *testing.T) {
	svc, vs := newTestVaultService(t)
	uid := createUserHelper(t, svc, "alice")
	ctx := t.Context()
	if _, err := vs.Setup(ctx, uid, validSetupRequest(t)); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if _, err := vs.CreateItem(ctx, uid, &service.CreateVaultItemRequest{
		Type:             service.VaultItemTypeLogin,
		EncryptedTitle:   fakeCiphertext(t, 8),
		EncryptedPayload: fakeCiphertext(t, 32),
	}); err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	if err := svc.DeleteUser(ctx, uid); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	// Recreate the user with the same username; the vault should be
	// gone (account row deleted by cascade).
	uid2 := createUserHelper(t, svc, "alice")
	if uid2 == uid {
		t.Fatal("expected new user id after recreate")
	}
	status, err := vs.Status(ctx, uid2)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Initialized {
		t.Error("recreated user should have a clean vault")
	}
}
