//go:build integration

package integration_test

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	redisadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/redis"
)

func TestRateLimiterIsSharedAcrossReplicasAndExpires(t *testing.T) {
	redis := sharedRedis(t)
	class := fmt.Sprintf("integration-%s", uuid.NewString())
	first := redisadapter.NewRateLimiter(redis.client, class, 5, 200*time.Millisecond)
	second := redisadapter.NewRateLimiter(redis.client, class, 5, 200*time.Millisecond)

	var allowed atomic.Int64
	var group sync.WaitGroup
	for index := 0; index < 24; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			limiter := first
			if index%2 == 1 {
				limiter = second
			}
			if limiter.Allow("198.51.100.12") {
				allowed.Add(1)
			}
		}(index)
	}
	group.Wait()

	require.EqualValues(t, 5, allowed.Load())
	require.False(t, redisadapter.NewRateLimiter(redis.client, class, 5, 200*time.Millisecond).Allow("198.51.100.12"))

	time.Sleep(250 * time.Millisecond)
	require.True(t, second.Allow("198.51.100.12"))
}

func TestRateLimiterSeparatesEndpointClassesAcrossReplicas(t *testing.T) {
	redis := sharedRedis(t)
	publicClass := fmt.Sprintf("integration-public-read-%s", uuid.NewString())
	operatorClass := fmt.Sprintf("integration-operator-read-%s", uuid.NewString())
	firstPublic := redisadapter.NewRateLimiter(redis.client, publicClass, 1, time.Minute)
	secondPublic := redisadapter.NewRateLimiter(redis.client, publicClass, 1, time.Minute)
	operator := redisadapter.NewRateLimiter(redis.client, operatorClass, 1, time.Minute)
	scope := "operator:operator-42"

	require.True(t, firstPublic.Allow(scope))
	require.False(t, secondPublic.Allow(scope))
	require.True(t, operator.Allow(scope))
}
