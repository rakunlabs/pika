package service

import (
	"context"
	"sync"

	"github.com/rakunlabs/pika/internal/external"
	"github.com/rakunlabs/pika/internal/hook"
	"github.com/rakunlabs/pika/internal/secret/keymgr"
)

type Service struct {
	store Storage

	// External backend clients, cached by a hash of the full client
	// configuration (credentials included) so editing a resource yields a
	// fresh client. Purged on every settings save; see purgeExternalClients.
	vaultClients    *clientCache[*external.VaultClient]
	kubeClients     *clientCache[*external.KubeClient]
	gcpClients      *clientCache[*external.GCPSecretManagerClient]
	gcpParamClients *clientCache[*external.GCPParameterManagerClient]
	azureClients    *clientCache[*external.AzureKeyVaultClient]

	// hookDispatcher emits events when config operations occur.
	// May be nil if hooks are not configured.
	hookDispatcher *hook.Dispatcher

	// auditLog buffers audit entries; nil when storage is absent.
	auditLog *auditLog
	// tokenUsage batches API token last-used timestamps.
	tokenUsage *tokenUsage
	// canWriteBackground gates background storage writes; nil = always.
	canWriteBackground func() bool
	// bgWorker owns Service-level background loops.
	bgWorker *worker

	// coordMu guards the swappable coordinators below (passkeys, totp,
	// vault), which are replaced on auth reload while requests run.
	coordMu sync.RWMutex

	// passkeys is the WebAuthn coordinator. nil when the deployment
	// has no passkey configuration (e.g. RPID unset). Set by
	// SetPasskeys at boot; consumed via Passkeys() with a nil-check.
	passkeys *PasskeyService

	// totp is the TOTP / 2FA coordinator. nil when TOTP is disabled
	// in settings. Set by SetTOTPService at boot; consumed via
	// TOTPCoord() with a nil-check. The MFA strategy reads
	// IsEnabledForUser to decide whether to step up at login.
	totp *TOTPService

	// vault is the personal-vault coordinator (1Password-style E2E
	// item store). nil when the deployment doesn't enable the
	// feature. Set by SetVaultService at boot; consumed via
	// VaultCoord() with a nil-check. The /api/v1/me/vault/* handler
	// chain skips registration when this is nil so the routes 404
	// instead of 503-ing every call.
	vault *VaultService

	// keyManager owns the lifecycle of the at-rest server encryption
	// key. Set by SetKeyManager at boot; nil-safe everywhere it's
	// consumed (keyops.go gates each method on a non-nil check). The
	// HTTP layer reads its state via GetKeyStatus to decide between
	// the unlock screen and the normal app shell.
	keyManager *keymgr.Manager

	// rootCtx is a server-lifetime context set once at boot via
	// SetRootContext. It is handed to background goroutines that must
	// outlive any single request — currently the Vault AppRole token
	// renewal loop (see getVaultClient → StartRenewal). Without this,
	// renewal would bind to the first request's context and stop when
	// that request completes, forcing a fresh AppRole login on every
	// token TTL instead of cheaply extending the existing lease.
	rootCtx context.Context
}

func New(store Storage) *Service {
	s := &Service{
		store:           store,
		vaultClients:    newClientCache[*external.VaultClient](),
		kubeClients:     newClientCache[*external.KubeClient](),
		gcpClients:      newClientCache[*external.GCPSecretManagerClient](),
		gcpParamClients: newClientCache[*external.GCPParameterManagerClient](),
		azureClients:    newClientCache[*external.AzureKeyVaultClient](),
		tokenUsage:      newTokenUsage(),
		bgWorker:        newWorker(),
	}
	if store != nil {
		s.auditLog = &auditLog{retention: DefaultAuditRetention}
		s.startTokenUsageFlusher()
		s.startAuditWorker()
	}
	return s
}

// SessionStorage returns the session storage backend.
// Used by the session store for DB-backed session persistence.
func (s *Service) SessionStorage() SessionStorage {
	return s.store.Sessions()
}

// SetRootContext stores a server-lifetime context used by background
// goroutines that must outlive individual requests (e.g. Vault token
// renewal). Call once at boot, before serving traffic, with the
// context that is cancelled on server shutdown.
func (s *Service) SetRootContext(ctx context.Context) {
	s.rootCtx = ctx
}

// SetHookDispatcher sets the hook dispatcher for emitting config events.
func (s *Service) SetHookDispatcher(d *hook.Dispatcher) {
	s.hookDispatcher = d
}

// emitHook emits a hook event if the dispatcher is set.
func (s *Service) emitHook(event hook.Event) {
	if s.hookDispatcher != nil {
		s.hookDispatcher.Emit(event)
	}
}

// ── external.Deps satisfaction ──
//
// The four exported wrappers below let *Service satisfy
// external.Deps without renaming the long-standing private helpers
// (getVaultClient/...) that the rest of the data path still calls.
// They are intentionally trivial; if you find yourself adding logic
// here, move it into the underlying private helper instead so both
// call sites stay in sync.

// VaultClient implements external.Deps.
func (s *Service) VaultClient(ctx context.Context, v *external.Vault) *external.VaultClient {
	return s.getVaultClient(ctx, v)
}

// KubeClient implements external.Deps.
func (s *Service) KubeClient(k *external.Kubernetes) (*external.KubeClient, error) {
	return s.getKubeClient(k)
}

