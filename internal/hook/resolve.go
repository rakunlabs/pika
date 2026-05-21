package hook

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/rakunlabs/pika/internal/rawfs"
	"github.com/rakunlabs/pika/internal/secretref"
)

// Resolver resolves PEM references like "raw://mount/path" and "config://file/path"
// into their actual content. This allows hooks to reference certificates stored
// in Pika's own raw mounts or config store.
type Resolver struct {
	// rawMounts provides access to raw filesystem mounts.
	// Key is mount prefix, value is the RawFS backend.
	rawMounts func() map[string]rawfs.RawFS
	// configData reads config file data by key.
	configData func(ctx context.Context, key string) ([]byte, error)
}

// NewResolver creates a new reference resolver.
func NewResolver(
	rawMounts func() map[string]rawfs.RawFS,
	configData func(ctx context.Context, key string) ([]byte, error),
) *Resolver {
	return &Resolver{
		rawMounts:  rawMounts,
		configData: configData,
	}
}

// Resolve resolves a PEM value. If it starts with "raw://" or "config://",
// the content is fetched from the corresponding source. References may select
// nested scalar values with "#" selectors, for example
// "config://tls/secrets#/client/cert" or "raw://m/secrets.yaml#/client/cert".
// Otherwise the value is returned as-is (treated as inline PEM text).
func (r *Resolver) Resolve(ctx context.Context, value string) (string, error) {
	if r == nil || value == "" {
		return value, nil
	}

	switch {
	case strings.HasPrefix(value, "raw://"):
		return r.resolveRaw(value[len("raw://"):])
	case strings.HasPrefix(value, "config://"):
		return r.resolveConfig(ctx, value[len("config://"):])
	default:
		return value, nil
	}
}

// resolveRaw reads content from a raw mount. Path format: "mount/path/to/file".
func (r *Resolver) resolveRaw(path string) (string, error) {
	if r.rawMounts == nil {
		return "", fmt.Errorf("raw mount resolver not available")
	}
	location, selector, hasSelector, err := secretref.Split(path)
	if err != nil {
		return "", fmt.Errorf("invalid raw reference %q: %w", path, err)
	}

	// Split into mount prefix and file path
	parts := strings.SplitN(location, "/", 2)
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid raw reference %q: expected format mount/path", location)
	}
	mountPrefix, filePath := parts[0], parts[1]

	mounts := r.rawMounts()
	fs, ok := mounts[mountPrefix]
	if !ok {
		return "", fmt.Errorf("raw mount %q not found", mountPrefix)
	}

	reader, _, err := fs.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("reading raw://%s: %w", location, err)
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("reading raw://%s: %w", location, err)
	}

	if hasSelector {
		return secretref.Select(data, selector)
	}
	return string(data), nil
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
