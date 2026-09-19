package port

import "context"

// SessionLimiter bounds how many agent CLI sessions run at once on this
// machine, across every CLI runtime. The bound can change while sessions run.
type SessionLimiter interface {
	// Acquire blocks until a session may start or ctx ends. release must be
	// called exactly once when the session is over; calling it again is a no-op.
	Acquire(ctx context.Context) (release func(), err error)
	// SetLimit changes the bound; n <= 0 means unlimited. Running sessions are
	// never interrupted by a lower bound.
	SetLimit(n int)
	Stats() SessionLimitStats
}

type SessionLimitStats struct {
	// Limit is <= 0 when unlimited.
	Limit   int `json:"limit"`
	Active  int `json:"active"`
	Waiting int `json:"waiting"`
}

// SessionLimitStore persists the bound the user chose.
type SessionLimitStore interface {
	// MaxConcurrentSessions reports ok=false when nothing was ever stored.
	MaxConcurrentSessions(ctx context.Context) (n int, ok bool, err error)
	SetMaxConcurrentSessions(ctx context.Context, n int) error
}
