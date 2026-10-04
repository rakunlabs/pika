package service_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/rakunlabs/pika/internal/service"
)

func newServerModeVault(t *testing.T) (*service.Service, *service.VaultService) {
	t.Helper()
	svc, _ := newKeyopsService(t)
	if err := svc.InitializeServerKey(t.Context(), "operator-passphrase"); err != nil {
		t.Fatalf("InitializeServerKey: %v", err)
	}
	vs := service.NewVaultService(svc)
	svc.SetVaultService(vs)
	t.Cleanup(vs.Close)
	return svc, vs
}

func setVaultKeyMode(t *testing.T, svc *service.Service, mode string) {
	t.Helper()
	err := svc.PatchSettings(t.Context(), &service.PatchSettings{
		Action: service.ActionKeySet,
		Vault:  &service.VaultSettings{KeyMode: mode},
	})
	if err != nil {
		t.Fatalf("PatchSettings(key_mode=%s): %v", mode, err)
	}
}

func TestVaultServerMode_RequiresUnlockedServerKey(t *testing.T) {
	svc, _ := newKeyopsService(t)
	err := svc.PatchSettings(t.Context(), &service.PatchSettings{
		Action: service.ActionKeySet,
		Vault:  &service.VaultSettings{KeyMode: service.VaultKeyModeServer},
	})
	if !errors.Is(err, service.ErrBadRequest) {
		t.Fatalf("want ErrBadRequest without server key, got %v", err)
	}
}

func TestVaultKeyMode_DefaultWithoutServerKey(t *testing.T) {
	svc, _ := newKeyopsService(t)
	if got := svc.VaultKeyMode(t.Context()); got != service.VaultKeyModeUser {
		t.Fatalf("default mode without server key = %q, want user", got)
	}
}

func TestVaultKeyMode_DefaultsToServerOnFreshDeployment(t *testing.T) {
	svc, _ := newServerModeVault(t)
	if got := svc.VaultKeyMode(t.Context()); got != service.VaultKeyModeServer {
		t.Fatalf("default mode = %q, want server", got)
	}
	// Saving an unrelated vault flag must not pin the mode.
	if err := svc.PatchSettings(t.Context(), &service.PatchSettings{
		Action: service.ActionKeySet,
		Vault:  &service.VaultSettings{Disabled: false},
	}); err != nil {
		t.Fatalf("PatchSettings: %v", err)
	}
	if got := svc.VaultKeyMode(t.Context()); got != service.VaultKeyModeServer {
		t.Fatalf("mode after unrelated save = %q, want server", got)
	}
}

func TestVaultKeyMode_ExistingUserVaultKeepsUserDefault(t *testing.T) {
	svc, vs := newServerModeVault(t)
	uid := createUserHelper(t, svc, "alice")
	setVaultKeyMode(t, svc, service.VaultKeyModeUser)
	if _, err := vs.Setup(t.Context(), uid, validSetupRequest(t)); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	// Simulate a legacy install where the mode was never stored.
	if err := svc.PatchSettings(t.Context(), &service.PatchSettings{
		Action: service.ActionKeySet,
		Vault:  &service.VaultSettings{},
	}); err != nil {
		t.Fatalf("PatchSettings: %v", err)
	}
	if got := svc.VaultKeyMode(t.Context()); got != service.VaultKeyModeUser {
		t.Fatalf("default mode with an existing user vault = %q, want user", got)
	}
}

func TestVaultServerMode_SetupAndOpen(t *testing.T) {
	svc, vs := newServerModeVault(t)
	uid := createUserHelper(t, svc, "alice")
	setVaultKeyMode(t, svc, service.VaultKeyModeServer)

	if _, err := vs.Setup(t.Context(), uid, validSetupRequest(t)); !errors.Is(err, service.ErrBadRequest) {
		t.Fatalf("master-password setup in server mode: want ErrBadRequest, got %v", err)
	}

	view, key, err := vs.SetupServerManaged(t.Context(), uid, &service.VaultServerSetupRequest{})
	if err != nil {
		t.Fatalf("SetupServerManaged: %v", err)
	}
	if view.KeyMode != service.VaultKeyModeServer || len(view.WrappedVaultKey) != 0 {
		t.Fatalf("view = %+v, want server mode without wrapped key", view)
	}
	if len(key) != 32 {
		t.Fatalf("vault key length = %d, want 32", len(key))
	}

	got, err := vs.ServerVaultKey(t.Context(), uid)
	if err != nil {
		t.Fatalf("ServerVaultKey: %v", err)
	}
	if !bytes.Equal(got, key) {
		t.Fatal("ServerVaultKey returned a different key")
	}

	status, err := vs.Status(t.Context(), uid)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.KeyMode != service.VaultKeyModeServer || status.DeploymentKeyMode != service.VaultKeyModeServer {
		t.Fatalf("status = %+v", status)
	}
}

