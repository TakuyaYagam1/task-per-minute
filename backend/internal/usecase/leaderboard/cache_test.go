package leaderboard_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	leaderboardusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/leaderboard"
	leaderboardmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/leaderboard/mocks"
	"github.com/stretchr/testify/require"
)

func TestCacheTop50CachesAndCopiesEntries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC)
	want := []leaderboardusecase.Entry{{Rank: 1, Username: "alice", Wins: 4}}

	reader := leaderboardmocks.NewMockReader(t)
	reader.EXPECT().Top50(ctx).Return(want, nil).Once()
	clock := leaderboardmocks.NewMockClock(t)
	clock.EXPECT().Now().Return(now)

	cache := leaderboardusecase.NewCache(reader, clock)
	first, err := cache.Top50(ctx)
	require.NoError(t, err)
	require.Equal(t, want, first)

	first[0].Username = "changed"
	second, err := cache.Top50(ctx)
	require.NoError(t, err)
	require.Equal(t, want, second)
}

func TestCacheInvalidateForcesRefresh(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC)
	first := []leaderboardusecase.Entry{{Rank: 1, Username: "alice"}}
	second := []leaderboardusecase.Entry{{Rank: 1, Username: "bob"}}

	reader := leaderboardmocks.NewMockReader(t)
	reader.EXPECT().Top50(ctx).Return(first, nil).Once()
	reader.EXPECT().Top50(ctx).Return(second, nil).Once()
	clock := leaderboardmocks.NewMockClock(t)
	clock.EXPECT().Now().Return(now)

	cache := leaderboardusecase.NewCache(reader, clock)
	entries, err := cache.Top50(ctx)
	require.NoError(t, err)
	require.Equal(t, first, entries)

	cache.Invalidate()
	entries, err = cache.Top50(ctx)
	require.NoError(t, err)
	require.Equal(t, second, entries)
}

func TestCacheReloadsExpiredSnapshot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	current := time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC)
	first := []leaderboardusecase.Entry{{Rank: 1, Username: "alice"}}
	second := []leaderboardusecase.Entry{{Rank: 1, Username: "bob"}}

	reader := leaderboardmocks.NewMockReader(t)
	reader.EXPECT().Top50(ctx).Return(first, nil).Once()
	reader.EXPECT().Top50(ctx).Return(second, nil).Once()
	clock := leaderboardmocks.NewMockClock(t)
	clock.EXPECT().Now().RunAndReturn(func() time.Time { return current })

	cache := leaderboardusecase.NewCache(reader, clock)
	entries, err := cache.Top50(ctx)
	require.NoError(t, err)
	require.Equal(t, first, entries)

	current = current.Add(10 * time.Second)
	entries, err = cache.Top50(ctx)
	require.NoError(t, err)
	require.Equal(t, second, entries)
}

func TestCacheDoesNotStoreLoadError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC)
	loadErr := errors.New("load leaderboard")
	want := []leaderboardusecase.Entry{{Rank: 1, Username: "alice"}}

	reader := leaderboardmocks.NewMockReader(t)
	reader.EXPECT().Top50(ctx).Return(nil, loadErr).Once()
	reader.EXPECT().Top50(ctx).Return(want, nil).Once()
	clock := leaderboardmocks.NewMockClock(t)
	clock.EXPECT().Now().Return(now)

	cache := leaderboardusecase.NewCache(reader, clock)
	_, err := cache.Top50(ctx)
	require.ErrorIs(t, err, loadErr)

	entries, err := cache.Top50(ctx)
	require.NoError(t, err)
	require.Equal(t, want, entries)
}

func TestCacheCollapsesConcurrentRefreshes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC)
	want := []leaderboardusecase.Entry{{Rank: 1, Username: "alice"}}
	started := make(chan struct{})
	release := make(chan struct{})

	reader := leaderboardmocks.NewMockReader(t)
	reader.EXPECT().Top50(ctx).RunAndReturn(func(context.Context) ([]leaderboardusecase.Entry, error) {
		close(started)
		<-release
		return want, nil
	}).Once()
	clock := leaderboardmocks.NewMockClock(t)
	clock.EXPECT().Now().Return(now)

	cache := leaderboardusecase.NewCache(reader, clock)
	const callers = 8
	results := make(chan []leaderboardusecase.Entry, callers)
	errorsFound := make(chan error, callers)
	var ready sync.WaitGroup
	ready.Add(callers)
	start := make(chan struct{})
	for range callers {
		go func() {
			ready.Done()
			<-start
			entries, err := cache.Top50(ctx)
			results <- entries
			errorsFound <- err
		}()
	}
	ready.Wait()
	close(start)
	<-started
	close(release)

	for range callers {
		require.NoError(t, <-errorsFound)
		require.Equal(t, want, <-results)
	}
}
