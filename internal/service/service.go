package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"

	"github.com/rakunlabs/pika/internal/external"
	"github.com/rakunlabs/pika/internal/hook"
	"github.com/rakunlabs/pika/internal/secret/keymgr"
)

type Service struct {
	store Storage

	// vaultClients caches Vault clients keyed by vault address.
	// This avoids re-authenticating on every config fetch.
	vaultMu      sync.RWMutex
	vaultClients map[string]*external.VaultClient

	// kubeClients caches Kubernetes clients keyed by kubeconfig path (or "" for in-cluster).
	kubeMu      sync.RWMutex
	kubeClients map[string]*external.KubeClient

	// gcpClients caches GCP Secret Manager clients keyed by project ID.
	gcpMu      sync.RWMutex
	gcpClients map[string]*external.GCPSecretManagerClient

	// gcpParamClients caches GCP Parameter Manager clients keyed by
	// "<service account JSON>|<location>". Parameter Manager is
	// location-scoped, so the same service account can drive multiple
	// clients (e.g. one for "global", one for "us-central1") and we
	// want each to keep its own cached access token.
	gcpParamMu      sync.RWMutex
	gcpParamClients map[string]*external.GCPParameterManagerClient

	// azureClients caches Azure Key Vault clients keyed by vault URL.
	azureMu      sync.RWMutex
	azureClients map[string]*external.AzureKeyVaultClient

	// hookDispatcher emits events when config operations occur.
	// May be nil if hooks are not configured.
	hookDispatcher *hook.Dispatcher

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
	return &Service{
		store:        store,
		vaultClients: make(map[string]*external.VaultClient),
		kubeClients:  make(map[string]*external.KubeClient),
		gcpClients:      make(map[string]*external.GCPSecretManagerClient),
		gcpParamClients: make(map[string]*external.GCPParameterManagerClient),
		azureClients:    make(map[string]*external.AzureKeyVaultClient),
	}
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

// kubeClientCacheKey derives a stable cache key for a Kubernetes external resource.
// Inline kubeconfig content is hashed (not stored verbatim) so the key stays bounded.
func kubeClientCacheKey(k8s *external.Kubernetes) string {
	if k8s == nil {
		return "in-cluster"
	}
	// Suffix the proxy so the same kubeconfig routed through different
	// proxies maps to distinct cached clients.
	proxySuffix := "|" + k8s.Proxy
	if k8s.KubeconfigContent != "" {
		sum := sha256.Sum256([]byte(k8s.KubeconfigContent))
		return "inline:" + hex.EncodeToString(sum[:]) + proxySuffix
	}
	if k8s.Kubeconfig != "" {
		return "path:" + k8s.Kubeconfig + proxySuffix
	}
	return "in-cluster" + proxySuffix
}

// getKubeClient returns a cached or new KubeClient for the given Kubernetes config.
func (s *Service) getKubeClient(k8s *external.Kubernetes) (*external.KubeClient, error) {
	key := kubeClientCacheKey(k8s)

	s.kubeMu.RLock()
	client, exists := s.kubeClients[key]
	s.kubeMu.RUnlock()

	if exists {
		return client, nil
	}

	s.kubeMu.Lock()
	defer s.kubeMu.Unlock()

	// Double-check after acquiring write lock
	if client, exists = s.kubeClients[key]; exists {
		return client, nil
	}

	client, err := external.NewKubeClient(k8s)
	if err != nil {
		return nil, err
	}

	s.kubeClients[key] = client
	return client, nil
}

// getVaultClient returns a cached or new VaultClient for the given vault config.
// If the client doesn't exist yet, it creates one, configures authentication,
// and starts background token renewal.
func (s *Service) getVaultClient(ctx context.Context, vault *external.Vault) *external.VaultClient {
	// Cache key includes the proxy so two resources that share a Vault
	// address but route through different proxies get distinct clients.
	cacheKey := vault.Address + "|" + vault.Proxy

	s.vaultMu.RLock()
	client, exists := s.vaultClients[cacheKey]
	s.vaultMu.RUnlock()

	if exists {
		return client
	}

	s.vaultMu.Lock()
	defer s.vaultMu.Unlock()

	// Double-check after acquiring write lock
	if client, exists = s.vaultClients[cacheKey]; exists {
		return client
	}

	client = external.NewVaultClient(vault.Address, vault.Proxy)

	// Configure authentication
	if vault.AppRole != nil {
		client.SetAppRole(vault.AppRole)
		// Enable background token renewal for AppRole-based auth.
		// The renewal goroutine must outlive the request that first
		// created this client, so bind it to the server-lifetime
		// context rather than the per-request ctx. Fall back to the
		// request ctx only when no root context was set (e.g. tests).
		renewCtx := s.rootCtx
		if renewCtx == nil {
			renewCtx = ctx
		}
		client.StartRenewal(renewCtx)
	} else if vault.Token != "" {
		client.SetToken(vault.Token)
	}

	s.vaultClients[cacheKey] = client
	return client
}

// getGCPClient returns a cached or new GCP Secret Manager client.
func (s *Service) getGCPClient(gcp *external.GCP) (*external.GCPSecretManagerClient, error) {
	// Cache key is the full JSON (contains project_id) plus the proxy
	// so resources sharing credentials but different proxies stay apart.
	key := gcp.ServiceAccountJSON + "|" + gcp.Proxy

	s.gcpMu.RLock()
	client, exists := s.gcpClients[key]
	s.gcpMu.RUnlock()

	if exists {
		return client, nil
	}

	s.gcpMu.Lock()
	defer s.gcpMu.Unlock()

	if client, exists = s.gcpClients[key]; exists {
		return client, nil
	}

	client, err := external.NewGCPSecretManagerClient(gcp.ServiceAccountJSON, gcp.Proxy)
	if err != nil {
		return nil, err
	}

	s.gcpClients[key] = client
	return client, nil
}

// getGCPParameterClient returns a cached or new GCP Parameter Manager
// client. Cache key combines the service-account JSON and the
// requested location: different locations need separate clients even
// when they share credentials, because each client pins its own
// location for every call.
func (s *Service) getGCPParameterClient(g *external.GCPParameter) (*external.GCPParameterManagerClient, error) {
	location := g.GetLocation()
	key := g.ServiceAccountJSON + "|" + location + "|" + g.Proxy

	s.gcpParamMu.RLock()
	client, exists := s.gcpParamClients[key]
	s.gcpParamMu.RUnlock()

	if exists {
		return client, nil
	}

	s.gcpParamMu.Lock()
	defer s.gcpParamMu.Unlock()

	if client, exists = s.gcpParamClients[key]; exists {
		return client, nil
	}

	client, err := external.NewGCPParameterManagerClient(g.ServiceAccountJSON, location, g.Proxy)
	if err != nil {
		return nil, err
	}

	s.gcpParamClients[key] = client
	return client, nil
}

// getAzureClient returns a cached or new Azure Key Vault client.
func (s *Service) getAzureClient(azure *external.Azure) *external.AzureKeyVaultClient {
	// Cache key includes the proxy so the same vault URL routed through
	// different proxies yields distinct clients.
	cacheKey := azure.VaultURL + "|" + azure.Proxy

	s.azureMu.RLock()
	client, exists := s.azureClients[cacheKey]
	s.azureMu.RUnlock()

	if exists {
		return client
	}

	s.azureMu.Lock()
	defer s.azureMu.Unlock()

	if client, exists = s.azureClients[cacheKey]; exists {
		return client
	}

	client = external.NewAzureKeyVaultClient(azure.VaultURL, azure.TenantID, azure.ClientID, azure.ClientSecret, azure.Proxy)
	s.azureClients[cacheKey] = client
	return client
}
