package hook

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"
)

// kafkaClientPool manages shared Kafka clients keyed by their connection
// configuration. Multiple hooks targeting the same cluster reuse one client.
type kafkaClientPool struct {
	mu      sync.Mutex
	clients map[string]*poolEntry
}

type poolEntry struct {
	client   *kgo.Client
	refCount int
}

func newKafkaClientPool() *kafkaClientPool {
	return &kafkaClientPool{
		clients: make(map[string]*poolEntry),
	}
}

// clientKey builds a deterministic cache key from the Kafka target's connection
// config (brokers + security). The topic is NOT part of the key because the
// same client can produce to any topic.
func clientKey(target *KafkaTarget) string {
	// Serialize brokers + security to a canonical JSON string as key.
	type connKey struct {
		Brokers  []string      `json:"b"`
		Security KafkaSecurity `json:"s"`
	}
	brokers := make([]string, len(target.Brokers))
	copy(brokers, target.Brokers)
	sort.Strings(brokers)

	k := connKey{Brokers: brokers, Security: target.Security}
	data, _ := json.Marshal(k)
	return string(data)
}

// getOrCreate returns an existing client for the given config or creates a new one.
func (p *kafkaClientPool) getOrCreate(ctx context.Context, target *KafkaTarget, resolver *Resolver) (*kgo.Client, string, error) {
	key := clientKey(target)

	p.mu.Lock()
	defer p.mu.Unlock()

	if entry, ok := p.clients[key]; ok {
		entry.refCount++
		slog.Debug("reusing shared kafka client", "brokers", target.Brokers, "refs", entry.refCount)
		return entry.client, key, nil
	}

	client, err := newKafkaClient(ctx, target, resolver)
	if err != nil {
		return nil, "", err
	}

	p.clients[key] = &poolEntry{client: client, refCount: 1}
	slog.Info("created shared kafka client", "brokers", target.Brokers)
	return client, key, nil
}

// release decrements the ref count and closes the client when it reaches zero.
func (p *kafkaClientPool) release(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	entry, ok := p.clients[key]
	if !ok {
		return
	}

	entry.refCount--
	if entry.refCount <= 0 {
		entry.client.Close()
		delete(p.clients, key)
		slog.Debug("closed shared kafka client", "key_hash", key[:min(40, len(key))])
	}
}

// closeAll closes all pooled clients.
func (p *kafkaClientPool) closeAll() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for key, entry := range p.clients {
		entry.client.Close()
		delete(p.clients, key)
	}
}

// kafkaSink sends events to a Kafka topic using a shared pooled client.
type kafkaSink struct {
	client   *kgo.Client
	clientID []byte
	topic    string
	pool     *kafkaClientPool
	poolKey  string
}

// newKafkaClient creates a new franz-go client with full TLS/SASL support.
func newKafkaClient(ctx context.Context, target *KafkaTarget, resolver *Resolver) (*kgo.Client, error) {
	clientID := UserAgent()

	opts := []kgo.Opt{
		kgo.SeedBrokers(target.Brokers...),
		kgo.ClientID(clientID),
		kgo.SoftwareNameAndVersion(ServiceName, Version),
	}

	// Auto topic creation — enabled by default (nil = true)
	if target.AutoTopicCreation == nil || *target.AutoTopicCreation {
		opts = append(opts, kgo.AllowAutoTopicCreation())
	}

	// TLS configuration
	if target.Security.TLS.Enabled {
		tlsCfg, err := buildTLSConfig(ctx, target.Security.TLS, resolver)
		if err != nil {
			return nil, fmt.Errorf("kafka TLS config: %w", err)
		}
		opts = append(opts, kgo.DialTLSConfig(tlsCfg))
	}

	// SASL authentication
	if len(target.Security.SASL) > 0 {
		mechanisms, err := buildSASLMechanisms(target.Security.SASL)
		if err != nil {
			return nil, fmt.Errorf("kafka SASL config: %w", err)
		}
		if len(mechanisms) > 0 {
			opts = append(opts, kgo.SASL(mechanisms...))
		}
	}

	client, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("creating kafka client: %w", err)
	}

	return client, nil
}

// NewKafkaSink creates a new Kafka producer sink using a shared client pool.
func NewKafkaSink(ctx context.Context, target *KafkaTarget, pool *kafkaClientPool, resolver *Resolver) (Sink, error) {
	if target == nil || len(target.Brokers) == 0 {
		return nil, fmt.Errorf("kafka brokers are required")
	}
	if target.Topic == "" {
		return nil, fmt.Errorf("kafka topic is required")
	}

	client, poolKey, err := pool.getOrCreate(ctx, target, resolver)
	if err != nil {
		return nil, err
	}

	return &kafkaSink{
		client:   client,
		clientID: []byte(UserAgent()),
		topic:    target.Topic,
		pool:     pool,
		poolKey:  poolKey,
	}, nil
}

