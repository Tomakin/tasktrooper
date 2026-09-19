// Package sessionlimit is the machine-wide bound on concurrent agent CLI
// sessions. It replaces a fixed-size channel semaphore because the bound is
// now a setting the user changes while sessions run: a channel's capacity is
// fixed at make(), and raising it meant a restart that killed every session.
package sessionlimit

import (
	"context"
	"sync"

	"github.com/makifbaysal/tasktrooper/server/internal/port"
)

type Limiter struct {
	mu     sync.Mutex
	limit  int
	active int
	// waiters are served first in, first out, so a session queued behind a
	// full machine is not overtaken by one that arrived later.
	waiters []chan struct{}
}

func New(limit int) *Limiter {
	return &Limiter{limit: limit}
}

func (l *Limiter) Acquire(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l.mu.Lock()
	if l.hasRoomLocked() && len(l.waiters) == 0 {
		l.active++
		l.mu.Unlock()
		return l.releaser(), nil
	}
	ch := make(chan struct{})
	l.waiters = append(l.waiters, ch)
	l.mu.Unlock()

	select {
	case <-ch:
		return l.releaser(), nil
	case <-ctx.Done():
		l.mu.Lock()
		for i, w := range l.waiters {
			if w == ch {
				l.waiters = append(l.waiters[:i], l.waiters[i+1:]...)
				l.mu.Unlock()
				return nil, ctx.Err()
			}
		}
		l.mu.Unlock()
		// The slot was granted in the same instant the context ended. It is
		// ours, and nobody will use it, so hand it straight back.
		l.releaser()()
		return nil, ctx.Err()
	}
}

func (l *Limiter) SetLimit(n int) {
	l.mu.Lock()
	l.limit = n
	l.grantLocked()
	l.mu.Unlock()
}

func (l *Limiter) Stats() port.SessionLimitStats {
	l.mu.Lock()
	defer l.mu.Unlock()
	return port.SessionLimitStats{Limit: l.limit, Active: l.active, Waiting: len(l.waiters)}
}

func (l *Limiter) releaser() func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			l.active--
			l.grantLocked()
			l.mu.Unlock()
		})
	}
}

func (l *Limiter) hasRoomLocked() bool {
	return l.limit <= 0 || l.active < l.limit
}

func (l *Limiter) grantLocked() {
	for len(l.waiters) > 0 && l.hasRoomLocked() {
		ch := l.waiters[0]
		l.waiters = l.waiters[1:]
		l.active++
		close(ch)
	}
}

var _ port.SessionLimiter = (*Limiter)(nil)
