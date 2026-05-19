package server

import (
	"github.com/rakunlabs/pika/internal/registry"
	"github.com/rakunlabs/pika/internal/registry/docker"
	"github.com/rakunlabs/pika/internal/registry/goproxy"
	"github.com/rakunlabs/pika/internal/registry/npm"
)

// registerRegistryFactories is the single boot-time hookup for
// every protocol head supported by the artifact registry feature.
// It runs once per process, before the manager's initial Reload,
// so settings rows for any registered (type, kind) tuple are
// installed at cold start.
//
// Adding a new protocol means:
//
//   1. Implement the Registry interface in
//      internal/registry/{protocol}/.
//   2. Add a Factory constructor (NewLocalFactory,
//      NewRemoteFactory, NewVirtualFactory by convention).
//   3. Register it here.
//   4. Update internal/service/registry_validate.go's Type allowlist
//      if the protocol introduces a new type string.
//
// The Virtual factory needs a manager handle to resolve member
// repos at request time; the closure captures it directly.
func registerRegistryFactories(m *registry.Manager) error {
	if m == nil {
		return nil
	}
	// Go module proxy
	if err := m.RegisterFactory(goTypeKey, localKindKey, goproxy.NewLocalFactory()); err != nil {
		return err
	}
	if err := m.RegisterFactory(goTypeKey, remoteKindKey, goproxy.NewRemoteFactory()); err != nil {
		return err
	}
	if err := m.RegisterFactory(goTypeKey, virtualKindKey, goproxy.NewVirtualFactory(m)); err != nil {
		return err
	}
	// NPM registry
	if err := m.RegisterFactory(npmTypeKey, localKindKey, npm.NewLocalFactory()); err != nil {
		return err
	}
	if err := m.RegisterFactory(npmTypeKey, remoteKindKey, npm.NewRemoteFactory()); err != nil {
		return err
	}
	if err := m.RegisterFactory(npmTypeKey, virtualKindKey, npm.NewVirtualFactory(m)); err != nil {
		return err
	}
	// Docker / OCI registry — Local, Remote, Virtual all wired.
	if err := m.RegisterFactory(dockerTypeKey, localKindKey, docker.NewLocalFactory()); err != nil {
		return err
	}
	if err := m.RegisterFactory(dockerTypeKey, remoteKindKey, docker.NewRemoteFactory()); err != nil {
		return err
	}
	if err := m.RegisterFactory(dockerTypeKey, virtualKindKey, docker.NewVirtualFactory(m)); err != nil {
		return err
	}
	return nil
}

// String constants for the type / kind keys. Re-declared here
// (instead of imported from internal/service) to keep this file's
// import surface minimal — the values must match
// service.RegistryType{Go,NPM,Docker} and service.RegistryKind{Local,
// Remote,Virtual} exactly, which the validator enforces.
const (
	goTypeKey      = "go"
	npmTypeKey     = "npm"     // reserved for NPM phase
	dockerTypeKey  = "docker"  // reserved for Docker phase
	localKindKey   = "local"
	remoteKindKey  = "remote"
	virtualKindKey = "virtual"
)


