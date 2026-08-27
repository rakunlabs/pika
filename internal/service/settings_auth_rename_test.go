package service_test

import (
	"testing"

	"github.com/rakunlabs/bw/codec"

	"github.com/rakunlabs/pika/internal/service"
)

// legacyAuthCookie and legacyAuthIssuer are the field shapes that existed
// before the opt-in flags were replaced by their opt-out counterparts.
// Settings rows written by an older pika still carry these keys.
type legacyAuthCookie struct {
	Name     string
	Domain   string
	Path     string
	Secure   bool
	HttpOnly bool
	SameSite string
}

type legacyAuthIssuer struct {
	AccessTTL     int64
	RefreshTTL    int64
	RotateRefresh bool
}

// TestLegacyCookieSettingsDecodeToSecureDefaults is the upgrade guard for
// the flag inversion.
//
// `http_only` and `rotate_refresh` were opt-in flags that defaulted to the
// weaker behaviour: an install that never opened the settings page shipped
// a script-readable session cookie and no refresh rotation. Their
// replacements are opt-outs, so the safe behaviour is the zero value.
//
// The rename means a persisted row's old key is simply unknown to the new
// struct. What matters is where that lands: msgpack must skip it and leave
// the new field false — HttpOnly on, rotation on — for rows written with
// the old flag either way. An upgrade that silently disabled either
// protection would be a security regression delivered by a refactor.
func TestLegacyCookieSettingsDecodeToSecureDefaults(t *testing.T) {
	tests := []struct {
		name     string
		httpOnly bool
		rotate   bool
	}{
		{name: "operator had opted in", httpOnly: true, rotate: true},
		{name: "operator never touched it", httpOnly: false, rotate: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cookieBlob, err := encodeLegacy(legacyAuthCookie{
				Name:     "pika_session",
				Path:     "/",
				Secure:   true,
				HttpOnly: tt.httpOnly,
				SameSite: "Lax",
			})
			if err != nil {
				t.Fatalf("encode cookie: %v", err)
			}

			var cookie service.AuthCookie
			if err := decodeLegacy(cookieBlob, &cookie); err != nil {
				t.Fatalf("decode cookie: %v", err)
			}

			if cookie.DisableHTTPOnly {
				t.Error("upgrade must not disable HttpOnly")
			}
			// Fields that kept their name must survive untouched.
			if cookie.Name != "pika_session" || cookie.Path != "/" || !cookie.Secure || cookie.SameSite != "Lax" {
				t.Errorf("unrelated cookie settings were lost: %+v", cookie)
			}

			issuerBlob, err := encodeLegacy(legacyAuthIssuer{
				AccessTTL:     900,
				RefreshTTL:    604800,
				RotateRefresh: tt.rotate,
			})
			if err != nil {
				t.Fatalf("encode issuer: %v", err)
			}

			var issuer service.AuthIssuer
			if err := decodeLegacy(issuerBlob, &issuer); err != nil {
				t.Fatalf("decode issuer: %v", err)
			}

			if issuer.DisableRefreshRotation {
				t.Error("upgrade must not disable refresh rotation")
			}
			if issuer.AccessTTL != 900 || issuer.RefreshTTL != 604800 {
				t.Errorf("unrelated issuer settings were lost: %+v", issuer)
			}
		})
	}
}

// encodeLegacy / decodeLegacy use the exact codec bw persists settings
// with, so the test exercises the real decode path rather than an
// approximation of it.
func encodeLegacy(v any) ([]byte, error) {
	return codec.MsgPack().Marshal(v)
}

func decodeLegacy(b []byte, v any) error {
	return codec.MsgPack().Unmarshal(b, v)
}
