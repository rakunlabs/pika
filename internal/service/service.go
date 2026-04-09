package service

import (
	"context"
	"sync"

	"github.com/rakunlabs/pika/internal/external"
	"github.com/rakunlabs/pika/internal/hook"
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

	// azureClients caches Azure Key Vault clients keyed by vault URL.
	azureMu      sync.RWMutex
	azureClients map[string]*external.AzureKeyVaultClient

	// hookDispatcher emits events when config operations occur.
	// May be nil if hooks are not configured.
	hookDispatcher *hook.Dispatcher
}

func New(store Storage) *Service {
	return &Service{
		store:        store,
		vaultClients: make(map[string]*external.VaultClient),
		kubeClients:  make(map[string]*external.KubeClient),
		gcpClients:   make(map[string]*external.GCPSecretManagerClient),
		azureClients: make(map[string]*external.AzureKeyVaultClient),
	}
}

// SessionStorage returns the session storage backend.
// Used by the session store for DB-backed session persistence.
func (s *Service) SessionStorage() SessionStorage {
	return s.store.Sessions()
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

// getKubeClient returns a cached or new KubeClient for the given Kubernetes config.
func (s *Service) getKubeClient(k8s *external.Kubernetes) (*external.KubeClient, error) {
	key := k8s.Kubeconfig // "" for in-cluster

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

	client, err := external.NewKubeClient(k8s.Kubeconfig)
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
	s.vaultMu.RLock()
	client, exists := s.vaultClients[vault.Address]
	s.vaultMu.RUnlock()

	if exists {
		return client
	}

	s.vaultMu.Lock()
	defer s.vaultMu.Unlock()

	// Double-check after acquiring write lock
	if client, exists = s.vaultClients[vault.Address]; exists {
		return client
	}

	client = external.NewVaultClient(vault.Address)

	// Configure authentication
	if vault.AppRole != nil {
		client.SetAppRole(vault.AppRole)
		// Enable background token renewal for AppRole-based auth
		client.StartRenewal(ctx)
	} else if vault.Token != "" {
		client.SetToken(vault.Token)
	}

	s.vaultClients[vault.Address] = client
	return client
}

// getGCPClient returns a cached or new GCP Secret Manager client.
func (s *Service) getGCPClient(gcp *external.GCP) (*external.GCPSecretManagerClient, error) {
	key := gcp.ServiceAccountJSON // use the full JSON as cache key (contains project_id)

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

	client, err := external.NewGCPSecretManagerClient(gcp.ServiceAccountJSON)
	if err != nil {
		return nil, err
	}

	s.gcpClients[key] = client
	return client, nil
}

// getAzureClient returns a cached or new Azure Key Vault client.
func (s *Service) getAzureClient(azure *external.Azure) *external.AzureKeyVaultClient {
	s.azureMu.RLock()
	client, exists := s.azureClients[azure.VaultURL]
	s.azureMu.RUnlock()

	if exists {
		return client
	}

	s.azureMu.Lock()
	defer s.azureMu.Unlock()

	if client, exists = s.azureClients[azure.VaultURL]; exists {
		return client
	}

	client = external.NewAzureKeyVaultClient(azure.VaultURL, azure.TenantID, azure.ClientID, azure.ClientSecret)
	s.azureClients[azure.VaultURL] = client
	return client
}
