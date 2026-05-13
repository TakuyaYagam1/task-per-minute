package websocket

import (
	"context"
	"time"
)

func runAfterOrDone(ctx context.Context, delay time.Duration, fn func()) {
	if fn == nil {
		return
	}
	if delay <= 0 {
		fn()
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	timer := time.NewTimer(delay)
	go func() {
		defer timer.Stop()
		select {
		case <-timer.C:
			fn()
		case <-ctx.Done():
		}
	}()
}
