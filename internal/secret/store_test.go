package secret_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/rakunlabs/pika/internal/external"
	"github.com/rakunlabs/pika/internal/hook"
	"github.com/rakunlabs/pika/internal/secret"
	"github.com/rakunlabs/pika/internal/secret/crypto"
	"github.com/rakunlabs/pika/internal/secret/envelope"
	"github.com/rakunlabs/pika/internal/secret/keymgr"
	"github.com/rakunlabs/pika/internal/service"
	bwstore "github.com/rakunlabs/pika/internal/storage/bw"
)

const (
	vaultToken   = "hvs.SUPER-SECRET-vault-token"
	awsSecret    = "aws-SUPER-SECRET-key"
	redisPass    = "redis-SUPER-SECRET-pass"
	oauthSecret  = "oauth-SUPER-SECRET-client"
	staticToken  = "static-SUPER-SECRET-token"
	vaultAddress = "https://vault.example.com"
)

func newRawStore(t *testing.T) *bwstore.Storage {
	t.Helper()
	store, err := bwstore.New(t.Context(), &bwstore.Config{InMemory: true})
	if err != nil {
		t.Fatalf("bw.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func newUnlockedManager(t *testing.T) *keymgr.Manager {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	enc, err := crypto.NewChaCha20(key)
	if err != nil {
		t.Fatalf("NewChaCha20: %v", err)
	}
	mgr := keymgr.New()
	if err := mgr.Unlock(enc); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	return mgr
}

func secretSettings() *service.Settings {
	return &service.Settings{
		External: map[string]external.External{
			"vault": {Vault: &external.Vault{Address: vaultAddress, Mount: "secret", Token: vaultToken}},
			"aws":   {AWS: &external.AWS{Region: "eu-west-1", AccessKey: "AKIA-PUB", SecretKey: awsSecret}},
		},
		Hooks: []hook.Hook{{
			Name:    "redis",
			Enabled: true,
			Targets: []hook.Target{{
				Type:  "redis",
				Redis: &hook.RedisTarget{Address: "redis:6379", Password: redisPass, Channel: "events"},
			}},
		}},
		Auth: &service.AuthSettings{
			OAuth2: []service.OAuth2StrategySettings{{Name: "google", ClientID: "cid", ClientSecret: oauthSecret}},
		},
		PublicEndpoints: []service.PublicEndpoint{{
			ID:   "ep1",
			Auth: service.EndpointAuth{Mode: "static_token", StaticTokens: []string{staticToken}},
		}},
	}
}

func allSecrets() []string {
	return []string{vaultToken, awsSecret, redisPass, oauthSecret, staticToken}
}

func assertNoPlaintext(t *testing.T, raw *service.Settings) {
	t.Helper()
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal raw row: %v", err)
	}
	for _, s := range allSecrets() {
		if bytes.Contains(b, []byte(s)) {
			t.Errorf("plaintext secret %q found in raw backend row", s)
		}
		if bytes.Contains(raw.SensitivePayload, []byte(s)) {
			t.Errorf("plaintext secret %q found in sealed payload", s)
		}
	}
}

func TestSettings_EncryptedAtRest(t *testing.T) {
	ctx := t.Context()
	raw := newRawStore(t)
	mgr := newUnlockedManager(t)
	st := secret.New(raw, mgr)

	if err := st.Settings().Set(ctx, secretSettings()); err != nil {
		t.Fatalf("Set: %v", err)
	}

	rawRow, err := raw.Settings().Get(ctx)
	if err != nil {
		t.Fatalf("raw Get: %v", err)
	}
	if len(rawRow.SensitivePayload) == 0 {
		t.Fatal("SensitivePayload is empty; secrets were not sealed")
	}
	if !envelope.IsSealed(rawRow.SensitivePayload) {
		t.Fatal("SensitivePayload is not an envelope")
	}
	assertNoPlaintext(t, rawRow)
	if rawRow.External["vault"].Vault.Address != vaultAddress {
		t.Errorf("public vault address not stored in plaintext: %q", rawRow.External["vault"].Vault.Address)
	}

	got, err := st.Settings().Get(ctx)
	if err != nil {
		t.Fatalf("wrapped Get: %v", err)
	}
	if got.External["vault"].Vault.Token != vaultToken {
		t.Errorf("vault token = %q", got.External["vault"].Vault.Token)
	}
	if got.External["aws"].AWS.SecretKey != awsSecret {
		t.Errorf("aws secret = %q", got.External["aws"].AWS.SecretKey)
	}
	if got.Hooks[0].Targets[0].Redis.Password != redisPass {
		t.Errorf("redis password = %q", got.Hooks[0].Targets[0].Redis.Password)
	}
	if got.Auth.OAuth2[0].ClientSecret != oauthSecret {
		t.Errorf("oauth secret = %q", got.Auth.OAuth2[0].ClientSecret)
	}
	if toks := got.PublicEndpoints[0].Auth.StaticTokens; len(toks) != 1 || toks[0] != staticToken {
		t.Errorf("static tokens = %v", toks)
	}
}

func TestSettings_NoSecretsNoPayload(t *testing.T) {
	ctx := t.Context()
	raw := newRawStore(t)
	st := secret.New(raw, newUnlockedManager(t))

	s := &service.Settings{External: map[string]external.External{
		"vault": {Vault: &external.Vault{Address: vaultAddress}},
	}}
	if err := st.Settings().Set(ctx, s); err != nil {
		t.Fatalf("Set: %v", err)
	}
	rawRow, err := raw.Settings().Get(ctx)
	if err != nil {
		t.Fatalf("raw Get: %v", err)
	}
	if len(rawRow.SensitivePayload) != 0 {
		t.Fatalf("SensitivePayload = %d bytes; want empty", len(rawRow.SensitivePayload))
	}
}

func TestSettings_LockedRejectsSecrets(t *testing.T) {
	ctx := t.Context()
	raw := newRawStore(t)
	st := secret.New(raw, keymgr.New())

	in := secretSettings()
	err := st.Settings().Set(ctx, in)
	if err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("Set while locked err = %v; want locked error", err)
	}
	// Caller's struct must be restored after the rejected write.
	if in.External["vault"].Vault.Token != vaultToken {
		t.Errorf("caller vault token mutated: %q", in.External["vault"].Vault.Token)
	}
	if in.Hooks[0].Targets[0].Redis.Password != redisPass {
		t.Errorf("caller redis password mutated: %q", in.Hooks[0].Targets[0].Redis.Password)
	}
	if toks := in.PublicEndpoints[0].Auth.StaticTokens; len(toks) != 1 || toks[0] != staticToken {
		t.Errorf("caller static tokens mutated: %v", toks)
	}
	// Nothing must have been persisted.
	if row, err := raw.Settings().Get(ctx); err == nil && row != nil {
		b, _ := json.Marshal(row)
		for _, s := range allSecrets() {
			if bytes.Contains(b, []byte(s)) {
				t.Errorf("secret %q persisted while locked", s)
			}
		}
	}
}

