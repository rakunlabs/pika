package hook

import (
	"context"
	"fmt"
	"strings"

	"github.com/rakunlabs/pika/internal/secretref"
)

// Resolver resolves PEM references like "config://file/path" into their
// actual content. This allows hooks to reference certificates stored in
// Pika's config store.
//
// Note: the previous "raw://" scheme (resolved against raw filesystem
// mounts) was removed when the raw-mount feature was extracted out of
// pika into the kutu repo. Any leftover `raw://...` reference will now
// pass through Resolve unchanged and be treated as inline PEM text.
type Resolver struct {
	// configData reads config file data by key.
	configData func(ctx context.Context, key string) ([]byte, error)
}

// NewResolver creates a new reference resolver.
func NewResolver(
	configData func(ctx context.Context, key string) ([]byte, error),
) *Resolver {
	return &Resolver{
		configData: configData,
	}
}

// Resolve resolves a PEM value. If it starts with "config://", the
// content is fetched from the corresponding source. References may
// select nested scalar values with "#" selectors, for example
// "config://tls/secrets#/client/cert". Otherwise the value is
// returned as-is (treated as inline PEM text).
func (r *Resolver) Resolve(ctx context.Context, value string) (string, error) {
	if r == nil || value == "" {
		return value, nil
	}

	if strings.HasPrefix(value, "config://") {
		return r.resolveConfig(ctx, value[len("config://"):])
	}
	return value, nil
}

// resolveConfig reads content from the config store.
func (r *Resolver) resolveConfig(ctx context.Context, key string) (string, error) {
	if r.configData == nil {
		return "", fmt.Errorf("config resolver not available")
	}
	location, selector, hasSelector, err := secretref.Split(key)
	if err != nil {
		return "", fmt.Errorf("invalid config reference %q: %w", key, err)
	}

	data, err := r.configData(ctx, location)
	if err != nil {
		return "", fmt.Errorf("reading config://%s: %w", location, err)
	}

	if hasSelector {
		return secretref.Select(data, selector)
	}
	return string(data), nil
}
