package service

import (
	"sync"

	"github.com/rakunlabs/pika/internal/external"
)

const (
	keyFolder   = "_folder"
	keyFile     = "_file"
	keySettings = "_settings"
)

type Service struct {
	store Storage

	// vaultClients caches Vault clients keyed by vault address.
	// This avoids re-authenticating on every config fetch.
	vaultMu      sync.RWMutex
	vaultClients map[string]*external.VaultClient
}

func New(store Storage) *Service {
	return &Service{
		store:        store,
		vaultClients: make(map[string]*external.VaultClient),
	}
}

// getVaultClient returns a cached or new VaultClient for the given vault config.
// If the client doesn't exist yet, it creates one and configures authentication.
func (s *Service) getVaultClient(vault *external.Vault) *external.VaultClient {
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
	} else if vault.Token != "" {
		client.SetToken(vault.Token)
	}

	s.vaultClients[vault.Address] = client
	return client
}