func TestSettings_LockedAllowsSecretFreeWrites(t *testing.T) {
	ctx := t.Context()
	raw := newRawStore(t)
	st := secret.New(raw, keymgr.New())

	s := &service.Settings{External: map[string]external.External{
		"vault": {Vault: &external.Vault{Address: vaultAddress}},
	}}
	if err := st.Settings().Set(ctx, s); err != nil {
		t.Fatalf("Set secret-free while locked: %v", err)
	}
	got, err := st.Settings().Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.External["vault"].Vault.Address != vaultAddress {
		t.Errorf("address = %q", got.External["vault"].Vault.Address)
	}
}

func TestSettings_LockedGetReturnsStrippedRow(t *testing.T) {
	ctx := t.Context()
	raw := newRawStore(t)
	mgr := newUnlockedManager(t)
	st := secret.New(raw, mgr)

	if err := st.Settings().Set(ctx, secretSettings()); err != nil {
		t.Fatalf("Set: %v", err)
	}
	mgr.Lock()

	got, err := st.Settings().Get(ctx)
	if err != nil {
		t.Fatalf("Get while locked: %v", err)
	}
	if got.External["vault"].Vault.Token != "" {
		t.Errorf("vault token leaked while locked: %q", got.External["vault"].Vault.Token)
	}
	if got.External["vault"].Vault.Address != vaultAddress {
		t.Errorf("public address missing while locked: %q", got.External["vault"].Vault.Address)
	}
	if len(got.SensitivePayload) == 0 {
		t.Error("sealed payload should still be carried through while locked")
	}
}

