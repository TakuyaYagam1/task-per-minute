package inmem_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/repo/inmem"
)

type mutableClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *mutableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mutableClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

func TestRevocation_RoundTrip(t *testing.T) {
	t.Parallel()
	clk := &mutableClock{now: time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)}
	store := inmem.NewRevocation(clk)
	jti := uuid.NewString()

	revoked, err := store.IsRevoked(context.Background(), jti)
	require.NoError(t, err)
	require.False(t, revoked, "unknown jti must not appear revoked")

	require.NoError(t, store.Revoke(context.Background(), jti, clk.Now().Add(time.Hour)))

	revoked, err = store.IsRevoked(context.Background(), jti)
	require.NoError(t, err)
	require.True(t, revoked)
}

func TestRevocation_RevokeExistingLiveJTI_ReturnsErrTokenRevoked(t *testing.T) {
	t.Parallel()
	clk := &mutableClock{now: time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)}
	store := inmem.NewRevocation(clk)
	jti := uuid.NewString()
	expiresAt := clk.Now().Add(time.Hour)

	require.NoError(t, store.Revoke(context.Background(), jti, expiresAt))
	require.ErrorIs(t, store.Revoke(context.Background(), jti, expiresAt), apperr.ErrTokenRevoked)
}

func TestRevocation_ExpiredEntryAutoEvicts(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk := &mutableClock{now: start}
	store := inmem.NewRevocation(clk)
	jti := uuid.NewString()

	require.NoError(t, store.Revoke(context.Background(), jti, start.Add(time.Minute)))

	clk.Set(start.Add(2 * time.Minute))
	revoked, err := store.IsRevoked(context.Background(), jti)
	require.NoError(t, err)
	require.False(t, revoked, "expired entry must read as not-revoked (lazy eviction)")
}

func TestRevocation_Cleanup_EvictsExpired(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	clk := &mutableClock{now: start}
	store := inmem.NewRevocation(clk)

	live := uuid.NewString()
	stale := uuid.NewString()
	require.NoError(t, store.Revoke(context.Background(), live, start.Add(time.Hour)))
	require.NoError(t, store.Revoke(context.Background(), stale, start.Add(time.Minute)))

	clk.Set(start.Add(10 * time.Minute))
	store.Cleanup()

	liveStill, err := store.IsRevoked(context.Background(), live)
	require.NoError(t, err)
	require.True(t, liveStill, "live entry must survive Cleanup")

	staleStill, err := store.IsRevoked(context.Background(), stale)
	require.NoError(t, err)
	require.False(t, staleStill, "stale entry must be evicted by Cleanup")
}

func TestRevocation_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	clk := &mutableClock{now: time.Now().UTC()}
	store := inmem.NewRevocation(clk)
	const workers = 32
	const opsEach = 100

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < opsEach; j++ {
				jti := uuid.NewString()
				_ = store.Revoke(context.Background(), jti, clk.Now().Add(time.Hour))
				_, _ = store.IsRevoked(context.Background(), jti)
			}
		}()
	}
	wg.Wait()
}
