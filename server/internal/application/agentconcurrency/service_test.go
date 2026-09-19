package agentconcurrency

import (
	"context"
	"errors"
	"testing"

	"github.com/makifbaysal/tasktrooper/server/internal/port"
)

type fakeLimiter struct{ stats port.SessionLimitStats }

func (f *fakeLimiter) Acquire(context.Context) (func(), error) {
	f.stats.Active++
	return func() { f.stats.Active-- }, nil
}
func (f *fakeLimiter) SetLimit(n int)                { f.stats.Limit = n }
func (f *fakeLimiter) Stats() port.SessionLimitStats { return f.stats }

type memStore struct {
	n   int
	ok  bool
	err error
}

func (m *memStore) MaxConcurrentSessions(context.Context) (int, bool, error) { return m.n, m.ok, m.err }
func (m *memStore) SetMaxConcurrentSessions(_ context.Context, n int) error {
	m.n, m.ok = n, true
	return nil
}

func TestLoadPrefersTheStoredValue(t *testing.T) {
	l := &fakeLimiter{}
	svc := New(l, &memStore{n: 2, ok: true})
	if got := svc.Load(context.Background(), 3); got != 2 || l.Stats().Limit != 2 {
		t.Fatalf("got %d, limiter %+v", got, l.Stats())
	}
}

func TestLoadFallsBackToTheConfiguredValue(t *testing.T) {
	for _, store := range []*memStore{{}, {err: errors.New("db down")}} {
		l := &fakeLimiter{}
		if got := New(l, store).Load(context.Background(), 3); got != 3 || l.Stats().Limit != 3 {
			t.Fatalf("got %d, limiter %+v", got, l.Stats())
		}
	}
}

func TestSetValidatesPersistsAndApplies(t *testing.T) {
	l := &fakeLimiter{stats: port.SessionLimitStats{Limit: 1}}
	store := &memStore{}
	svc := New(l, store)
	ctx := context.Background()
	for _, bad := range []int{0, -1, 11} {
		if _, err := svc.Set(ctx, bad); err == nil {
			t.Errorf("%d accepted", bad)
		}
	}
	release, _ := l.Acquire(ctx)
	defer release()
	st, err := svc.Set(ctx, 4)
	if err != nil {
		t.Fatal(err)
	}
	if st.Limit != 4 || st.Active != 1 || st.Min != 1 || st.Max != 10 || store.n != 4 {
		t.Fatalf("status %+v store %+v", st, store)
	}
}
