// Package agentconcurrency is the user's control over how many agent CLI
// sessions run at once on this machine. The bound lives in app_settings and is
// applied to the shared limiter immediately, without a restart.
package agentconcurrency

import (
	"context"
	"fmt"

	"github.com/rs/zerolog/log"

	"github.com/makifbaysal/tasktrooper/server/internal/port"
)

const (
	MinSessions = 1
	MaxSessions = 10
)

type Status struct {
	Limit   int `json:"limit"`
	Active  int `json:"active"`
	Waiting int `json:"waiting"`
	Min     int `json:"min"`
	Max     int `json:"max"`
}

type Service struct {
	limiter port.SessionLimiter
	store   port.SessionLimitStore
}

func New(limiter port.SessionLimiter, store port.SessionLimitStore) *Service {
	return &Service{limiter: limiter, store: store}
}

// Load applies the stored bound, or configured when nothing was stored yet.
// The config file's value is only ever the first default: once someone picks a
// number in the UI, that number wins on every restart.
func (s *Service) Load(ctx context.Context, configured int) int {
	n, ok, err := s.store.MaxConcurrentSessions(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("reading the stored agent concurrency failed; using the configured value")
	}
	if !ok || err != nil {
		n = configured
	}
	s.limiter.SetLimit(n)
	return n
}

func (s *Service) Status() Status {
	st := s.limiter.Stats()
	return Status{Limit: st.Limit, Active: st.Active, Waiting: st.Waiting, Min: MinSessions, Max: MaxSessions}
}

func (s *Service) Set(ctx context.Context, n int) (Status, error) {
	if n < MinSessions || n > MaxSessions {
		return Status{}, fmt.Errorf("concurrent agent sessions must be between %d and %d", MinSessions, MaxSessions)
	}
	if err := s.store.SetMaxConcurrentSessions(ctx, n); err != nil {
		return Status{}, err
	}
	s.limiter.SetLimit(n)
	log.Info().Int("limit", n).Msg("agent concurrency changed")
	return s.Status(), nil
}
