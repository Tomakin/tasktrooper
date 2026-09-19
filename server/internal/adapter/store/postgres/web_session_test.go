package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/makifbaysal/tasktrooper/server/internal/adapter/store/postgres"
	"github.com/makifbaysal/tasktrooper/server/internal/domain"
)

func TestWebSessionStore(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping embedded postgres integration test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pg, err := newTestDatabase(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, pg.DSN())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := postgres.NewWebSessionStore(postgres.NewDB(pool))

	now := time.Now().UTC().Truncate(time.Microsecond)
	live := domain.WebSession{TokenHash: "live", Username: "alice", CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour)}
	stale := domain.WebSession{TokenHash: "stale", Username: "alice", CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(-time.Minute)}
	for _, s := range []domain.WebSession{live, stale} {
		if err := store.Create(ctx, s); err != nil {
			t.Fatal(err)
		}
	}

	got, err := store.Get(ctx, "live")
	if err != nil || got.Username != "alice" || !got.ExpiresAt.Equal(live.ExpiresAt) {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := store.Get(ctx, "missing"); !errors.Is(err, domain.ErrWebSessionNotFound) {
		t.Fatalf("missing: %v", err)
	}

	later := now.Add(10 * time.Minute)
	if err := store.Touch(ctx, "live", later, later.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, _ = store.Get(ctx, "live")
	if !got.LastSeenAt.Equal(later) || !got.ExpiresAt.Equal(later.Add(time.Hour)) {
		t.Fatalf("touch not applied: %+v", got)
	}

	n, err := store.DeleteExpired(ctx, now)
	if err != nil || n != 1 {
		t.Fatalf("delete expired: n=%d err=%v", n, err)
	}
	if _, err := store.Get(ctx, "stale"); !errors.Is(err, domain.ErrWebSessionNotFound) {
		t.Fatal("stale session survived")
	}

	if err := store.Delete(ctx, "live"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, "live"); !errors.Is(err, domain.ErrWebSessionNotFound) {
		t.Fatal("deleted session survived")
	}
}
