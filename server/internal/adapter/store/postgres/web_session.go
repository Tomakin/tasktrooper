package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/makifbaysal/tasktrooper/server/internal/domain"
)

type WebSessionStore struct {
	pool *DB
}

func NewWebSessionStore(pool *DB) *WebSessionStore {
	return &WebSessionStore{pool: pool}
}

func (s *WebSessionStore) Create(ctx context.Context, ws domain.WebSession) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO web_sessions (token_hash, username, created_at, last_seen_at, expires_at)
		VALUES ($1, $2, $3, $4, $5)
	`, ws.TokenHash, ws.Username, ws.CreatedAt, ws.LastSeenAt, ws.ExpiresAt)
	if err != nil {
		return fmt.Errorf("create web session: %w", err)
	}
	return nil
}

func (s *WebSessionStore) Get(ctx context.Context, tokenHash string) (domain.WebSession, error) {
	var ws domain.WebSession
	err := s.pool.QueryRow(ctx, `
		SELECT token_hash, username, created_at, last_seen_at, expires_at
		FROM web_sessions WHERE token_hash = $1
	`, tokenHash).Scan(&ws.TokenHash, &ws.Username, &ws.CreatedAt, &ws.LastSeenAt, &ws.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.WebSession{}, domain.ErrWebSessionNotFound
	}
	if err != nil {
		return domain.WebSession{}, fmt.Errorf("get web session: %w", err)
	}
	return ws, nil
}

func (s *WebSessionStore) Touch(ctx context.Context, tokenHash string, lastSeen, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE web_sessions SET last_seen_at = $2, expires_at = $3 WHERE token_hash = $1
	`, tokenHash, lastSeen, expiresAt)
	if err != nil {
		return fmt.Errorf("touch web session: %w", err)
	}
	return nil
}

func (s *WebSessionStore) Delete(ctx context.Context, tokenHash string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM web_sessions WHERE token_hash = $1`, tokenHash); err != nil {
		return fmt.Errorf("delete web session: %w", err)
	}
	return nil
}

func (s *WebSessionStore) DeleteExpired(ctx context.Context, now time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM web_sessions WHERE expires_at <= $1`, now)
	if err != nil {
		return 0, fmt.Errorf("delete expired web sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}
