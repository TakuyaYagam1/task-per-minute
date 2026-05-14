package middleware_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/middleware"
)

func TestBuild_SuccessAddsRequestIDSecurityHeadersAndLog(t *testing.T) {
	t.Parallel()

	var logs lockedBuffer
	log := newTestLogger(t, &logs)
	const requestID = "req-123"

	handler := middleware.Build(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, requestID, middleware.GetRequestIDFromCtx(r.Context()))
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/leaderboard", nil)
	req.Header.Set("X-Request-ID", requestID)
	req.RemoteAddr = "203.0.113.42:4567"
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, requestID, rr.Header().Get("X-Request-ID"))
	require.Equal(t, "nosniff", rr.Header().Get("X-Content-Type-Options"))
	require.Equal(t, "DENY", rr.Header().Get("X-Frame-Options"))
	require.Equal(t, "strict-origin-when-cross-origin", rr.Header().Get("Referrer-Policy"))
	require.Equal(t, "default-src 'self'", rr.Header().Get("Content-Security-Policy"))
	require.Empty(t, rr.Header().Get("Cache-Control"))

	entry := requireLogEntry(t, logs.String(), "http request")
	require.Equal(t, "info", entry["level"])
	require.Equal(t, requestID, entry["request_id"])
	require.Equal(t, http.MethodGet, entry["method"])
	require.Equal(t, "/api/v1/leaderboard", entry["path"])
	require.Equal(t, float64(http.StatusOK), entry["status"])
	require.NotEmpty(t, entry["duration"])
	require.Contains(t, entry, "duration_ms")
}

func TestBuild_AddsNoStoreHeadersForSensitiveResponses(t *testing.T) {
	t.Parallel()

	handler := middleware.Build(logkit.Noop())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "no-store", rr.Header().Get("Cache-Control"))
	require.Equal(t, "no-cache", rr.Header().Get("Pragma"))
	require.Equal(t, "0", rr.Header().Get("Expires"))
}

func TestBuild_RecovererReturnsInternalJSONAndLogsError(t *testing.T) {
	t.Parallel()

	var logs lockedBuffer
	log := newTestLogger(t, &logs)
	const requestID = "panic-req-123"

	handler := middleware.Build(log)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/duels/current", nil)
	req.Header.Set("X-Request-ID", requestID)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusInternalServerError, rr.Code)
	require.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
	require.JSONEq(t, `{
		"type":"about:blank",
		"title":"Internal Server Error",
		"status":500,
		"detail":"internal",
		"instance":"/api/v1/duels/current"
	}`, rr.Body.String())
	require.NotContains(t, rr.Body.String(), "boom")
	require.NotContains(t, strings.ToLower(rr.Body.String()), "stack")

	entries := parseLogEntries(t, logs.String())
	require.NotEmpty(t, entries)
	require.True(t, hasLogEntry(entries, func(entry map[string]any) bool {
		return entry["level"] == "error" &&
			entry["message"] == "panic recovered" &&
			entry["request_id"] == requestID &&
			entry["panic"] == "boom" &&
			strings.Contains(entry["stack"].(string), "goroutine")
	}), "expected panic log entry with request_id")
	require.True(t, hasLogEntry(entries, func(entry map[string]any) bool {
		return entry["level"] == "error" &&
			entry["message"] == "http request failed" &&
			entry["request_id"] == requestID &&
			entry["status"] == float64(http.StatusInternalServerError)
	}), "expected request log entry with 500 status")
}

func TestTimeoutReturnsProblemJSON(t *testing.T) {
	t.Parallel()

	handler := middleware.Timeout(time.Millisecond)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/slow", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusServiceUnavailable, rr.Code)
	require.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
	require.JSONEq(t, `{
		"type":"about:blank",
		"title":"Service Unavailable",
		"status":503,
		"detail":"timeout",
		"instance":"/api/v1/slow"
	}`, rr.Body.String())
}

func TestTimeoutLogsPanicAfterDeadline(t *testing.T) {
	t.Parallel()

	var logs lockedBuffer
	log := newTestLogger(t, &logs)
	const requestID = "late-panic-req"

	handler := middleware.Build(log, middleware.WithTimeout(time.Millisecond))(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		panic("late boom")
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/slow", nil)
	req.Header.Set("X-Request-ID", requestID)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusServiceUnavailable, rr.Code)
	require.Eventually(t, func() bool {
		if strings.TrimSpace(logs.String()) == "" {
			return false
		}
		return hasLogEntry(parseLogEntries(t, logs.String()), func(entry map[string]any) bool {
			return entry["level"] == "error" &&
				entry["message"] == "panic recovered" &&
				entry["request_id"] == requestID &&
				entry["panic"] == "late boom" &&
				entry["after_timeout"] == true
		})
	}, 250*time.Millisecond, 10*time.Millisecond)
}

func TestBuild_TrustedProxyCIDRsResolveForwardedClientIP(t *testing.T) {
	t.Parallel()

	var got string
	handler := middleware.Build(
		logkit.Noop(),
		middleware.WithTrustedProxyCIDRs([]string{"127.0.0.0/8"}),
	)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = middleware.ClientIPFromRequest(r)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/leaderboard", nil)
	req.RemoteAddr = "127.0.0.1:4567"
	req.Header.Set("X-Forwarded-For", "198.51.100.42")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "198.51.100.42", got)
}

func newTestLogger(t *testing.T, buf *lockedBuffer) logkit.Logger {
	t.Helper()

	log, err := logkit.New(
		logkit.WithLevel(logkit.DebugLevel),
		logkit.WithSyncWriter(buf),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, log.Close())
	})
	return log
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func requireLogEntry(t *testing.T, raw, message string) map[string]any {
	t.Helper()

	for _, entry := range parseLogEntries(t, raw) {
		if entry["message"] == message {
			return entry
		}
	}
	t.Fatalf("log entry with message %q not found in %s", message, raw)
	return nil
}

func parseLogEntries(t *testing.T, raw string) []map[string]any {
	t.Helper()

	raw = strings.TrimSpace(raw)
	require.NotEmpty(t, raw)

	lines := strings.Split(raw, "\n")
	entries := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry))
		entries = append(entries, entry)
	}
	return entries
}

func hasLogEntry(entries []map[string]any, match func(map[string]any) bool) bool {
	for _, entry := range entries {
		if match(entry) {
			return true
		}
	}
	return false
}
