package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
)

func TestTournamentTransportStructuredLogging(t *testing.T) {
	var logs lockedBuffer
	logger := newTestLogger(t, &logs)
	const (
		requestID    = "tournament-request-123"
		commandID    = "72000000-0000-4000-8000-000000000002"
		tournamentID = "72000000-0000-4000-8000-000000000001"
		privateValue = "private-transport-marker"
	)

	handler := middleware.Build(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/tournaments/"+tournamentID+"/actions?cursor="+privateValue,
		strings.NewReader(`{"content":"`+privateValue+`"}`),
	)
	req.Header.Set("X-Request-ID", requestID)
	req.Header.Set("Idempotency-Key", commandID)
	req.Header.Set("Authorization", "Bearer "+privateValue)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusConflict, recorder.Code)
	entries := parseLogEntries(t, logs.String())
	var tournamentEntry map[string]any
	for _, entry := range entries {
		if entry["message"] == "tournament event" {
			require.Nil(t, tournamentEntry, "one tournament-specific outcome is allowed per request")
			tournamentEntry = entry
		}
	}
	require.NotNil(t, tournamentEntry)
	require.Equal(t, "warn", tournamentEntry["level"])
	require.Equal(t, "tournament.http", tournamentEntry["event"])
	require.Equal(t, "rejected", tournamentEntry["outcome"])
	require.Equal(t, commandID, tournamentEntry["correlation_id"])
	require.Equal(t, tournamentID, tournamentEntry["tournament_id"])
	require.Equal(t, "http_request", tournamentEntry["entity_kind"])
	require.Equal(t, commandID, tournamentEntry["entity_id"])
	require.Equal(t, "operator_mutation", tournamentEntry["stage"])
	require.Equal(t, "post", tournamentEntry["transition"])
	require.Equal(t, "status_409", tournamentEntry["reason_code"])
	require.InDelta(t, float64(0), tournamentEntry["revision"], 0)
	require.GreaterOrEqual(t, tournamentEntry["duration_ms"].(float64), float64(0))
	require.NotContains(t, tournamentEntry, "path")
	require.NotContains(t, tournamentEntry, "body")
	require.NotContains(t, tournamentEntry, "cursor")
	require.NotContains(t, logs.String(), privateValue)
}

func TestTournamentTransportStructuredLoggingRejectsInvalidCommandCorrelation(t *testing.T) {
	var logs lockedBuffer
	logger := newTestLogger(t, &logs)
	const (
		requestID    = "tournament-request-124"
		tournamentID = "72000000-0000-4000-8000-000000000003"
		privateValue = "private-idempotency-marker"
	)

	handler := middleware.Build(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/tournaments/"+tournamentID+"/actions", nil)
	req.Header.Set("X-Request-ID", requestID)
	req.Header.Add("Idempotency-Key", privateValue)
	req.Header.Add("Idempotency-Key", "72000000-0000-4000-8000-000000000004")
	req.Header.Set("X-Private-Correlation", privateValue)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusConflict, recorder.Code)
	entries := parseLogEntries(t, logs.String())
	var tournamentEntry map[string]any
	for _, entry := range entries {
		if entry["message"] == "tournament event" {
			tournamentEntry = entry
		}
	}
	require.NotNil(t, tournamentEntry)
	require.Equal(t, requestID, tournamentEntry["correlation_id"])
	require.Equal(t, requestID, tournamentEntry["entity_id"])
	require.NotContains(t, logs.String(), privateValue)
}

func TestTournamentTransportStructuredLoggingUsesRequestCorrelationForReads(t *testing.T) {
	var logs lockedBuffer
	logger := newTestLogger(t, &logs)
	const (
		requestID    = "tournament-request-125"
		commandID    = "72000000-0000-4000-8000-000000000005"
		tournamentID = "72000000-0000-4000-8000-000000000006"
	)

	handler := middleware.Build(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tournaments/"+tournamentID, nil)
	req.Header.Set("X-Request-ID", requestID)
	req.Header.Set("Idempotency-Key", commandID)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	entries := parseLogEntries(t, logs.String())
	var tournamentEntry map[string]any
	for _, entry := range entries {
		if entry["message"] == "tournament event" {
			tournamentEntry = entry
		}
	}
	require.NotNil(t, tournamentEntry)
	require.Equal(t, requestID, tournamentEntry["correlation_id"])
	require.Equal(t, requestID, tournamentEntry["entity_id"])
}
