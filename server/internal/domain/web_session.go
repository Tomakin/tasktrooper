package domain

import (
	"errors"
	"time"
)

// WebSession is a browser sign-in. Only the SHA-256 of the cookie value is
// stored, so a copy of the database does not hand out live sessions.
type WebSession struct {
	TokenHash  string
	Username   string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

var ErrWebSessionNotFound = errors.New("web session not found")
