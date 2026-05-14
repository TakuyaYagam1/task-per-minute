package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/middleware"
)

func TestStreamAdminPlayerEventsWritesReadyAndChangeEvents(t *testing.T) {
	t.Parallel()

	events := make(chan struct{}, 1)
	subscriber := &adminPlayerEventsFake{
		events:       events,
		subscribed:   make(chan struct{}),
		unsubscribed: make(chan struct{}),
	}
	server := New(Dependencies{AdminPlayerEvents: subscriber})
	auth := newAdminCookieAuthUsecase(t)
	pair, err := auth.Login(t.Context(), "admin-password")
	require.NoError(t, err)

	handler := middleware.AdminJWT(auth)(http.HandlerFunc(server.StreamAdminPlayerEvents))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/players/events", nil).WithContext(ctx)
	req.AddCookie(&http.Cookie{Name: middleware.AdminAccessCookieName, Value: pair.AccessToken})
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(rr, req)
	}()

	require.Eventually(t, func() bool {
		select {
		case <-subscriber.subscribed:
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
		case <-subscriber.unsubscribed:
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

	server := New(Dependencies{AdminPlayerEvents: &adminPlayerEventsFake{}})
	handler := http.HandlerFunc(server.StreamAdminPlayerEvents)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/players/events", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusUnauthorized, rr.Code)
	require.True(t, strings.Contains(rr.Body.String(), `"status":401`))
}

type adminPlayerEventsFake struct {
	events       <-chan struct{}
	subscribed   chan struct{}
	unsubscribed chan struct{}
	err          error
}

func (f *adminPlayerEventsFake) SubscribeAdminPlayerChanges(context.Context) (<-chan struct{}, func(), error) {
	if f.subscribed != nil {
		close(f.subscribed)
	}
	if f.err != nil {
		return nil, nil, f.err
	}
	unsubscribe := func() {
		if f.unsubscribed != nil {
			close(f.unsubscribed)
		}
	}
	return f.events, unsubscribe, nil
}
