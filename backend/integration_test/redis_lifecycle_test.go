//go:build integration

package integration_test

import (
	"sync"
	"testing"

	testkit "github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit"
	goredis "github.com/redis/go-redis/v9"
)

type redisFx struct {
	client *goredis.Client
}

var (
	redisOnce     sync.Once
	redisFixture  *redisFx
	errRedisInit  error
	redisTeardown func()
)

func sharedRedis(t *testing.T) *redisFx {
	t.Helper()
	redisOnce.Do(func() {
		redisFixture, redisTeardown, errRedisInit = startRedis()
	})
	if errRedisInit != nil {
		t.Fatalf("redis setup: %v", errRedisInit)
	}
	return redisFixture
}

func startRedis() (*redisFx, func(), error) {
	fixture, teardown, err := testkit.StartRedis(containerStartupTimeout)
	if err != nil {
		return nil, nil, err
	}
	return &redisFx{client: fixture.Client}, teardown, nil
}
