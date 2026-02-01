package crypto

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// RotationScheduler handles automatic key rotation and background re-encryption.
type RotationScheduler struct {
	keyManager  *KeyManager
	encryptor   Encryptor
	reencryptor ReencryptFunc
	config      RotationConfig

	running      atomic.Bool
	reencrypting atomic.Bool
	stopCh       chan struct{}
	mu           sync.Mutex

	// Progress tracking
	totalItems     int64
	processedItems int64
}

// ReencryptFunc is a function that re-encrypts a value.
// It receives the key and old ciphertext, and should store the re-encrypted value.
type ReencryptFunc func(ctx context.Context, key string, oldCiphertext []byte) error

// NewRotationScheduler creates a new rotation scheduler.
func NewRotationScheduler(km *KeyManager, enc Encryptor, cfg RotationConfig) *RotationScheduler {
	return &RotationScheduler{
		keyManager: km,
		encryptor:  enc,
		config:     cfg,
		stopCh:     make(chan struct{}),
	}
}

// SetReencryptFunc sets the function used for re-encryption.
// This should be called before starting the scheduler.
func (rs *RotationScheduler) SetReencryptFunc(fn ReencryptFunc) {
	rs.reencryptor = fn
}

// Start begins the rotation scheduler.
func (rs *RotationScheduler) Start(ctx context.Context) error {
	if !rs.config.Enabled {
		return nil
	}

	if rs.running.Swap(true) {
		return nil // Already running
	}

	go rs.run(ctx)
	return nil
}

// Stop stops the rotation scheduler.
func (rs *RotationScheduler) Stop() {
	if rs.running.Swap(false) {
		close(rs.stopCh)
	}
}

// run is the main scheduler loop.
func (rs *RotationScheduler) run(ctx context.Context) {
	ticker := time.NewTicker(rs.config.Interval)
	defer ticker.Stop()

	// Check if rotation is needed on start
	rs.checkRotation(ctx)

	for {
		select {
		case <-ctx.Done():
			rs.running.Store(false)
			return
		case <-rs.stopCh:
			return
		case <-ticker.C:
			rs.checkRotation(ctx)
		}
	}
}

// checkRotation checks if key rotation is needed and performs it.
func (rs *RotationScheduler) checkRotation(ctx context.Context) {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	keys, err := rs.keyManager.ListKeys()
	if err != nil {
		slog.Error("failed to list keys for rotation check", "error", err)
		return
	}

	if len(keys) == 0 {
		return
	}

	// Find active key
	var activeKey *KeyInfo
	for i := range keys {
		if keys[i].Status == KeyStatusActive {
			activeKey = &keys[i]
			break
		}
	}

	if activeKey == nil {
		return
	}

	// Check if rotation is needed based on age
	keyAge := time.Since(activeKey.CreatedAt)
	if keyAge >= rs.config.Interval {
		slog.Info("rotating encryption key",
			"current_version", activeKey.Version,
			"key_age", keyAge.String())

		newVersion, err := rs.keyManager.RotateKey(ctx)
		if err != nil {
			slog.Error("failed to rotate key", "error", err)
			return
		}

		slog.Info("key rotation completed", "new_version", newVersion)

		// Start background re-encryption if configured and reencryptor is set
		if rs.config.ReencryptOnStart && rs.reencryptor != nil {
			go rs.backgroundReencrypt(ctx, activeKey.Version)
		}
	}
}

// backgroundReencrypt re-encrypts data in the background without blocking operations.
func (rs *RotationScheduler) backgroundReencrypt(ctx context.Context, oldKeyVersion string) {
	if rs.reencrypting.Swap(true) {
		return // Already re-encrypting
	}
	defer rs.reencrypting.Store(false)

	slog.Info("starting background re-encryption", "old_key_version", oldKeyVersion)

	// Reset progress
	atomic.StoreInt64(&rs.totalItems, 0)
	atomic.StoreInt64(&rs.processedItems, 0)

	// The actual re-encryption is delegated to the ReencryptFunc
	// which has access to the storage layer
	if rs.reencryptor != nil {
		if err := rs.reencryptor(ctx, oldKeyVersion, nil); err != nil {
			slog.Error("background re-encryption failed", "error", err)
			return
		}
	}

	slog.Info("background re-encryption completed",
		"processed", atomic.LoadInt64(&rs.processedItems))
}

// TriggerRotation manually triggers a key rotation.
func (rs *RotationScheduler) TriggerRotation(ctx context.Context) (string, error) {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	currentVersion := rs.keyManager.GetActiveKeyVersion()
	newVersion, err := rs.keyManager.RotateKey(ctx)
	if err != nil {
		return "", err
	}

	// Start background re-encryption if reencryptor is set
	if rs.reencryptor != nil {
		go rs.backgroundReencrypt(ctx, currentVersion)
	}

	return newVersion, nil
}

// IsReencrypting returns whether background re-encryption is in progress.
func (rs *RotationScheduler) IsReencrypting() bool {
	return rs.reencrypting.Load()
}

// ReencryptionProgress returns the current re-encryption progress.
func (rs *RotationScheduler) ReencryptionProgress() (processed, total int64) {
	return atomic.LoadInt64(&rs.processedItems), atomic.LoadInt64(&rs.totalItems)
}

// SetProgress updates the re-encryption progress (called by reencryptor func).
func (rs *RotationScheduler) SetProgress(processed, total int64) {
	atomic.StoreInt64(&rs.processedItems, processed)
	atomic.StoreInt64(&rs.totalItems, total)
}

// ForceReencrypt manually triggers re-encryption with the current key.
func (rs *RotationScheduler) ForceReencrypt(ctx context.Context) error {
	if rs.reencryptor == nil {
		return fmt.Errorf("no reencrypt function configured")
	}

	go rs.backgroundReencrypt(ctx, "")
	return nil
}
