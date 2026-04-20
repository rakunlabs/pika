package service

import "context"

// Capabilities is the frozen capability-key set resolved for a request.
type Capabilities []string

// Has reports whether the set contains the given capability key.
func (c Capabilities) Has(key string) bool {
	for _, k := range c {
		if k == key {
			return true
		}
	}
	return false
}

type capabilitiesCtxKey struct{}

// WithCapabilities attaches a resolved capability set to ctx.
func WithCapabilities(ctx context.Context, keys []string) context.Context {
	return context.WithValue(ctx, capabilitiesCtxKey{}, Capabilities(keys))
}

// CapabilitiesFromContext returns the capability set attached via WithCapabilities.
// Returns an empty slice when nothing is attached.
func CapabilitiesFromContext(ctx context.Context) Capabilities {
	v, _ := ctx.Value(capabilitiesCtxKey{}).(Capabilities)
	return v
}
