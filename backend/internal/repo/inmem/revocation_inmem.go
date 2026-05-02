package inmem

import (
	"context"
	"sync"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/pkg/clock"
)

// Revocation is the in-memory adapter for the
// internal/usecase/admin.Revocation port. It keeps revoked JTIs in
// process memory, lazily evicting entries whose ExpiresAt has passed.
//
// Suitable for a single-instance backend;
// a Redis-backed implementation can drop in via the same port for
// horizontal scaling without touching usecase code.
type Revocation struct {
	entries sync.Map
	clock   clock.Clock
}

func NewRevocation(c clock.Clock) *Revocation {
	if c == nil {
		c = clock.Real{}
	}
	return &Revocation{clock: c}
}

func (s *Revocation) Revoke(_ context.Context, jti string, expiresAt time.Time) error {
	now := s.clock.Now()
	if !expiresAt.After(now) {
		return nil
	}

	for {
		existing, loaded := s.entries.LoadOrStore(jti, expiresAt)
		if !loaded {
			return nil
		}
		if exp, ok := existing.(time.Time); ok && !now.Before(exp) {
			s.entries.CompareAndDelete(jti, existing)
			continue
		}
		return apperr.ErrTokenRevoked
	}
}

func (s *Revocation) IsRevoked(_ context.Context, jti string) (bool, error) {
	v, ok := s.entries.Load(jti)
	if !ok {
		return false, nil
	}
	exp, _ := v.(time.Time)
	if !s.clock.Now().Before(exp) {
		s.entries.CompareAndDelete(jti, v)
		return false, nil
	}
	return true, nil
}

// Cleanup walks the store and evicts every entry whose TTL has elapsed.
// Safe to call from a periodic goroutine; reads/writes are protected by sync.Map.
func (s *Revocation) Cleanup() {
	now := s.clock.Now()
	s.entries.Range(func(k, v any) bool {
		exp, _ := v.(time.Time)
		if !now.Before(exp) {
			s.entries.CompareAndDelete(k, v)
		}
		return true
	})
}
