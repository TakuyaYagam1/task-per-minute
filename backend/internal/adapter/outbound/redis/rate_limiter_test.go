package redis

import (
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestRateLimiterFailsClosedWithoutAuthoritativeStore(t *testing.T) {
	t.Parallel()

	limiter := NewRateLimiter(nil, "admin-login", 5, time.Minute)

	require.False(t, limiter.Allow("192.0.2.1"))
	require.Empty(t, limiter.RetryAfter())
}

func TestRateLimiterRejectsSubMillisecondWindow(t *testing.T) {
	t.Parallel()

	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { require.NoError(t, client.Close()) })

	limiter := NewRateLimiter(client, "public-read", 1, time.Nanosecond)

	require.Nil(t, limiter.client)
	require.False(t, limiter.Allow("public:198.51.100.42"))
}

func TestRateLimiterBucketKeySeparatesClassAndHidesScope(t *testing.T) {
	t.Parallel()

	limiter := &RateLimiter{class: "player-join"}
	first := limiter.bucketKey("203.0.113.7")
	second := limiter.bucketKey("203.0.113.8")

	require.NotEqual(t, first, second)
	require.NotContains(t, first, "203.0.113.7")
	require.Equal(t, first, limiter.bucketKey("203.0.113.7"))
}

func TestRateLimiterRetryAfterRoundsUp(t *testing.T) {
	t.Parallel()

	limiter := &RateLimiter{retryAfter: rateLimitRetryAfter(1500 * time.Millisecond)}

	require.Equal(t, "2", limiter.RetryAfter())
}