func TestSettings_WrongKeyReportsCorrupt(t *testing.T) {
	ctx := t.Context()
	raw := newRawStore(t)
	mgr := newUnlockedManager(t)
	st := secret.New(raw, mgr)

	if err := st.Settings().Set(ctx, secretSettings()); err != nil {
		t.Fatalf("Set: %v", err)
	}

	other := newUnlockedManager(t)
	got, err := secret.New(raw, other).Settings().Get(ctx)
	if !errors.Is(err, secret.ErrSealedCorrupt) {
		t.Fatalf("Get with wrong key err = %v; want ErrSealedCorrupt", err)
	}
	if got == nil {
		t.Fatal("row should be returned alongside ErrSealedCorrupt")
	}
	if got.External["vault"].Vault.Token != "" {
		t.Errorf("vault token populated despite decrypt failure")
	}
}

func TestSettings_TamperedPayloadReportsCorrupt(t *testing.T) {
	ctx := t.Context()
	raw := newRawStore(t)
	mgr := newUnlockedManager(t)
	st := secret.New(raw, mgr)

	if err := st.Settings().Set(ctx, secretSettings()); err != nil {
		t.Fatalf("Set: %v", err)
	}
	row, err := raw.Settings().Get(ctx)
	if err != nil {
		t.Fatalf("raw Get: %v", err)
	}
	row.SensitivePayload[len(row.SensitivePayload)-1] ^= 0xFF
	if err := raw.Settings().Set(ctx, row); err != nil {
		t.Fatalf("raw Set: %v", err)
	}
	if _, err := st.Settings().Get(ctx); !errors.Is(err, secret.ErrSealedCorrupt) {
		t.Fatalf("Get tampered err = %v; want ErrSealedCorrupt", err)
	}
}

func TestSettings_TxSealsToo(t *testing.T) {
	ctx := t.Context()
	raw := newRawStore(t)
	st := secret.New(raw, newUnlockedManager(t))

	err := st.Tx(ctx, func(ctx context.Context, tx service.Storage) error {
		return tx.Settings().Set(ctx, secretSettings())
	})
	if err != nil {
		t.Fatalf("Tx: %v", err)
	}
	rawRow, err := raw.Settings().Get(ctx)
	if err != nil {
		t.Fatalf("raw Get: %v", err)
	}
	assertNoPlaintext(t, rawRow)
	got, err := st.Settings().Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.External["vault"].Vault.Token != vaultToken {
		t.Errorf("vault token = %q", got.External["vault"].Vault.Token)
	}
}

func TestStorage_KeyManagerAndPassthrough(t *testing.T) {
	raw := newRawStore(t)
	mgr := keymgr.New()
	st := secret.New(raw, mgr)

	if st.KeyManager() != mgr {
		t.Fatal("KeyManager() did not return the configured manager")
	}
	if st.Version() != raw.Version() {
		t.Errorf("Version = %d; raw = %d", st.Version(), raw.Version())
	}

	var _ service.Storage = st
}
