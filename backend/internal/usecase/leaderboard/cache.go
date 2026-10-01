package leaderboard

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	cacheKey = "top50"
	cacheTTL = 10 * time.Second
)

type Reader interface {
	Top50(ctx context.Context) ([]Entry, error)
}

type Clock interface {
	Now() time.Time
}

type Cache struct {
	next  Reader
	clock Clock
	pages PageReader

	mu       sync.Mutex
	snapshot *snapshot
	refresh  singleflight.Group
}

type snapshot struct {
	entries   []Entry
	expiresAt time.Time
}

func NewCache(next Reader, clock Clock, pages ...PageReader) *Cache {
	cache := &Cache{next: next, clock: clock}
	if len(pages) > 0 {
		cache.pages = pages[0]
	}
	return cache
}

func (c *Cache) Top50(ctx context.Context) ([]Entry, error) {
	now := c.clock.Now()
	if entries, ok := c.cached(now); ok {
		return entries, nil
	}

	value, err, _ := c.refresh.Do(cacheKey, func() (any, error) {
		refreshTime := c.clock.Now()
		if entries, ok := c.cached(refreshTime); ok {
			return entries, nil
		}

		entries, loadErr := c.next.Top50(ctx)
		if loadErr != nil {
			return nil, loadErr
		}

		c.mu.Lock()
		c.snapshot = &snapshot{
			entries:   cloneEntries(entries),
			expiresAt: refreshTime.Add(cacheTTL),
		}
		c.mu.Unlock()
		return entries, nil
	})
	if err != nil {
		return nil, err
	}
	entries, ok := value.([]Entry)
	if !ok {
		return nil, fmt.Errorf("leaderboard cache: unexpected refresh result %T", value)
	}
	return cloneEntries(entries), nil
}

func (c *Cache) Page(ctx context.Context, query PageQuery) (PageResult, error) {
	if c.pages == nil {
		return PageResult{}, errors.New("leaderboard cache: page reader is not configured")
	}
	return c.pages.Page(ctx, query)
}

func (c *Cache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snapshot = nil
}

func (c *Cache) cached(now time.Time) ([]Entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.snapshot == nil || !now.Before(c.snapshot.expiresAt) {
		return nil, false
	}
	return cloneEntries(c.snapshot.entries), true
}

func cloneEntries(entries []Entry) []Entry {
	return append([]Entry(nil), entries...)
}
