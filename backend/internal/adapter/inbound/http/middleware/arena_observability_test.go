package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
)

func TestArenaTransportStructuredLogging(t *testing.T) {
	var logs lockedBuffer
	logger := newTestLogger(t, &logs)
	const (
		requestID    = "arena-request-123"
		tournamentID = "72000000-0000-4000-8000-000000000001"
		privateValue = "private-transport-marker"
	)

	handler := middleware.Build(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/arena/operator/tournaments/"+tournamentID+"/actions?cursor="+privateValue,
		strings.NewReader(`{"content":"`+privateValue+`"}`),
	)
	req.Header.Set("X-Request-ID", requestID)
	req.Header.Set("Authorization", "Bearer "+privateValue)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusConflict, recorder.Code)
	entries := parseLogEntries(t, logs.String())
	var arenaEntry map[string]any
	for _, entry := range entries {
		if entry["message"] == "arena event" {
			require.Nil(t, arenaEntry, "one Arena-specific outcome is allowed per request")
			arenaEntry = entry
		}
	}
	require.NotNil(t, arenaEntry)
	require.Equal(t, "warn", arenaEntry["level"])
	require.Equal(t, "arena.http", arenaEntry["event"])
	require.Equal(t, "rejected", arenaEntry["outcome"])
	require.Equal(t, requestID, arenaEntry["correlation_id"])
	require.Equal(t, tournamentID, arenaEntry["tournament_id"])
	require.Equal(t, "http_request", arenaEntry["entity_kind"])
	require.Equal(t, requestID, arenaEntry["entity_id"])
	require.Equal(t, "operator_mutation", arenaEntry["stage"])
	require.Equal(t, "post", arenaEntry["transition"])
	require.Equal(t, "status_409", arenaEntry["reason_code"])
	require.InDelta(t, float64(0), arenaEntry["revision"], 0)
	require.GreaterOrEqual(t, arenaEntry["duration_ms"].(float64), float64(0))
	require.NotContains(t, arenaEntry, "path")
	require.NotContains(t, arenaEntry, "body")
	require.NotContains(t, arenaEntry, "cursor")
	require.NotContains(t, logs.String(), privateValue)
}
