package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/makifbaysal/tasktrooper/server/internal/adapter/store/postgres"
)

func TestSettingsStoreSessionLimit(t *testing.T) {
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
	store := postgres.NewSettingsStore(postgres.NewDB(pool), "/tmp/ws", "en", true)

	if _, ok, err := store.MaxConcurrentSessions(ctx); err != nil || ok {
		t.Fatalf("nothing stored yet: ok=%v err=%v", ok, err)
	}
	if err := store.SetMaxConcurrentSessions(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if err := store.SetMaxConcurrentSessions(ctx, 4); err != nil {
		t.Fatal(err)
	}
	if n, ok, err := store.MaxConcurrentSessions(ctx); err != nil || !ok || n != 4 {
		t.Fatalf("n=%d ok=%v err=%v", n, ok, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE app_settings SET value = 'many' WHERE key = 'max_concurrent_sessions'`); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.MaxConcurrentSessions(ctx); err != nil || ok {
		t.Fatalf("a non-number reads as unset: ok=%v err=%v", ok, err)
	}
}
