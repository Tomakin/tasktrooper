package webauth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/makifbaysal/tasktrooper/server/internal/domain"
)

type memStore struct {
	mu   sync.Mutex
	rows map[string]domain.WebSession
}

func newMemStore() *memStore { return &memStore{rows: map[string]domain.WebSession{}} }

func (m *memStore) Create(_ context.Context, s domain.WebSession) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[s.TokenHash] = s
	return nil
}

func (m *memStore) Get(_ context.Context, h string) (domain.WebSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.rows[h]
	if !ok {
		return domain.WebSession{}, domain.ErrWebSessionNotFound
	}
	return s, nil
}

func (m *memStore) Touch(_ context.Context, h string, seen, exp time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.rows[h]
	s.LastSeenAt, s.ExpiresAt = seen, exp
	m.rows[h] = s
	return nil
}

func (m *memStore) Delete(_ context.Context, h string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows, h)
	return nil
}

func (m *memStore) DeleteExpired(_ context.Context, now time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for k, s := range m.rows {
		if !now.Before(s.ExpiresAt) {
			delete(m.rows, k)
			n++
		}
	}
	return n, nil
}

func hashFor(t *testing.T, pw string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(pw), MinBcryptCost)
	if err != nil {
		t.Fatal(err)
	}
	return string(h)
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestService(t *testing.T) (*Service, *memStore, *clock) {
	t.Helper()
	users, err := ParseUsers("alice:" + hashFor(t, "correct horse"))
	if err != nil {
		t.Fatal(err)
	}
	store := newMemStore()
	svc, err := NewService(Config{Users: users}, store)
	if err != nil {
		t.Fatal(err)
	}
	c := &clock{t: time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)}
	svc.SetClock(c.now)
	return svc, store, c
}

func TestParseUsers(t *testing.T) {
	good := hashFor(t, "pw")
	users, err := ParseUsers("# comment\nalice:" + good + ", bob:" + good + "\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 || users[0].Name != "alice" || users[1].Name != "bob" {
		t.Fatalf("users = %+v", users)
	}

	weak, _ := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	for name, raw := range map[string]string{
		"no separator": "alice",
		"empty name":   ":" + good,
		"not bcrypt":   "alice:plaintext",
		"weak cost":    "alice:" + string(weak),
		"duplicate":    "alice:" + good + ",alice:" + good,
	} {
		if _, err := ParseUsers(raw); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if users, err := ParseUsers("  \n# only a comment\n"); err != nil || len(users) != 0 {
		t.Errorf("blank input: users=%v err=%v", users, err)
	}
}

func TestLoginAuthenticateLogout(t *testing.T) {
	svc, store, c := newTestService(t)
	ctx := context.Background()

	token, sess, err := svc.Login(ctx, "alice", "correct horse", "203.0.113.1")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Username != "alice" || token == "" {
		t.Fatalf("session = %+v token=%q", sess, token)
	}
	if _, stored := store.rows[token]; stored {
		t.Fatal("the raw token must not be the stored key")
	}

	got, err := svc.Authenticate(ctx, token)
	if err != nil || got.Username != "alice" {
		t.Fatalf("authenticate: %+v %v", got, err)
	}

	c.advance(6 * 24 * time.Hour)
	if _, err := svc.Authenticate(ctx, token); err != nil {
		t.Fatalf("within ttl: %v", err)
	}
	c.advance(6 * 24 * time.Hour)
	if _, err := svc.Authenticate(ctx, token); err != nil {
		t.Fatalf("sliding expiry should have extended the session: %v", err)
	}

	if err := svc.Logout(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("after logout: %v", err)
	}
}

func TestSessionExpiresWhenIdle(t *testing.T) {
	svc, store, c := newTestService(t)
	ctx := context.Background()
	token, _, err := svc.Login(ctx, "alice", "correct horse", "a")
	if err != nil {
		t.Fatal(err)
	}
	c.advance(DefaultSessionTTL + time.Second)
	if _, err := svc.Authenticate(ctx, token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expired session: %v", err)
	}
	if len(store.rows) != 0 {
		t.Fatal("expired session row should be deleted")
	}
}

func TestRemovedUserIsSignedOut(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	token, _, err := svc.Login(ctx, "alice", "correct horse", "a")
	if err != nil {
		t.Fatal(err)
	}
	delete(svc.users, "alice")
	if _, err := svc.Authenticate(ctx, token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("removed user: %v", err)
	}
}

func TestWrongPasswordAndUnknownUser(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	if _, _, err := svc.Login(ctx, "alice", "wrong", "a"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password: %v", err)
	}
	if _, _, err := svc.Login(ctx, "mallory", "correct horse", "a"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unknown user: %v", err)
	}
	if _, _, err := svc.Login(ctx, "alice", strings.Repeat("x", maxPasswordLen+1), "a"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("oversized password: %v", err)
	}
	if _, err := svc.Authenticate(ctx, ""); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("empty token: %v", err)
	}
	if _, err := svc.Authenticate(ctx, "forged"); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("unknown token: %v", err)
	}
}

func TestLockoutAfterFiveFailures(t *testing.T) {
	svc, _, c := newTestService(t)
	ctx := context.Background()

	for i := 1; i <= 4; i++ {
		if _, _, err := svc.Login(ctx, "alice", "wrong", "198.51.100.7"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	_, _, err := svc.Login(ctx, "alice", "wrong", "198.51.100.7")
	var locked *LockedError
	if !errors.As(err, &locked) || locked.RetryAfter != DefaultLockout {
		t.Fatalf("fifth failure should lock: %v", err)
	}

	if _, _, err := svc.Login(ctx, "alice", "correct horse", "198.51.100.7"); !errors.As(err, &locked) {
		t.Fatalf("the right password must be refused while locked: %v", err)
	}
	if _, _, err := svc.Login(ctx, "alice", "correct horse", "192.0.2.9"); err != nil {
		t.Fatalf("another address is not locked: %v", err)
	}

	c.advance(DefaultLockout + time.Second)
	if _, _, err := svc.Login(ctx, "alice", "correct horse", "198.51.100.7"); err != nil {
		t.Fatalf("after the lockout: %v", err)
	}
}

func TestFailuresOutsideTheWindowDoNotAccumulate(t *testing.T) {
	svc, _, c := newTestService(t)
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		_, _, _ = svc.Login(ctx, "alice", "wrong", "a")
	}
	c.advance(DefaultFailureWindow + time.Minute)
	if _, _, err := svc.Login(ctx, "alice", "wrong", "a"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("a failure after the window restarts the count: %v", err)
	}
}

func TestSuccessResetsTheFailureCount(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		_, _, _ = svc.Login(ctx, "alice", "wrong", "a")
	}
	if _, _, err := svc.Login(ctx, "alice", "correct horse", "a"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Login(ctx, "alice", "wrong", "a"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("count should have been reset: %v", err)
	}
}