// Send produces a message to the configured Kafka topic.
func (s *kafkaSink) Send(ctx context.Context, payload []byte, key string) error {
	record := &kgo.Record{
		Topic: s.topic,
		Value: payload,
		Headers: []kgo.RecordHeader{
			{Key: "service", Value: s.clientID},
		},
	}
	if key != "" {
		record.Key = []byte(key)
	}

	results := s.client.ProduceSync(ctx, record)
	if err := results.FirstErr(); err != nil {
		return fmt.Errorf("producing to kafka topic %s: %w", s.topic, err)
	}

	slog.Debug("kafka event produced", "topic", s.topic, "key", key)
	return nil
}

// Close releases the shared client reference.
func (s *kafkaSink) Close() error {
	if s.pool != nil {
		s.pool.release(s.poolKey)
	}
	return nil
}

// buildTLSConfig creates a *tls.Config from KafkaTLS settings.
// PEM fields take precedence over file paths.
// Values starting with "raw://" or "config://" are resolved via the Resolver.
func buildTLSConfig(ctx context.Context, cfg KafkaTLS, resolver *Resolver) (*tls.Config, error) {
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	// Resolve and load client certificate
	certPEM, err := resolvePEM(ctx, cfg.CertPEM, cfg.CertFile, resolver)
	if err != nil {
		return nil, fmt.Errorf("resolving client cert: %w", err)
	}
	keyPEM, err := resolvePEM(ctx, cfg.KeyPEM, cfg.KeyFile, resolver)
	if err != nil {
		return nil, fmt.Errorf("resolving client key: %w", err)
	}

	if certPEM != nil && keyPEM != nil {
		cert, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			return nil, fmt.Errorf("parsing client certificate: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	// Resolve and load CA certificate
	caPEM, err := resolvePEM(ctx, cfg.CAPEM, cfg.CAFile, resolver)
	if err != nil {
		return nil, fmt.Errorf("resolving CA cert: %w", err)
	}
	if caPEM != nil {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("CA PEM contains no valid certificates")
		}
		tlsCfg.RootCAs = pool
	}

	return tlsCfg, nil
}

// resolvePEM returns the PEM data for a certificate field.
// Priority: pemValue > filePath. Both support raw:// and config:// references.
func resolvePEM(ctx context.Context, pemValue, filePath string, resolver *Resolver) ([]byte, error) {
	// PEM content takes precedence
	if pemValue != "" {
		resolved, err := resolveRefValue(ctx, pemValue, resolver)
		if err != nil {
			return nil, err
		}
		return []byte(resolved), nil
	}

	// Fall back to file path
	if filePath != "" {
		// File path can also be a reference
		if strings.HasPrefix(filePath, "raw://") || strings.HasPrefix(filePath, "config://") {
			resolved, err := resolveRefValue(ctx, filePath, resolver)
			if err != nil {
				return nil, err
			}
			return []byte(resolved), nil
		}

		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("reading file %s: %w", filePath, err)
		}
		return data, nil
	}

	return nil, nil
}

// resolveRefValue resolves a value that may be a raw:// or config:// reference.
func resolveRefValue(ctx context.Context, value string, resolver *Resolver) (string, error) {
	if resolver != nil {
		return resolver.Resolve(ctx, value)
	}
	// No resolver available - if it's a reference, that's an error
	if strings.HasPrefix(value, "raw://") || strings.HasPrefix(value, "config://") {
		return "", fmt.Errorf("reference %q cannot be resolved (no resolver configured)", value)
	}
	return value, nil
}

// buildSASLMechanisms creates SASL mechanisms from config, following the
// same model as github.com/worldline-go/wkafka.
func buildSASLMechanisms(configs []KafkaSASL) ([]sasl.Mechanism, error) {
	var mechanisms []sasl.Mechanism

	for _, c := range configs {
		if c.Plain != nil && c.Plain.Enabled {
			auth := plain.Auth{
				User: c.Plain.User,
				Pass: c.Plain.Pass,
			}
			mechanisms = append(mechanisms, auth.AsMechanism())
		}

		if c.SCRAM != nil && c.SCRAM.Enabled {
			auth := scram.Auth{
				User:    c.SCRAM.User,
				Pass:    c.SCRAM.Pass,
				IsToken: c.SCRAM.IsToken,
			}

			switch c.SCRAM.Algorithm {
			case "SCRAM-SHA-256":
				mechanisms = append(mechanisms, auth.AsSha256Mechanism())
			case "SCRAM-SHA-512":
				mechanisms = append(mechanisms, auth.AsSha512Mechanism())
			default:
				return nil, fmt.Errorf("invalid SCRAM algorithm %q (must be SCRAM-SHA-256 or SCRAM-SHA-512)", c.SCRAM.Algorithm)
			}
		}
	}

	return mechanisms, nil
}
