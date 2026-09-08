package bw

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/rakunlabs/pika/internal/service"
)

func TestPasskeyChallengeConsume(t *testing.T) {
	ctx := context.Background()
	store, err := New(ctx, &Config{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	challenges := store.PasskeyChallenges()
	if err := challenges.Save(ctx, &service.PasskeyChallenge{ID: "session", Data: []byte("payload")}); err != nil {
		t.Fatal(err)
	}

	// A transaction-scoped consume must not expose data that can be rolled back.
	if err := store.Tx(ctx, func(ctx context.Context, tx service.Storage) error {
		if row, err := tx.PasskeyChallenges().Consume(ctx, "session"); err == nil || row != nil {
			t.Error("transaction-scoped consume must fail without data")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if row, err := challenges.Consume(canceled, "session"); !errors.Is(err, context.Canceled) || row != nil {
		t.Fatalf("canceled consume = %v, %v", row, err)
	}

	var winners atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 64 {
		storage := store.PasskeyChallenges()
		wg.Go(func() {
			<-start
			row, err := storage.Consume(ctx, "session")
			if err != nil {
				if !errors.Is(err, service.ErrNotFound) || row != nil {
					t.Errorf("losing consume = %v, %v", row, err)
				}
				return
			}
			winners.Add(1)
			if row == nil || row.ID != "session" || string(row.Data) != "payload" {
				t.Error("incorrect consumed row")
			}
		})
	}
	close(start)
	wg.Wait()
	if got := winners.Load(); got != 1 {
		t.Fatalf("successful consumes = %d, want 1", got)
	}
	if row, err := challenges.Consume(ctx, "session"); !errors.Is(err, service.ErrNotFound) || row != nil {
		t.Fatalf("replay consume = %v, %v", row, err)
	}
	if _, err := challenges.Get(ctx, "session"); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("consumed row still present: %v", err)
	}
}
