package session

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rakunlabs/pika/internal/service"
)

const defaultCookieName = "pika_session"

// CookieOptions configures the session cookie.
type CookieOptions struct {
	Name     string
	Domain   string
	Path     string
	Secure   bool
	SameSite http.SameSite
}

// Session represents an active user session.
type Session struct {
	ID        string
	UserID    string
	Username  string
	ExpiresAt time.Time
}

// Store manages in-memory sessions.
type Store struct {
	sessions sync.Map // map[sessionID]*Session
	ttl      time.Duration
	cookie   CookieOptions
	stopCh   chan struct{}
}

// NewStore creates a session store with the given session TTL and cookie options.
// It starts a background goroutine to clean up expired sessions.
func NewStore(ttl time.Duration, cookie CookieOptions) *Store {
	if cookie.Name == "" {
		cookie.Name = defaultCookieName
	}
	if cookie.Path == "" {
		cookie.Path = "/"
	}
	if cookie.SameSite == 0 {
		cookie.SameSite = http.SameSiteLaxMode
	}

	s := &Store{
		ttl:    ttl,
		cookie: cookie,
		stopCh: make(chan struct{}),
	}

	go s.cleanup()

	return s
}

// Stop stops the background cleanup goroutine.
func (s *Store) Stop() {
	close(s.stopCh)
}

// Create creates a new session for the given user and returns the session cookie.
func (s *Store) Create(userID, username string) (*http.Cookie, error) {
	id, err := generateSessionID()
	if err != nil {
		return nil, err
	}

	session := &Session{
		ID:        id,
		UserID:    userID,
		Username:  username,
		ExpiresAt: time.Now().Add(s.ttl),
	}

	s.sessions.Store(id, session)

	return s.newCookie(id, int(s.ttl.Seconds())), nil
}

// Get retrieves a session by its ID.
// Returns nil if the session doesn't exist or has expired.
func (s *Store) Get(sessionID string) *Session {
	val, ok := s.sessions.Load(sessionID)
	if !ok {
		return nil
	}

	session := val.(*Session)
	if time.Now().After(session.ExpiresAt) {
		s.sessions.Delete(sessionID)
		return nil
	}

	return session
}

// Delete removes a session.
func (s *Store) Delete(sessionID string) {
	s.sessions.Delete(sessionID)
}

// ClearCookie returns a cookie that clears the session cookie in the browser.
func (s *Store) ClearCookie() *http.Cookie {
	return s.newCookie("", -1)
}

// Middleware checks for a valid session cookie and injects the user into the context.
// Returns 401 Unauthorized if the session is invalid or missing.
func (s *Store) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(s.cookie.Name)
		if err != nil || cookie.Value == "" {
			http.Error(w, `{"message":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		session := s.Get(cookie.Value)
		if session == nil {
			http.Error(w, `{"message":"session expired"}`, http.StatusUnauthorized)
			return
		}

		// Inject user into context
		ctx := service.WithUser(r.Context(), session.Username)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// CookieName returns the configured cookie name.
func (s *Store) CookieName() string {
	return s.cookie.Name
}

// newCookie creates an http.Cookie with the store's cookie options.
func (s *Store) newCookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     s.cookie.Name,
		Value:    value,
		Path:     s.cookie.Path,
		Domain:   s.cookie.Domain,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   s.cookie.Secure,
		SameSite: s.cookie.SameSite,
	}
}

// cleanup periodically removes expired sessions.
func (s *Store) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			now := time.Now()
			s.sessions.Range(func(key, value any) bool {
				session := value.(*Session)
				if now.After(session.ExpiresAt) {
					s.sessions.Delete(key)
				}
				return true
			})
		}
	}
}

func generateSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ParseSameSite converts a string to http.SameSite.
func ParseSameSite(s string) http.SameSite {
	switch strings.ToLower(s) {
	case "strict":
		return http.SameSiteStrictMode
	case "none":
		return http.SameSiteNoneMode
	case "lax":
		return http.SameSiteLaxMode
	default:
		return http.SameSiteLaxMode
	}
}
