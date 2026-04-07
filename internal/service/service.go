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

	// hookDispatcher emits events when config operations occur.
	// May be nil if hooks are not configured.
	hookDispatcher *hook.Dispatcher
}

func New(store Storage) *Service {
	return &Service{
		store:        store,
		vaultClients: make(map[string]*external.VaultClient),
		kubeClients:  make(map[string]*external.KubeClient),
	}
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
