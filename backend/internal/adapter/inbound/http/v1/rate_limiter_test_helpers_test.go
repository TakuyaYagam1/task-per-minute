package v1

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/mock"

	middlewaremocks "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware/mocks"
)

func newAllowingRateLimiter(t *testing.T) *middlewaremocks.MockRateLimiter {
	t.Helper()

	limiter := middlewaremocks.NewMockRateLimiter(t)
	limiter.EXPECT().Allow(mock.Anything).Return(true).Maybe()
	return limiter
}

func newOneRequestRateLimiter(t *testing.T, retryAfter string) *middlewaremocks.MockRateLimiter {
	t.Helper()

	var calls atomic.Int32
	limiter := middlewaremocks.NewMockRateLimiter(t)
	limiter.EXPECT().Allow(mock.Anything).RunAndReturn(func(string) bool {
		return calls.Add(1) == 1
	}).Maybe()
	limiter.EXPECT().RetryAfter().Return(retryAfter).Maybe()
	return limiter
}
