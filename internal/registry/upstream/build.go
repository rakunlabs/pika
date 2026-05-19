package upstream

import (
	"fmt"
	"time"

	"github.com/rakunlabs/pika/internal/rawfs"
	"github.com/rakunlabs/pika/internal/registry"
	"github.com/rakunlabs/pika/internal/service"
)

// RemoteBuild bundles the four pieces every protocol's Remote
// factory builds before assembling its concrete struct:
//
//   - FS:         the raw mount handle (for path-keyed storage)
//   - Client:     the *upstream.Client (auth + base URL resolved)
//   - MutableTTL: parsed time.Duration for the mutable cache
//   - BasePath:   the per-repo path prefix (passed through verbatim)
//
// The four NewRemoteFactory implementations across protocols all
// did the exact same five-step recipe to populate these fields,
// with slightly different error message prefixes. Centralising
// removes ~60 lines of drift and gives every protocol the same
// error shape.
type RemoteBuild struct {
	FS         rawfs.RawFS
	Client     *Client
	MutableTTL time.Duration
	BasePath   string
}

// RemoteBuildOptions tweaks BuildRemote's behaviour for protocols
// that need a non-default HTTP timeout. Zero value = defaults.
type RemoteBuildOptions struct {
	// ClientTimeout overrides the upstream HTTP client's per-request
	// timeout. Docker uses a 5-minute timeout because blob fetches
	// can be GB-sized; smaller protocols rely on the default.
	ClientTimeout time.Duration
}

// BuildRemote runs the cross-protocol pre-flight for a Remote
// factory:
//
//  1. Resolve the raw mount handle through deps.MountRawFS.
//  2. Construct the upstream client with auth + base URL + TLS opts.
//  3. Parse MutableTTL with the supplied default when blank or
//     unparseable.
//
// The protocol name (e.g. "goproxy/remote", "docker/remote") is
// folded into every error message so log lines stay searchable.
//
// Callers wrap the returned RemoteBuild's fields into their own
// concrete Remote struct.
func BuildRemote(
	deps registry.Deps,
	protocolLabel string,
	ns string,
	r *service.RegistryRepository,
	defaultTTL time.Duration,
	opts ...RemoteBuildOptions,
) (RemoteBuild, error) {
	var opt RemoteBuildOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	fs, err := deps.MountRawFS(r.Mount)
	if err != nil {
		return RemoteBuild{}, fmt.Errorf("%s %s/%s: mount: %w", protocolLabel, ns, r.Name, err)
	}
	client, err := NewClient(Config{
		BaseURL:            r.URL,
		Auth:               r.Auth,
		Resolver:           deps.Resolver,
		InsecureSkipVerify: r.InsecureSkipVerify,
		Timeout:            opt.ClientTimeout,
	})
	if err != nil {
		return RemoteBuild{}, fmt.Errorf("%s %s/%s: client: %w", protocolLabel, ns, r.Name, err)
	}
	ttl := defaultTTL
	if r.MutableTTL != "" {
		if d, err := time.ParseDuration(r.MutableTTL); err == nil {
			ttl = d
		}
		// Note: silently fall back to defaultTTL for unparseable
		// strings. The validator should reject those at save
		// time; runtime tolerance here is defence-in-depth.
	}
	return RemoteBuild{
		FS:         fs,
		Client:     client,
		MutableTTL: ttl,
		BasePath:   r.BasePath,
	}, nil
}
