package hook

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"

	"github.com/redis/go-redis/v9"
)

// redisSink publishes events to a Redis Pub/Sub channel.
// Supports both standalone and cluster modes.
type redisSink struct {
	client  redis.Cmdable
	closer  io.Closer
	channel string
}

// NewRedisSink creates a new Redis Pub/Sub sink.
// If Addresses has multiple entries, a cluster client is created.
// Otherwise, a standalone client is used.
// The resolver is used to resolve raw:// and config:// references in TLS file paths.
func NewRedisSink(ctx context.Context, target *RedisTarget, resolver *Resolver) (Sink, error) {
	if target == nil {
		return nil, fmt.Errorf("redis target is required")
	}
	if target.Channel == "" {
		return nil, fmt.Errorf("redis target channel is required")
	}

	// Build TLS config if enabled
	var tlsConfig *tls.Config
	if target.TLS.Enabled {
		var err error
		tlsConfig, err = buildRedisTLSConfig(ctx, target.TLS, resolver)
		if err != nil {
			return nil, fmt.Errorf("redis: configuring TLS: %w", err)
		}
	}

	var client redis.Cmdable
	var closer io.Closer

	if len(target.Addresses) > 0 {
		// Cluster mode
		cc := redis.NewClusterClient(&redis.ClusterOptions{
			Addrs:     target.Addresses,
			Password:  target.Password,
			TLSConfig: tlsConfig,
		})
		client = cc
		closer = cc
	} else {
		// Standalone mode
		addr := target.Address
		if addr == "" {
			return nil, fmt.Errorf("redis target address is required")
		}
		sc := redis.NewClient(&redis.Options{
			Addr:      addr,
			Password:  target.Password,
			DB:        target.DB,
			TLSConfig: tlsConfig,
		})
		client = sc
		closer = sc
	}

	// Verify connection
	if err := client.Ping(context.Background()).Err(); err != nil {
		closer.Close()
		return nil, fmt.Errorf("redis: connecting: %w", err)
	}

	return &redisSink{
		client:  client,
		closer:  closer,
		channel: target.Channel,
	}, nil
}

// buildRedisTLSConfig creates a *tls.Config from RedisTLS settings.
// File paths support raw:// and config:// references via the resolver.
func buildRedisTLSConfig(ctx context.Context, cfg RedisTLS, resolver *Resolver) (*tls.Config, error) {
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	// Resolve and load client certificate
	certPEM, err := resolvePEM(ctx, "", cfg.CertFile, resolver)
	if err != nil {
		return nil, fmt.Errorf("resolving client cert: %w", err)
	}
	keyPEM, err := resolvePEM(ctx, "", cfg.KeyFile, resolver)
	if err != nil {
		return nil, fmt.Errorf("resolving client key: %w", err)
	}

	if len(certPEM) > 0 && len(keyPEM) > 0 {
		cert, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			return nil, fmt.Errorf("loading client key pair: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	// Resolve and load CA certificate
	caPEM, err := resolvePEM(ctx, "", cfg.CAFile, resolver)
	if err != nil {
		return nil, fmt.Errorf("resolving CA cert: %w", err)
	}
	if len(caPEM) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("invalid CA certificate")
		}
		tlsCfg.RootCAs = pool
	}

	return tlsCfg, nil
}

// Send publishes the payload to the Redis Pub/Sub channel.
func (s *redisSink) Send(ctx context.Context, payload []byte, _ string) error {
	if err := s.client.Publish(ctx, s.channel, payload).Err(); err != nil {
		return fmt.Errorf("redis: publishing to channel %s: %w", s.channel, err)
	}
	return nil
}

// Close releases the Redis connection.
func (s *redisSink) Close() error {
	return s.closer.Close()
}
