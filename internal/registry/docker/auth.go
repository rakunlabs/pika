package docker

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Bearer challenge + token issuance for the Docker registry.
//
// Two endpoints participate:
//
//   /v2/         — initial probe. We respond 401 + WWW-Authenticate
//                  pointing at /v2/token.
//   /v2/token    — credentials exchange. Client sends Basic auth
//                  (pika token in the password slot); we mint a
//                  signed JWT carrying the requested scope and a
//                  short expiry.
//
// JWT format
//
// We don't depend on a JWT library. The token is HS256-signed JSON
// with the shape:
//
//	header  = {"alg":"HS256","typ":"JWT"}
//	payload = {"sub":"<token-id>","scope":"<scope>","exp":<unix>,"iat":<unix>}
//
// The HMAC key is derived from the pika-wide encryption key (the
// service caller provides the bytes via Deps). For tests we accept
// any non-empty byte slice.
//
// Why HS256 (symmetric) instead of RS256
//
// The token signer and the token verifier are the same process. An
// asymmetric algorithm would just add operational complexity (key
// distribution, rotation) without buying us anything — the JWT
// never leaves pika.

// tokenLifetime is how long an issued bearer token is valid. Docker
// clients refresh on 401 anyway, so a short window keeps the blast
// radius of a leaked token small.
const tokenLifetime = 5 * time.Minute

// challenge writes the 401 + WWW-Authenticate response that primes
// a Docker client to fetch a token from /v2/token. The realm value
// is reconstructed from the inbound request so deployments behind
// reverse proxies see the right URL.
func challenge(w http.ResponseWriter, r *http.Request, message string) {
	realm := tokenRealm(r)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate",
		fmt.Sprintf(`Bearer realm=%q,service="pika"`, realm))
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = fmt.Fprintf(w, `{"errors":[{"code":"UNAUTHORIZED","message":%q}]}`, message)
}

// tokenRealm reconstructs the canonical URL of the /v2/token
// endpoint that pairs with this request's registry prefix. The
// X-Pika-Registry-Prefix header (set by the data-mux entry
// handler) carries the "/registries/{ns}/{repo}" prefix; the
// realm URL appends "/v2/token" to that.
func tokenRealm(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	host := r.Host
	if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
		host = fh
	}
	prefix := r.Header.Get("X-Pika-Registry-Prefix")
	return scheme + "://" + host + prefix + "/v2/token"
}

// tokenClaims is the payload encoded into the bearer JWT. Fields
// match the standard JWT names so any third-party tool inspecting
// our tokens sees a familiar shape.
type tokenClaims struct {
	Subject string `json:"sub"`
	Scope   string `json:"scope"`
	Issued  int64  `json:"iat"`
	Expires int64  `json:"exp"`
}

// TokenSigner is the narrow interface the auth handler needs from
// the surrounding service. Defined here so the docker package
// stays independent of pika's secret store.
type TokenSigner interface {
	// SignerKey returns the HMAC secret used for JWT signing. The
	// pika service feeds this from its encryption key so tokens
	// invalidate when the key rotates.
	SignerKey() []byte
}

// staticSigner is the test/default TokenSigner: holds a fixed key.
// In production the wiring passes a real signer that derives the
// key from the pika encryption store.
type staticSigner struct{ key []byte }

func (s staticSigner) SignerKey() []byte { return s.key }

// NewStaticSigner returns a TokenSigner with a fixed key. Used by
// tests and as a fallback when the registry runtime hasn't been
// wired with a real signer yet.
func NewStaticSigner(key []byte) TokenSigner { return staticSigner{key: key} }

// issueToken mints a bearer JWT for the given subject + scope.
// Caller is responsible for verifying the pika credentials first;
// this function is just the formatter.
func issueToken(signer TokenSigner, subject, scope string) (string, error) {
	if signer == nil || len(signer.SignerKey()) == 0 {
		return "", fmt.Errorf("token signer not configured")
	}
	now := time.Now().Unix()
	claims := tokenClaims{
		Subject: subject,
		Scope:   scope,
		Issued:  now,
		Expires: now + int64(tokenLifetime.Seconds()),
	}
	headerBytes, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	payloadBytes, _ := json.Marshal(claims)
	signingInput := b64url(headerBytes) + "." + b64url(payloadBytes)
	sig := hmacSha256(signer.SignerKey(), signingInput)
	return signingInput + "." + b64url(sig), nil
}

// verifyToken parses and validates a bearer JWT, returning the
// embedded claims. Returns an error when the signature is wrong,
// the format is malformed, or the token has expired.
func verifyToken(signer TokenSigner, token string) (*tokenClaims, error) {
	if signer == nil || len(signer.SignerKey()) == 0 {
		return nil, fmt.Errorf("token signer not configured")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token: not 3 segments")
	}
	signingInput := parts[0] + "." + parts[1]
	wantSig := hmacSha256(signer.SignerKey(), signingInput)
	gotSig, err := b64urlDecode(parts[2])
	if err != nil {
		return nil, fmt.Errorf("invalid token signature encoding: %w", err)
	}
	if !hmac.Equal(wantSig, gotSig) {
		return nil, fmt.Errorf("invalid token signature")
	}
	payload, err := b64urlDecode(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid token payload encoding: %w", err)
	}
	var claims tokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("invalid token payload: %w", err)
	}
	if claims.Expires < time.Now().Unix() {
		return nil, fmt.Errorf("token expired")
	}
	return &claims, nil
}

// hmacSha256 computes HMAC-SHA256(key, msg). Inlined to avoid a
// nontrivial import for a single algorithm.
func hmacSha256(key []byte, msg string) []byte {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(msg))
	return h.Sum(nil)
}

// b64url returns the base64-url (no-padding) encoding of buf.
func b64url(buf []byte) string {
	return base64.RawURLEncoding.EncodeToString(buf)
}

// b64urlDecode is the inverse of b64url.
func b64urlDecode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

// requireBearerToken extracts a Bearer JWT from the request and
// verifies it. Returns the claims on success. On failure writes a
// 401 challenge response and returns nil + an error the caller can
// log.
//
// The "scope" check is the responsibility of the caller — claims
// carry the scope verbatim and handlers compare against what the
// operation needs.
func requireBearerToken(w http.ResponseWriter, r *http.Request, signer TokenSigner) (*tokenClaims, error) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		challenge(w, r, "missing bearer token")
		return nil, fmt.Errorf("no bearer token")
	}
	token := strings.TrimPrefix(auth, "Bearer ")
	claims, err := verifyToken(signer, token)
	if err != nil {
		challenge(w, r, err.Error())
		return nil, err
	}
	return claims, nil
}

// PikaTokenAuthenticator is the narrow surface the Docker auth
// handler uses to verify a pika token (received in Basic-auth
// password). Defined here so the docker package stays decoupled
// from service.Service.
type PikaTokenAuthenticator interface {
	// ValidatePikaToken returns the token's identity/subject when
	// valid; error when not. The scope and op are also enforced
	// against the pika token's recorded scopes.
	ValidatePikaToken(ctx context.Context, raw, scope, op string) (subject string, err error)
}
