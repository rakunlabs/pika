// Package events provides the small Emitter interface that
// concrete registry implementations use to surface semantic
// lifecycle events (publish, delete, GC, cache purge, namespace /
// repository CRUD).
//
// The package contains no transport — it's purely a typed adapter
// between the registry runtime and pika's hook dispatcher
// (internal/hook). Keeping the interface here lets the
// internal/registry package stay free of a direct dependency on
// the hook package, which would create an import cycle through
// the service settings tree.
//
// Wiring: internal/server/api builds a concrete Emitter that
// delegates to the live hook.Dispatcher and hands it to the
// registry manager via Deps.Emitter. Registry impls (goproxy /
// npm / docker) call Emitter.Emit(...) at every mutation point.
// A nil Emitter is supported: implementations call EmitSafe
// instead of Emit directly when they're not sure a dispatcher is
// configured (tests, locked storage, etc.).
package events

import (
	"github.com/rakunlabs/pika/internal/hook"
)

// Emitter is the narrow surface a registry implementation talks to
// when it wants to publish a semantic event. The dispatcher binding
// is supplied by the server wiring layer.
type Emitter interface {
	Emit(event hook.Event)
}

// EmitSafe forwards to the Emitter only when non-nil. Callers that
// might be running in a test without a configured dispatcher use
// this so the publish path is always a single line, not a five-line
// nil-guard.
func EmitSafe(e Emitter, event hook.Event) {
	if e == nil {
		return
	}
	e.Emit(event)
}
