package ftpserve

import (
	"crypto/subtle"
	"sync"
)

// User represents an FTP user with optional share restrictions.
type User struct {
	Username       string
	Password       string
	Shares         []string // allowed share names; empty = all
	AuthorizedKeys string   // SSH public keys in OpenSSH authorized_keys format (one per line)
	ReadOnly       bool
}

// MultiUserAuth manages FTP users with thread-safe access.
type MultiUserAuth struct {
	mu    sync.RWMutex
	users []User
}

// NewMultiUserAuth creates a new multi-user authenticator.
func NewMultiUserAuth(users []User) *MultiUserAuth {
	return &MultiUserAuth{users: users}
}

// UpdateUsers replaces the user list.
func (a *MultiUserAuth) UpdateUsers(users []User) {
	a.mu.Lock()
	a.users = users
	a.mu.Unlock()
}

// Authenticate checks credentials and returns the matching user, or nil.
func (a *MultiUserAuth) Authenticate(username, password string) *User {
	a.mu.RLock()
	defer a.mu.RUnlock()

	for i := range a.users {
		if constantTimeEquals(a.users[i].Username, username) && constantTimeEquals(a.users[i].Password, password) {
			u := a.users[i]
			return &u
		}
	}
	return nil
}

// GetUser returns the user by username.
func (a *MultiUserAuth) GetUser(username string) *User {
	a.mu.RLock()
	defer a.mu.RUnlock()

	for i := range a.users {
		if a.users[i].Username == username {
			u := a.users[i]
			return &u
		}
	}
	return nil
}

func constantTimeEquals(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
