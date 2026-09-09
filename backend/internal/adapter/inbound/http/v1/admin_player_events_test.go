package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
)

func TestStreamAdminPlayerEventsWritesReadyAndChangeEvents(t *testing.T) {
	t.Parallel()

	events := make(chan struct{}, 1)
	subscribed := make(chan struct{})
	unsubscribed := make(chan struct{})
	subscriber := NewMockAdminPlayerEventSubscriber(t)
	subscriber.EXPECT().SubscribeAdminPlayerChanges(mock.Anything).
		Run(func(context.Context) { close(subscribed) }).
		Return(events, func() { close(unsubscribed) }, nil)
	server := New(Dependencies{AdminPlayerEvents: subscriber})
	verifier := newAdminAccessVerifier(t)

	handler := middleware.AdminSession(verifier)(http.HandlerFunc(server.StreamPlayerEvents))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/players/events", nil).WithContext(ctx)
	req.AddCookie(&http.Cookie{Name: middleware.AdminAccessCookieName, Value: adminAccessTestToken})
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(rr, req)
	}()

	require.Eventually(t, func() bool {
		select {
		case <-subscribed:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)

	events <- struct{}{}
	close(events)

	require.Eventually(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool {
		select {
		case <-unsubscribed:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)

	body := rr.Body.String()
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "text/event-stream", rr.Header().Get("Content-Type"))
	require.Contains(t, body, "event: ready\ndata: {}\n\n")
	require.Contains(t, body, "event: players_changed\ndata: {}\n\n")
}

func TestStreamAdminPlayerEventsRequiresAdmin(t *testing.T) {
	t.Parallel()

	server := New(Dependencies{AdminPlayerEvents: NewMockAdminPlayerEventSubscriber(t)})
	handler := http.HandlerFunc(server.StreamPlayerEvents)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/players/events", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusUnauthorized, rr.Code)
	require.Contains(t, rr.Body.String(), `"status":401`)
}
