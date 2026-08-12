package websocket

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	httpkitmw "github.com/wahrwelt-kit/go-httpkit/httputil/middleware"
)

func TestWriteHandshakeProblemPreservesProblemDetailsShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		request      func() *http.Request
		requestID    string
		status       int
		detail       string
		wantInstance string
	}{
		{
			name: "unauthorized",
			request: func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "/ws", nil)
			},
			status:       http.StatusUnauthorized,
			detail:       "authentication required",
			wantInstance: "/ws",
		},
		{
			name: "forbidden with request id",
			request: func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "/ws", nil)
			},
			requestID:    "req-forbidden",
			status:       http.StatusForbidden,
			detail:       "origin forbidden",
			wantInstance: "/ws",
		},
		{
			name: "not found strips query from instance",
			request: func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "/missing?token=secret", nil)
			},
			status:       http.StatusNotFound,
			detail:       "not found",
			wantInstance: "/missing",
		},
		{
			name: "method not allowed",
			request: func() *http.Request {
				return httptest.NewRequest(http.MethodPost, "/ws", nil)
			},
			status:       http.StatusMethodNotAllowed,
			detail:       "method not allowed",
			wantInstance: "/ws",
		},
		{
			name: "too many requests",
			request: func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "/ws", nil)
			},
			status:       http.StatusTooManyRequests,
			detail:       "rate limit exceeded",
			wantInstance: "/ws",
		},
		{
			name:         "nil request",
			request:      func() *http.Request { return nil },
			status:       http.StatusUnauthorized,
			detail:       "authentication required",
			wantInstance: "",
		},
		{
			name: "nil url",
			request: func() *http.Request {
				return &http.Request{Method: http.MethodGet, Header: make(http.Header)}
			},
			status:       http.StatusForbidden,
			detail:       "origin forbidden",
			wantInstance: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rr := httptest.NewRecorder()
			req := tt.request()
			serve := func(w http.ResponseWriter, r *http.Request) {
				writeHandshakeProblem(w, r, tt.status, tt.detail)
			}
			if tt.requestID == "" {
				serve(rr, req)
			} else {
				req.Header.Set("X-Request-ID", tt.requestID)
				httpkitmw.RequestID()(http.HandlerFunc(serve)).ServeHTTP(rr, req)
			}

			require.Equal(t, tt.status, rr.Code)
			require.Equal(t, wsProblemContentType, rr.Header().Get("Content-Type"))

			want := map[string]any{
				"type":     "about:blank",
				"title":    http.StatusText(tt.status),
				"status":   float64(tt.status),
				"detail":   tt.detail,
				"instance": tt.wantInstance,
			}
			if tt.requestID != "" {
				want["request_id"] = tt.requestID
			}
			var got map[string]any
			require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
			require.Equal(t, want, got)
		})
	}
}

func TestProblemStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		want   int32
	}{
		{name: "lower boundary", status: http.StatusBadRequest, want: http.StatusBadRequest},
		{name: "upper boundary", status: http.StatusNetworkAuthenticationRequired, want: http.StatusNetworkAuthenticationRequired},
		{name: "below problem range", status: http.StatusOK, want: http.StatusInternalServerError},
		{name: "above problem range", status: 512, want: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, problemStatus(tt.status))
		})
	}
}
