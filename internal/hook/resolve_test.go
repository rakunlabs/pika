package hook

import (
	"context"
	"testing"
)

func TestResolver_ConfigSelector(t *testing.T) {
	r := NewResolver(func(_ context.Context, key string) ([]byte, error) {
		if key != "tls/secrets" {
			t.Fatalf("config key = %q, want tls/secrets", key)
		}
		return []byte(`{"client":{"cert":"CERT"}}`), nil
	})

	got, err := r.Resolve(context.Background(), "config://tls/secrets#/client/cert")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "CERT" {
		t.Fatalf("Resolve = %q, want CERT", got)
	}
}
