package leaderboard

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	cachekit "github.com/wahrwelt-kit/go-cachekit"
	"golang.org/x/sync/singleflight"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
	"github.com/TakuyaYagam1/task-per-minute/pkg/clock"
)

const (
	topLimit    = 50
	cacheKey    = "leaderboard:top50"
	cacheTTL    = 10 * time.Second
	cacheMaxLen = 1
)

// Entry aliases usecase.LeaderboardEntry so callers may use either
// leaderboard.Entry or usecase.LeaderboardEntry.
type Entry = usecase.LeaderboardEntry

type LeaderboardUsecase struct {
	store usecase.LeaderboardStore
	repo  usecase.LeaderboardRepo
	clock clock.Clock

	mu    sync.Mutex
	cache *cachekit.LRFUCache[string, cachedTop]

	sf singleflight.Group
}

type cachedTop struct {
	entries   []Entry
	expiresAt time.Time
}

func NewLeaderboardUsecase(store usecase.LeaderboardStore, repo usecase.LeaderboardRepo, clk clock.Clock) *LeaderboardUsecase {
	if clk == nil {
		clk = clock.Real{}
	}
	return &LeaderboardUsecase{
		store: store,
		repo:  repo,
		clock: clk,
		cache: cachekit.NewLRFUCache[string, cachedTop](cacheMaxLen),
	}
}

func (u *LeaderboardUsecase) IncrementWin(ctx context.Context, username string) error {
	err := u.store.IncrementWin(ctx, username)
	u.Invalidate()
	if err != nil {
		return fmt.Errorf("LeaderboardUsecase - IncrementWin - LeaderboardStore.IncrementWin: %w", err)
	}
	return nil
}

func (u *LeaderboardUsecase) Top50(ctx context.Context) ([]Entry, error) {
	now := u.clock.Now()
	if entries, ok := u.cached(now); ok {
		return entries, nil
	}

	v, err, _ := u.sf.Do(cacheKey, func() (any, error) {
		refreshNow := u.clock.Now()
		if cached, ok := u.cached(refreshNow); ok {
			return cached, nil
		}

		entries, err := u.loadTop(ctx)
		if err != nil {
			return nil, err
		}

		u.mu.Lock()
		u.cache.Set(cacheKey, cachedTop{
			entries:   cloneEntries(entries),
			expiresAt: refreshNow.Add(cacheTTL),
		})
		u.mu.Unlock()

		return entries, nil
	})
	if err != nil {
		return nil, err
	}
	entries, ok := v.([]Entry)
	if !ok {
		return nil, fmt.Errorf("LeaderboardUsecase - Top50 - unexpected singleflight result type %T", v)
	}
	return cloneEntries(entries), nil
}

func (u *LeaderboardUsecase) cached(now time.Time) ([]Entry, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()

	cached, ok := u.cache.Get(cacheKey)
	if !ok || !now.Before(cached.expiresAt) {
		return nil, false
	}
	return cloneEntries(cached.entries), true
}

func (u *LeaderboardUsecase) Invalidate() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.cache = cachekit.NewLRFUCache[string, cachedTop](cacheMaxLen)
}

func (u *LeaderboardUsecase) loadTop(ctx context.Context) ([]Entry, error) {
	stats, err := u.repo.TopStats(ctx, topLimit)
	if err != nil {
		return nil, fmt.Errorf("LeaderboardUsecase - Top50 - LeaderboardRepo.TopStats: %w", err)
	}
	if len(stats) == 0 {
		return []Entry{}, nil
	}

	entries := make([]Entry, 0, len(stats))
	for _, row := range stats {
		entries = append(entries, Entry{
			Username:           row.Username,
			Wins:               row.Wins,
			AverageSolveTimeMs: row.AverageSolveTimeMs,
		})
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Wins != entries[j].Wins {
			return entries[i].Wins > entries[j].Wins
		}
		if entries[i].AverageSolveTimeMs != entries[j].AverageSolveTimeMs {
			return entries[i].AverageSolveTimeMs < entries[j].AverageSolveTimeMs
		}
		return entries[i].Username < entries[j].Username
	})

	if len(entries) > topLimit {
		entries = entries[:topLimit]
	}
	for i := range entries {
		entries[i].Rank = i + 1
	}
	return entries, nil
}

func cloneEntries(entries []Entry) []Entry {
	out := make([]Entry, len(entries))
	copy(out, entries)
	return out
}
