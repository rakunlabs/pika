package netguard

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestPolicyAllowed(t *testing.T) {
	p, err := NewPolicy([]string{"169.254.10.1"}, append([]string{"10.0.0.0/8"}, DefaultDenyCIDRs...))
	if err != nil {
		t.Fatal(err)
	}

	tests := map[string]bool{
		"169.254.169.254":        false,
		"169.254.10.1":           true, // allow wins
		"10.1.2.3":               false,
		"192.168.1.1":            true,
		"127.0.0.1":              true,
		"fe80::1":                false,
		"::ffff:169.254.169.254": false, // v4-mapped
		"8.8.8.8":                true,
	}
	for ip, want := range tests {
		if got := p.Allowed(netip.MustParseAddr(ip)); got != want {
			t.Errorf("Allowed(%s) = %v, want %v", ip, got, want)
		}
	}

	var nilPolicy *Policy
	if !nilPolicy.Allowed(netip.MustParseAddr("169.254.169.254")) {
		t.Error("nil policy must allow everything")
	}
}

func TestNewPolicyInvalid(t *testing.T) {
	if _, err := NewPolicy(nil, []string{"not-a-cidr"}); err == nil {
		t.Fatal("expected error for invalid CIDR")
	}
}

func TestDialerBlocks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	t.Cleanup(srv.Close)

	prev := Current()
	t.Cleanup(func() { SetPolicy(prev) })

	transport := &http.Transport{DialContext: Dialer().DialContext}
	client := &http.Client{Transport: transport}

	SetPolicy(&Policy{Deny: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}})
	_, err := client.Get(srv.URL)
	var blocked *ErrBlocked
	if !errors.As(err, &blocked) {
		t.Fatalf("expected ErrBlocked, got %v", err)
	}

	SetPolicy(nil)
	transport.CloseIdleConnections()
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("expected success with nil policy, got %v", err)
	}
	_ = resp.Body.Close()
}