// GCPClient implements external.Deps.
func (s *Service) GCPClient(g *external.GCP) (*external.GCPSecretManagerClient, error) {
	return s.getGCPClient(g)
}

// GCPParameterClient implements external.Deps.
func (s *Service) GCPParameterClient(g *external.GCPParameter) (*external.GCPParameterManagerClient, error) {
	return s.getGCPParameterClient(g)
}

// AzureClient implements external.Deps.
func (s *Service) AzureClient(a *external.Azure) *external.AzureKeyVaultClient {
	return s.getAzureClient(a)
}

// getKubeClient returns a cached or new KubeClient for the given Kubernetes config.
func (s *Service) getKubeClient(k8s *external.Kubernetes) (*external.KubeClient, error) {
	return s.kubeClients.get(configCacheKey(k8s), func() (*external.KubeClient, func(), error) {
		client, err := external.NewKubeClient(k8s)
		return client, nil, err
	})
}

// getVaultClient returns a cached or new VaultClient for the given vault config.
// If the client doesn't exist yet, it creates one, configures authentication,
// and starts background token renewal.
func (s *Service) getVaultClient(ctx context.Context, vault *external.Vault) *external.VaultClient {
	// Only the fields that shape the client (address, credentials, proxy)
	// go into the key; mount/kv_version are per-call and share a client.
	key := configCacheKey(struct {
		Address, Token, Proxy, ProxyMode string
		AppRole                          *external.VaultAppRole
	}{vault.Address, vault.Token, vault.Proxy, vault.ProxyMode, vault.AppRole})

	client, _ := s.vaultClients.get(key, func() (*external.VaultClient, func(), error) {
		client := external.NewVaultClient(vault.Address, vault.ProxyMode, vault.Proxy)

		if vault.AppRole != nil {
			client.SetAppRole(vault.AppRole)
			// The renewal goroutine must outlive the request that first
			// created this client, so bind it to the server-lifetime
			// context rather than the per-request ctx. Fall back to the
			// request ctx only when no root context was set (e.g. tests).
			// Cancelled when the client is evicted from the cache.
			parent := s.rootCtx
			if parent == nil {
				parent = ctx
			}
			renewCtx, cancel := context.WithCancel(parent)
			client.StartRenewal(renewCtx)
			return client, cancel, nil
		}

		if vault.Token != "" {
			client.SetToken(vault.Token)
		}
		return client, nil, nil
	})

	return client
}

// getGCPClient returns a cached or new GCP Secret Manager client.
func (s *Service) getGCPClient(gcp *external.GCP) (*external.GCPSecretManagerClient, error) {
	key := configCacheKey(struct {
		ServiceAccountJSON, Proxy, ProxyMode string
	}{gcp.ServiceAccountJSON, gcp.Proxy, gcp.ProxyMode})

	return s.gcpClients.get(key, func() (*external.GCPSecretManagerClient, func(), error) {
		client, err := external.NewGCPSecretManagerClient(gcp.ServiceAccountJSON, gcp.ProxyMode, gcp.Proxy)
		return client, nil, err
	})
}

// getGCPParameterClient returns a cached or new GCP Parameter Manager
// client. Different locations need separate clients even when they share
// credentials, because each client pins its own location for every call.
func (s *Service) getGCPParameterClient(g *external.GCPParameter) (*external.GCPParameterManagerClient, error) {
	location := g.GetLocation()
	key := configCacheKey(struct {
		ServiceAccountJSON, Location, Proxy, ProxyMode string
	}{g.ServiceAccountJSON, location, g.Proxy, g.ProxyMode})

	return s.gcpParamClients.get(key, func() (*external.GCPParameterManagerClient, func(), error) {
		client, err := external.NewGCPParameterManagerClient(g.ServiceAccountJSON, location, g.ProxyMode, g.Proxy)
		return client, nil, err
	})
}

// getAzureClient returns a cached or new Azure Key Vault client.
func (s *Service) getAzureClient(azure *external.Azure) *external.AzureKeyVaultClient {
	key := configCacheKey(struct {
		VaultURL, TenantID, ClientID, ClientSecret, Proxy, ProxyMode string
	}{azure.VaultURL, azure.TenantID, azure.ClientID, azure.ClientSecret, azure.Proxy, azure.ProxyMode})

	client, _ := s.azureClients.get(key, func() (*external.AzureKeyVaultClient, func(), error) {
		return external.NewAzureKeyVaultClient(azure.VaultURL, azure.TenantID, azure.ClientID, azure.ClientSecret, azure.ProxyMode, azure.Proxy), nil, nil
	})

	return client
}

// purgeExternalClients drops every cached external client (stopping
// background Vault renewals). Called after settings change so removed or
// edited resources don't keep stale clients alive.
func (s *Service) purgeExternalClients() {
	s.vaultClients.purge()
	s.kubeClients.purge()
	s.gcpClients.purge()
	s.gcpParamClients.purge()
	s.azureClients.purge()
}

// Close stops the background workers of every attached coordinator.
// Call once on shutdown.
func (s *Service) Close() {
	s.bgWorker.stop()
	s.SetPasskeyService(nil)
	s.SetTOTPService(nil)
	s.SetVaultService(nil)
	s.purgeExternalClients()
}
