package ctxutil

import (
	"context"
	"time"
)

// DetachedWithTimeout preserves values from ctx while decoupling async cleanup
// from request cancellation and bounding how long the detached work may run.
func DetachedWithTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	detached := context.WithoutCancel(ctx)
	if timeout <= 0 {
		return context.WithCancel(detached)
	}
	return context.WithTimeout(detached, timeout)
}
