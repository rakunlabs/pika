package server

import (
	"testing"

	"github.com/rakunlabs/pika/internal/server/servertls"
)

func TestListenerScheme(t *testing.T) {
	tests := []struct {
		name              string
		processTLSEnabled bool
		policy            servertls.Policy
		want              string
	}{
		{name: "process tls off", processTLSEnabled: false, policy: servertls.Policy{HTTPS: true}, want: "http"},
		{name: "https only", processTLSEnabled: true, policy: servertls.Policy{HTTPS: true}, want: "https"},
		{name: "https and http", processTLSEnabled: true, policy: servertls.Policy{HTTPS: true, PlainHTTP: true}, want: "https+http"},
		{name: "http only", processTLSEnabled: true, policy: servertls.Policy{PlainHTTP: true}, want: "http"},
		{name: "nothing enabled", processTLSEnabled: true, policy: servertls.Policy{}, want: "disabled"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := listenerScheme(tt.processTLSEnabled, tt.policy); got != tt.want {
				t.Fatalf("listenerScheme() = %q, want %q", got, tt.want)
			}
		})
	}
}
