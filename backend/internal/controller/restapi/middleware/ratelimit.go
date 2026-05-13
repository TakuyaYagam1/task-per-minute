package middleware

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/wahrwelt-kit/go-httpkit/httputil"
	httpkitmw "github.com/wahrwelt-kit/go-httpkit/httputil/middleware"
	"golang.org/x/time/rate"
)

type LoginRateLimiter struct {
	rate    rate.Limit
	burst   int
	window  time.Duration
	idleTTL time.Duration

	mu      sync.Mutex
	buckets map[string]*loginBucket
}

type loginBucket struct {
	limiter *rate.Limiter
	seenAt  time.Time
}

func NewLoginRateLimiter(ctx context.Context, attempts int, window, idleTTL time.Duration) *LoginRateLimiter {
	if attempts <= 0 || window <= 0 {
		return nil
	}
	if idleTTL <= 0 {
		idleTTL = window * 4
	}
	l := &LoginRateLimiter{
		rate:    rate.Every(window / time.Duration(attempts)),
		burst:   attempts,
		window:  window,
		idleTTL: idleTTL,
		buckets: make(map[string]*loginBucket),
	}
	go l.janitorLoop(ctx)
	return l
}

func (l *LoginRateLimiter) Allow(ip string) bool {
	if l == nil {
		return true
	}
	if ip == "" {
		return true
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[ip]
	if !ok {
		b = &loginBucket{limiter: rate.NewLimiter(l.rate, l.burst)}
		l.buckets[ip] = b
	}
	b.seenAt = now
	return b.limiter.Allow()
}

func (l *LoginRateLimiter) RetryAfter() string {
	if l == nil || l.window <= 0 {
		return ""
	}
	return retryAfterSeconds(l.window)
}

func (l *LoginRateLimiter) janitorLoop(ctx context.Context) {
	if ctx == nil {
		return
	}
	ticker := time.NewTicker(l.idleTTL / 2)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.evictIdle(time.Now())
		}
	}
}

func (l *LoginRateLimiter) evictIdle(now time.Time) {
	cutoff := now.Add(-l.idleTTL)
	l.mu.Lock()
	defer l.mu.Unlock()
	for ip, b := range l.buckets {
		if b.seenAt.Before(cutoff) {
			delete(l.buckets, ip)
		}
	}
}

type JoinRateLimiter struct {
	inner *LoginRateLimiter
}

func NewJoinRateLimiter(ctx context.Context, attempts int, window, idleTTL time.Duration) *JoinRateLimiter {
	return &JoinRateLimiter{inner: NewLoginRateLimiter(ctx, attempts, window, idleTTL)}
}

func (l *JoinRateLimiter) Allow(ip string) bool {
	if l == nil {
		return true
	}
	return l.inner.Allow(ip)
}

func (l *JoinRateLimiter) RetryAfter() string {
	if l == nil {
		return ""
	}
	return l.inner.RetryAfter()
}

func NewClientIPResolver(trustedProxyCIDRs []string) (func(*http.Request) string, error) {
	trustedNets, err := httputil.ParseTrustedProxyCIDRs(trustedProxyCIDRs)
	if err != nil {
		return nil, err
	}
	return func(r *http.Request) string {
		if r == nil {
			return ""
		}
		if ip := httpkitmw.GetClientIPFromContext(r.Context()); ip != "" {
			return ip
		}
		return httputil.GetClientIPWithNets(r, trustedNets)
	}, nil
}

func ClientIPFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if ip := httpkitmw.GetClientIPFromContext(r.Context()); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func retryAfterSeconds(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	seconds := (d + time.Second - 1) / time.Second
	if seconds < 1 {
		seconds = 1
	}
	return strconv.FormatInt(int64(seconds), 10)
}
