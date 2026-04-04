package ftpserve

import (
	"crypto/subtle"
	"sync"

	ftpserver "goftp.io/server/v2"
)

// User represents an FTP user with optional share restrictions.
type User struct {
	Username string
	Password string
	Shares   []string // allowed share names; empty = all
	ReadOnly bool
}

// MultiUserAuth implements goftp Auth with multiple users and per-user share access.
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

// CheckPasswd implements goftp Auth.
func (a *MultiUserAuth) CheckPasswd(ctx *ftpserver.Context, username, password string) (bool, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	for _, u := range a.users {
		if constantTimeEquals(u.Username, username) && constantTimeEquals(u.Password, password) {
			return true, nil
		}
	}

	return false, nil
}

// GetUser returns the user by username (after successful auth).
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
