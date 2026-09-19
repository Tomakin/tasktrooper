package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

const maxConcurrentSessionsKey = "max_concurrent_sessions"

func (s *SettingsStore) MaxConcurrentSessions(ctx context.Context) (int, bool, error) {
	var raw string
	err := s.pool.QueryRow(ctx, `SELECT value FROM app_settings WHERE key = $1`, maxConcurrentSessionsKey).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("get %s: %w", maxConcurrentSessionsKey, err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		// A hand-edited row that is not a number is treated as never set, so
		// the configured default applies instead of the server refusing to boot.
		return 0, false, nil
	}
	return n, true, nil
}

func (s *SettingsStore) SetMaxConcurrentSessions(ctx context.Context, n int) error {
	return s.setPlain(ctx, maxConcurrentSessionsKey, strconv.Itoa(n))
}
