package redis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

const (
	defaultRateLimitKeyPrefix = "rate-limit:"
	rateLimitCallTimeout      = 750 * time.Millisecond
	maxRateLimitScopeBytes    = 1024
)

var fixedWindowScript = goredis.NewScript(`
local current = redis.call('INCR', KEYS[1])
if current == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
if redis.call('PTTL', KEYS[1]) < 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
if current <= tonumber(ARGV[2]) then
  return 1
end
return 0
`)

// RateLimiter is a shared fixed-window limiter. Every bucket is an atomic
// Redis counter with a TTL, so replicas observe one bounded endpoint scope.
type RateLimiter struct {
	client     *goredis.Client
	class      string
	attempts   int64
	window     time.Duration
	retryAfter string
}

func NewRateLimiter(client *goredis.Client, class string, attempts int, window time.Duration) *RateLimiter {
	class = strings.TrimSpace(class)
	if client == nil || class == "" || strings.ContainsAny(class, ":\x00") || attempts <= 0 || window.Milliseconds() < 1 {
		return &RateLimiter{}
	}
	return &RateLimiter{
		client:     client,
		class:      class,
		attempts:   int64(attempts),
		window:     window,
		retryAfter: rateLimitRetryAfter(window),
	}
}

// Allow fails closed when the shared store cannot make an authoritative
// decision. Scope material is hashed before it becomes a Redis key.
func (l *RateLimiter) Allow(scope string) bool {
	if l == nil || l.client == nil || l.class == "" || l.attempts <= 0 || l.window.Milliseconds() < 1 ||
		scope == "" || len(scope) > maxRateLimitScopeBytes {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), rateLimitCallTimeout)
	defer cancel()

	allowed, err := fixedWindowScript.Run(
		ctx,
		l.client,
		[]string{l.bucketKey(scope)},
		l.window.Milliseconds(),
		l.attempts,
	).Int()
	return err == nil && allowed == 1
}

func (l *RateLimiter) RetryAfter() string {
	if l == nil {
		return ""
	}
	return l.retryAfter
}

func (l *RateLimiter) bucketKey(scope string) string {
	digest := sha256.Sum256([]byte(scope))
	return defaultRateLimitKeyPrefix + l.class + ":" + hex.EncodeToString(digest[:])
}

func rateLimitRetryAfter(window time.Duration) string {
	seconds := (window + time.Second - 1) / time.Second
	if seconds < 1 {
		seconds = 1
	}
	return strconv.FormatInt(int64(seconds), 10)
}
