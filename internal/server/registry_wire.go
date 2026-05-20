package server

import (
	"fmt"

	"github.com/rakunlabs/pika/internal/registry"
	"github.com/rakunlabs/pika/internal/registry/cargo"
	"github.com/rakunlabs/pika/internal/registry/docker"
	"github.com/rakunlabs/pika/internal/registry/goproxy"
	"github.com/rakunlabs/pika/internal/registry/helm"
	"github.com/rakunlabs/pika/internal/registry/maven"
	"github.com/rakunlabs/pika/internal/registry/npm"
	"github.com/rakunlabs/pika/internal/registry/pypi"
	"github.com/rakunlabs/pika/internal/service"
)

// protocolFactory bundles the three factory constructors for one
// protocol. Each protocol implements Local + Remote + Virtual; the
// Virtual constructor needs a Manager handle to resolve members at
// request time, hence the function shape.
//
// A nil Virtual is allowed for future protocols that don't yet
// support virtualisation; the wiring skips it cleanly.
type protocolFactory struct {
	Type    string
	Local   registry.Factory
	Remote  registry.Factory
	Virtual func(m *registry.Manager) registry.Factory
}

// protocolFactories is the single source of truth for which
// protocols pika ships with. Adding a new protocol = one entry
// here + the matching type constant in internal/service. The
// validator picks up the type via service.IsKnownRegistryType so
// there is no second list to keep in sync.
//
// Order is informational only — RegisterFactory is idempotent on
// (type, kind) and the manager iterates settings in repo order.
var protocolFactories = []protocolFactory{
	{
		Type:    service.RegistryTypeGo,
		Local:   goproxy.NewLocalFactory(),
		Remote:  goproxy.NewRemoteFactory(),
		Virtual: func(m *registry.Manager) registry.Factory { return goproxy.NewVirtualFactory(m) },
	},
	{
		Type:    service.RegistryTypeNPM,
		Local:   npm.NewLocalFactory(),
		Remote:  npm.NewRemoteFactory(),
		Virtual: func(m *registry.Manager) registry.Factory { return npm.NewVirtualFactory(m) },
	},
	{
		Type:    service.RegistryTypeDocker,
		Local:   docker.NewLocalFactory(),
		Remote:  docker.NewRemoteFactory(),
		Virtual: func(m *registry.Manager) registry.Factory { return docker.NewVirtualFactory(m) },
	},
	{
		Type:    service.RegistryTypeHelm,
		Local:   helm.NewLocalFactory(),
		Remote:  helm.NewRemoteFactory(),
		Virtual: func(m *registry.Manager) registry.Factory { return helm.NewVirtualFactory(m) },
	},
	{
		Type:    service.RegistryTypeMaven,
		Local:   maven.NewLocalFactory(),
		Remote:  maven.NewRemoteFactory(),
		Virtual: func(m *registry.Manager) registry.Factory { return maven.NewVirtualFactory(m) },
	},
	{
		Type:    service.RegistryTypePyPI,
		Local:   pypi.NewLocalFactory(),
		Remote:  pypi.NewRemoteFactory(),
		Virtual: func(m *registry.Manager) registry.Factory { return pypi.NewVirtualFactory(m) },
	},
	{
		Type:    service.RegistryTypeCargo,
		Local:   cargo.NewLocalFactory(),
		Remote:  cargo.NewRemoteFactory(),
		Virtual: func(m *registry.Manager) registry.Factory { return cargo.NewVirtualFactory(m) },
	},
}

// registerRegistryFactories is the single boot-time hookup for
// every protocol head supported by the artifact registry feature.
// It runs once per process, before the manager's initial Reload,
// so settings rows for any registered (type, kind) tuple are
// installed at cold start.
//
// Adding a new protocol means:
//
//  1. Implement the Registry interface in internal/registry/{protocol}/.
//  2. Add the protocol's type constant to service.KnownRegistryTypes.
//  3. Append a new protocolFactory entry to protocolFactories above.
//
// No edits to this function are required.
func registerRegistryFactories(m *registry.Manager) error {
	if m == nil {
		return nil
	}
	for _, pf := range protocolFactories {
		if pf.Local != nil {
			if err := m.RegisterFactory(pf.Type, service.RegistryKindLocal, pf.Local); err != nil {
				return fmt.Errorf("register %s/%s: %w", pf.Type, service.RegistryKindLocal, err)
			}
		}
		if pf.Remote != nil {
			if err := m.RegisterFactory(pf.Type, service.RegistryKindRemote, pf.Remote); err != nil {
				return fmt.Errorf("register %s/%s: %w", pf.Type, service.RegistryKindRemote, err)
			}
		}
		if pf.Virtual != nil {
			if err := m.RegisterFactory(pf.Type, service.RegistryKindVirtual, pf.Virtual(m)); err != nil {
				return fmt.Errorf("register %s/%s: %w", pf.Type, service.RegistryKindVirtual, err)
			}
		}
	}
	return nil
}
