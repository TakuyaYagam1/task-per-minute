package websocket

import "time"

func runAfterOrDone(done <-chan struct{}, delay time.Duration, fn func()) {
	if fn == nil {
		return
	}
	if delay <= 0 {
		fn()
		return
	}

	timer := time.NewTimer(delay)
	go func() {
		defer timer.Stop()
		select {
		case <-timer.C:
			fn()
		case <-done:
		}
	}()
}