func TestVaultServerMode_LockedServerKey(t *testing.T) {
	svc, vs := newServerModeVault(t)
	uid := createUserHelper(t, svc, "alice")
	setVaultKeyMode(t, svc, service.VaultKeyModeServer)
	if _, _, err := vs.SetupServerManaged(t.Context(), uid, nil); err != nil {
		t.Fatalf("SetupServerManaged: %v", err)
	}

	svc.KeyManager().Lock()
	if _, err := vs.ServerVaultKey(t.Context(), uid); !errors.Is(err, service.ErrVaultServerKeyLocked) {
		t.Fatalf("want ErrVaultServerKeyLocked, got %v", err)
	}
}

func TestVaultServerMode_ConvertRoundTrip(t *testing.T) {
	svc, vs := newServerModeVault(t)
	uid := createUserHelper(t, svc, "alice")
	setVaultKeyMode(t, svc, service.VaultKeyModeUser)

	req := validSetupRequest(t)
	if _, err := vs.Setup(t.Context(), uid, req); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	vaultKey := mustRand(t, 32)

	if _, err := vs.ConvertToServer(t.Context(), uid, &service.VaultConvertToServerRequest{
		VaultKey: vaultKey, SecretKeyHash: req.SecretKeyHash,
	}); !errors.Is(err, service.ErrBadRequest) {
		t.Fatalf("convert while deployment is in user mode: want ErrBadRequest, got %v", err)
	}

	setVaultKeyMode(t, svc, service.VaultKeyModeServer)

	if _, err := vs.ConvertToServer(t.Context(), uid, &service.VaultConvertToServerRequest{
		VaultKey: vaultKey, SecretKeyHash: mustRand(t, 32),
	}); !errors.Is(err, service.ErrUnauthorized) {
		t.Fatalf("wrong secret key hash: want ErrUnauthorized, got %v", err)
	}

	view, err := vs.ConvertToServer(t.Context(), uid, &service.VaultConvertToServerRequest{
		VaultKey: vaultKey, SecretKeyHash: req.SecretKeyHash,
	})
	if err != nil {
		t.Fatalf("ConvertToServer: %v", err)
	}
	if view.KeyMode != service.VaultKeyModeServer {
		t.Fatalf("key mode = %q", view.KeyMode)
	}
	got, err := vs.ServerVaultKey(t.Context(), uid)
	if err != nil || !bytes.Equal(got, vaultKey) {
		t.Fatalf("ServerVaultKey after convert: %v", err)
	}
	if _, err := vs.RotateMasterPassword(t.Context(), uid, validSetupRequest(t)); !errors.Is(err, service.ErrBadRequest) {
		t.Fatalf("rotate on server-managed vault: want ErrBadRequest, got %v", err)
	}

	setVaultKeyMode(t, svc, service.VaultKeyModeUser)

	back := validSetupRequest(t)
	view, err = vs.ConvertToUser(t.Context(), uid, back)
	if err != nil {
		t.Fatalf("ConvertToUser: %v", err)
	}
	if view.KeyMode != service.VaultKeyModeUser || !bytes.Equal(view.WrappedVaultKey, back.WrappedVaultKey) {
		t.Fatalf("view after ConvertToUser = %+v", view)
	}
	if _, err := vs.ServerVaultKey(t.Context(), uid); !errors.Is(err, service.ErrBadRequest) {
		t.Fatalf("ServerVaultKey on user vault: want ErrBadRequest, got %v", err)
	}
	if err := vs.UnlockCheck(t.Context(), uid, "", &service.UnlockCheckRequest{SecretKeyHash: back.SecretKeyHash}); err != nil {
		t.Fatalf("UnlockCheck with new secret key: %v", err)
	}
}

func TestVaultServerMode_ServerKeyRotationReseals(t *testing.T) {
	svc, vs := newServerModeVault(t)
	uid := createUserHelper(t, svc, "alice")
	setVaultKeyMode(t, svc, service.VaultKeyModeServer)
	_, key, err := vs.SetupServerManaged(t.Context(), uid, nil)
	if err != nil {
		t.Fatalf("SetupServerManaged: %v", err)
	}

	if err := svc.RotateServerKey(t.Context(), "operator-passphrase", "new-passphrase"); err != nil {
		t.Fatalf("RotateServerKey: %v", err)
	}
	svc.KeyManager().Lock()
	if err := svc.UnlockServerKey(t.Context(), "new-passphrase"); err != nil {
		t.Fatalf("UnlockServerKey: %v", err)
	}

	got, err := vs.ServerVaultKey(t.Context(), uid)
	if err != nil {
		t.Fatalf("ServerVaultKey after rotation: %v", err)
	}
	if !bytes.Equal(got, key) {
		t.Fatal("vault key changed across server key rotation")
	}
}
