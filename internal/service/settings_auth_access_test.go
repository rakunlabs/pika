package service

import "testing"

func TestAccountSecurityAllowed(t *testing.T) {
	tests := []struct {
		name       string
		settings   *AuthSettings
		superadmin bool
		want       bool
	}{
		{name: "missing settings", want: true},
		{name: "unrestricted user", settings: &AuthSettings{}, want: true},
		{name: "restricted user", settings: &AuthSettings{AccountSecurityAdminOnly: true}, want: false},
		{name: "restricted superadmin", settings: &AuthSettings{AccountSecurityAdminOnly: true}, superadmin: true, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.settings.AccountSecurityAllowed(tt.superadmin); got != tt.want {
				t.Fatalf("AccountSecurityAllowed(%v) = %v, want %v", tt.superadmin, got, tt.want)
			}
		})
	}
}
