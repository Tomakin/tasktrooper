package port

import (
	"context"
	"time"

	"github.com/makifbaysal/tasktrooper/server/internal/domain"
)

type WebSessionStore interface {
	Create(ctx context.Context, s domain.WebSession) error
	// Get returns domain.ErrWebSessionNotFound for an unknown hash; expiry is the
	// caller's decision, not the store's.
	Get(ctx context.Context, tokenHash string) (domain.WebSession, error)
	Touch(ctx context.Context, tokenHash string, lastSeen, expiresAt time.Time) error
	Delete(ctx context.Context, tokenHash string) error
	DeleteExpired(ctx context.Context, now time.Time) (int64, error)
}
