// Package webauth signs a person in from a browser when the server is reached
// over the web rather than from the desktop shell. The desktop, MCP and
// per-run bearer paths are untouched by it; it only adds a second way to be
// authenticated.
package webauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"golang.org/x/crypto/bcrypt"

	"github.com/makifbaysal/tasktrooper/server/internal/domain"
	"github.com/makifbaysal/tasktrooper/server/internal/port"
)

const (
	DefaultSessionTTL    = 7 * 24 * time.Hour
	DefaultMaxFailures   = 5
	DefaultFailureWindow = 15 * time.Minute
	DefaultLockout       = 15 * time.Minute

	// touchInterval bounds how often a sliding session writes its new expiry:
	// the UI polls several endpoints a second and each would otherwise be a
	// write.
	touchInterval = time.Minute

	// bcrypt ignores everything past 72 bytes; x/crypto refuses such input
	// outright. Anything much longer is not a password but a request body
	// meant to burn CPU.
	maxPasswordLen = 1024

	// failureTableCap bounds the in-memory lockout table against a caller
	// cycling usernames and addresses to grow it.
	failureTableCap = 10000
)

var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrUnauthenticated    = errors.New("no valid web session")
)

// LockedError is returned while a username/address pair is locked out. The
// correct password is refused too: otherwise the lock only slows a guesser
// down until the right guess, which is when it matters.
type LockedError struct {
	RetryAfter time.Duration
}

func (e *LockedError) Error() string {
	return fmt.Sprintf("too many failed sign-in attempts; retry in %s", e.RetryAfter.Round(time.Second))
}

type Config struct {
	Users         []User
	SessionTTL    time.Duration
	MaxFailures   int
	FailureWindow time.Duration
	Lockout       time.Duration
}

type failures struct {
	count       int
	first       time.Time
	lockedUntil time.Time
}

type Service struct {
	users map[string][]byte
	store port.WebSessionStore
	cfg   Config
	now   func() time.Time
	rand  io.Reader
	// dummyHash is compared against when the username is unknown, so the
	// response time does not tell a guesser which names exist.
	dummyHash []byte

	mu       sync.Mutex
	failures map[string]*failures
}

func NewService(cfg Config, store port.WebSessionStore) (*Service, error) {
	if len(cfg.Users) == 0 {
		return nil, errors.New("web auth needs at least one user")
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = DefaultSessionTTL
	}
	if cfg.MaxFailures <= 0 {
		cfg.MaxFailures = DefaultMaxFailures
	}
	if cfg.FailureWindow <= 0 {
		cfg.FailureWindow = DefaultFailureWindow
	}
	if cfg.Lockout <= 0 {
		cfg.Lockout = DefaultLockout
	}
	users := make(map[string][]byte, len(cfg.Users))
	cost := MinBcryptCost
	for _, u := range cfg.Users {
		users[u.Name] = u.Hash
		if c, err := bcrypt.Cost(u.Hash); err == nil && c > cost {
			cost = c
		}
	}
	dummy, err := bcrypt.GenerateFromPassword([]byte("tasktrooper-web-auth-dummy"), cost)
	if err != nil {
		return nil, fmt.Errorf("web auth: %w", err)
	}
	return &Service{
		users:     users,
		store:     store,
		cfg:       cfg,
		now:       time.Now,
		rand:      rand.Reader,
		dummyHash: dummy,
		failures:  map[string]*failures{},
	}, nil
}

func (s *Service) SetClock(now func() time.Time) { s.now = now }

