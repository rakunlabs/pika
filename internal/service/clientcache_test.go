package service

import (
	"testing"

	"github.com/rakunlabs/pika/internal/external"
)

func TestClientCacheGetAndPurge(t *testing.T) {
	c := newClientCache[int]()
	builds, releases := 0, 0
	create := func() (int, func(), error) {
		builds++
		return builds, func() { releases++ }, nil
	}

	v1, _ := c.get("a", create)
	v2, _ := c.get("a", create)
	if v1 != v2 || builds != 1 {
		t.Fatalf("expected cached value, got %d/%d builds=%d", v1, v2, builds)
	}

	c.purge()
	if releases != 1 {
		t.Fatalf("releases = %d, want 1", releases)
	}
	if v3, _ := c.get("a", create); v3 == v1 {
		t.Fatalf("expected rebuild after purge")
	}
}

func TestExternalClientCacheKeysIncludeCredentials(t *testing.T) {
	s := New(nil)

	a := s.getAzureClient(&external.Azure{VaultURL: "https://kv", ClientSecret: "one"})
	b := s.getAzureClient(&external.Azure{VaultURL: "https://kv", ClientSecret: "two"})
	if a == b {
		t.Fatal("azure clients with different secrets must not be shared")
	}
	if a != s.getAzureClient(&external.Azure{VaultURL: "https://kv", ClientSecret: "one"}) {
		t.Fatal("azure client with identical config should be reused")
	}

	v1 := s.getVaultClient(t.Context(), &external.Vault{Address: "https://vault", Token: "t1"})
	v2 := s.getVaultClient(t.Context(), &external.Vault{Address: "https://vault", Token: "t2"})
	if v1 == v2 {
		t.Fatal("vault clients with different tokens must not be shared")
	}
	if v1 != s.getVaultClient(t.Context(), &external.Vault{Address: "https://vault", Token: "t1", Mount: "other"}) {
		t.Fatal("vault client should be shared across mounts with the same credentials")
	}

	s.purgeExternalClients()
	if v1 == s.getVaultClient(t.Context(), &external.Vault{Address: "https://vault", Token: "t1"}) {
		t.Fatal("purge should drop cached vault clients")
	}
}
