package middleware

// RateLimiter protects a request class using a caller-provided stable scope.
// Implementations live in outbound adapters so limits remain process-wide.
type RateLimiter interface {
	Allow(scope string) bool
	RetryAfter() string
}