// HashToken is how a cookie value is looked up; the value itself is never
// stored.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Login checks a password and opens a session. clientAddr keys the lockout
// together with the username, so a guesser locks out only its own address.
func (s *Service) Login(ctx context.Context, username, password, clientAddr string) (string, domain.WebSession, error) {
	if len(username) > maxUsernameLen || len(password) > maxPasswordLen {
		return "", domain.WebSession{}, ErrInvalidCredentials
	}
	key := username + "\x00" + clientAddr
	if retry := s.lockedFor(key); retry > 0 {
		return "", domain.WebSession{}, &LockedError{RetryAfter: retry}
	}

	hash, known := s.users[username]
	if !known {
		hash = s.dummyHash
	}
	ok := bcrypt.CompareHashAndPassword(hash, []byte(password)) == nil && known
	if !ok {
		if retry := s.recordFailure(key); retry > 0 {
			log.Warn().Str("user", username).Str("addr", clientAddr).Msg("web sign-in locked after repeated failures")
			return "", domain.WebSession{}, &LockedError{RetryAfter: retry}
		}
		return "", domain.WebSession{}, ErrInvalidCredentials
	}
	s.clearFailures(key)

	raw := make([]byte, 32)
	if _, err := io.ReadFull(s.rand, raw); err != nil {
		return "", domain.WebSession{}, fmt.Errorf("web auth: session token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	now := s.now()
	sess := domain.WebSession{
		TokenHash:  HashToken(token),
		Username:   username,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(s.cfg.SessionTTL),
	}
	if err := s.store.Create(ctx, sess); err != nil {
		return "", domain.WebSession{}, err
	}
	if n, err := s.store.DeleteExpired(ctx, now); err != nil {
		log.Warn().Err(err).Msg("web auth: pruning expired sessions failed")
	} else if n > 0 {
		log.Debug().Int64("count", n).Msg("web auth: pruned expired sessions")
	}
	return token, sess, nil
}

// Authenticate resolves a cookie value to its session and slides its expiry.
// A session whose user has since been removed from the configuration is
// refused, so deleting a line from the users list signs that person out.
func (s *Service) Authenticate(ctx context.Context, token string) (domain.WebSession, error) {
	if token == "" {
		return domain.WebSession{}, ErrUnauthenticated
	}
	hash := HashToken(token)
	sess, err := s.store.Get(ctx, hash)
	if errors.Is(err, domain.ErrWebSessionNotFound) {
		return domain.WebSession{}, ErrUnauthenticated
	}
	if err != nil {
		return domain.WebSession{}, err
	}
	now := s.now()
	if !now.Before(sess.ExpiresAt) {
		_ = s.store.Delete(ctx, hash)
		return domain.WebSession{}, ErrUnauthenticated
	}
	if _, known := s.users[sess.Username]; !known {
		_ = s.store.Delete(ctx, hash)
		return domain.WebSession{}, ErrUnauthenticated
	}
	if now.Sub(sess.LastSeenAt) >= touchInterval {
		sess.LastSeenAt = now
		sess.ExpiresAt = now.Add(s.cfg.SessionTTL)
		if err := s.store.Touch(ctx, hash, sess.LastSeenAt, sess.ExpiresAt); err != nil {
			log.Warn().Err(err).Msg("web auth: sliding session expiry failed")
		}
	}
	return sess, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.store.Delete(ctx, HashToken(token))
}

func (s *Service) SessionTTL() time.Duration { return s.cfg.SessionTTL }

func (s *Service) lockedFor(key string) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.failures[key]
	if f == nil {
		return 0
	}
	if remaining := f.lockedUntil.Sub(s.now()); remaining > 0 {
		return remaining
	}
	return 0
}

// recordFailure counts one failed attempt and returns the lockout it
// triggered, if any.
func (s *Service) recordFailure(key string) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	f := s.failures[key]
	if f == nil {
		if len(s.failures) >= failureTableCap {
			s.pruneLocked(now)
		}
		f = &failures{}
		s.failures[key] = f
	}
	if f.count == 0 || now.Sub(f.first) > s.cfg.FailureWindow {
		f.count = 0
		f.first = now
	}
	f.count++
	if f.count >= s.cfg.MaxFailures {
		f.count = 0
		f.lockedUntil = now.Add(s.cfg.Lockout)
		return s.cfg.Lockout
	}
	return 0
}

func (s *Service) clearFailures(key string) {
	s.mu.Lock()
	delete(s.failures, key)
	s.mu.Unlock()
}

func (s *Service) pruneLocked(now time.Time) {
	for k, f := range s.failures {
		if now.After(f.lockedUntil) && now.Sub(f.first) > s.cfg.FailureWindow {
			delete(s.failures, k)
		}
	}
}
