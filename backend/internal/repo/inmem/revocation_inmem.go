package inmem

import (
	"context"
	"sync"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/pkg/clock"
)

// Revocation is the in-memory adapter for the
// internal/usecase/admin.Revocation port. It keeps revoked JTIs in
// process memory, lazily evicting entries whose ExpiresAt has passed.
//
// Suitable for a single-instance backend (PRD §1.4 admin auth scope);
// a Redis-backed implementation can drop in via the same port for
// horizontal scaling without touching usecase code.
type Revocation struct {
	entries sync.Map
	clock   clock.Clock
}

func NewRevocation(c clock.Clock) *Revocation {
	return &Revocation{clock: c}
}

func (s *Revocation) Revoke(_ context.Context, jti string, expiresAt time.Time) error {
	s.entries.Store(jti, expiresAt)
	return nil
}

func (s *Revocation) IsRevoked(_ context.Context, jti string) (bool, error) {
	v, ok := s.entries.Load(jti)
	if !ok {
		return false, nil
	}
	exp, _ := v.(time.Time)
	if s.clock.Now().After(exp) {
		s.entries.Delete(jti)
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
		if now.After(exp) {
			s.entries.Delete(k)
		}
		return true
	})
}
