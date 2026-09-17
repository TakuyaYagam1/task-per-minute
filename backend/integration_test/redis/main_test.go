//go:build integration

package redis_test

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"

	testkit "github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency"
)

const containerStartupTimeout = 90 * time.Second

type redisFx struct {
	client *goredis.Client
}

var sharedRedisFixture *redisFx

func TestMain(m *testing.M) {
	fixture, teardown, err := testkit.StartRedis(containerStartupTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "integration_test/redis: failed to start redis: %v\n", err)
		os.Exit(1)
	}
	sharedRedisFixture = &redisFx{client: fixture.Client}

	code := m.Run()
	teardown()
	os.Exit(code)
}

func sharedRedis(t *testing.T) *redisFx {
	t.Helper()
	if sharedRedisFixture == nil {
		t.Fatal("redis fixture is not initialized")
	}
	return sharedRedisFixture
}

func integrationLease(t *testing.T) idempotency.LeaseToken {
	t.Helper()
	lease, err := idempotency.NewLeaseToken()
	if err != nil {
		t.Fatal(err)
	}
	return lease
}

func uniq(prefix string) string {
	return prefix + "_" + uuid.NewString()[:16]
}
