package requestmeta_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	httpkitmw "github.com/wahrwelt-kit/go-httpkit/httputil/middleware"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/requestmeta"
)

func TestPlayerSessionTokenFromRequest(t *testing.T) {
	t.Parallel()

	validToken := uuid.New()
	tests := []struct {
		name    string
		request *http.Request
		want    uuid.UUID
		ok      bool
	}{
		{name: "nil request"},
		{name: "missing cookie", request: httptest.NewRequest(http.MethodGet, tournamentParticipantRealtimePath, nil)},
		{
			name:    "invalid cookie",
			request: requestWithSessionCookie("not-a-uuid"),
		},
		{
			name:    "nil UUID",
			request: requestWithSessionCookie(uuid.Nil.String()),
		},
		{
			name:    "valid cookie",
			request: requestWithSessionCookie(validToken.String()),
			want:    validToken,
			ok:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := requestmeta.PlayerSessionTokenFromRequest(tt.request)
			require.Equal(t, tt.ok, ok)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestRequestIDFromContext(t *testing.T) {
	t.Parallel()

	var got string
	handler := httpkitmw.RequestID()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = requestmeta.RequestIDFromContext(r.Context())
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, tournamentParticipantRealtimePath, nil))

	require.NotEmpty(t, got)
}

func requestWithSessionCookie(value string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, tournamentParticipantRealtimePath, nil)
	req.AddCookie(&http.Cookie{Name: requestmeta.PlayerSessionCookieName, Value: value})
	return req
}

const tournamentParticipantRealtimePath = "/api/v1/tournaments/2c754c2e-8458-4417-b049-44c5f92840c7/participant/realtime"
