package arena

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	logkit "github.com/wahrwelt-kit/go-logkit"

	appobservability "github.com/TakuyaYagam1/task-per-minute/internal/observability"
)

func TestArenaTransportStructuredLogging(t *testing.T) {
	tournamentID := uuid.MustParse("73000000-0000-4000-8000-000000000001")
	const requestID = "arena-ws-request-123"

	require.Equal(t, requestID, TransportCorrelationID(requestID, tournamentID))
	require.Equal(t, tournamentID.String(), TransportCorrelationID(strings.Repeat("x", 129), tournamentID))

	var output bytes.Buffer
	logger, err := logkit.New(
		logkit.WithLevel(logkit.DebugLevel),
		logkit.WithSyncWriter(&output),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, logger.Close()) })
	observer := appobservability.NewArenaStructuredLogger(logger)

	tests := []struct {
		action  string
		outcome string
		reason  string
		level   string
	}{
		{action: ArenaTransportConnect, outcome: appobservability.ArenaOutcomeSuccess, reason: "opened", level: "info"},
		{action: ArenaTransportResume, outcome: appobservability.ArenaOutcomeSuccess, reason: "resumed", level: "info"},
		{action: ArenaTransportReject, outcome: appobservability.ArenaOutcomeRejected, reason: "arena_invalid_payload", level: "warn"},
		{action: ArenaTransportDelivery, outcome: appobservability.ArenaOutcomeSuccess, reason: "snapshot", level: "info"},
		{action: ArenaTransportDisconnect, outcome: appobservability.ArenaOutcomeSuccess, reason: "closed", level: "info"},
	}
	for index, test := range tests {
		err := ObserveTransportEvent(t.Context(), observer, ArenaTransportEvent{
			CorrelationID: requestID,
			TournamentID:  tournamentID,
			Role:          "participant",
			Action:        test.action,
			Outcome:       test.outcome,
			ReasonCode:    test.reason,
			Duration:      time.Duration(index+1) * time.Millisecond,
			Revision:      int64(index + 1),
		})
		require.NoError(t, err)
	}

	entries := arenaTransportLogEntries(t, output.String())
	require.Len(t, entries, len(tests))
	for index, entry := range entries {
		require.Equal(t, tests[index].level, entry["level"])
		require.Equal(t, "arena.websocket", entry["event"])
		require.Equal(t, tests[index].outcome, entry["outcome"])
		require.Equal(t, requestID, entry["correlation_id"])
		require.Equal(t, tournamentID.String(), entry["tournament_id"])
		require.Equal(t, "ws_connection", entry["entity_kind"])
		require.Equal(t, requestID, entry["entity_id"])
		require.Equal(t, "participant", entry["stage"])
		require.Equal(t, tests[index].action, entry["transition"])
		require.Equal(t, tests[index].reason, entry["reason_code"])
		require.InDelta(t, float64(index+1), entry["duration_ms"], 0)
		require.InDelta(t, float64(index+1), entry["revision"], 0)
		for _, forbidden := range []string{"body", "frame", "cursor", "payload", "raw_error"} {
			require.NotContains(t, entry, forbidden)
		}
	}

	err = ObserveTransportEvent(t.Context(), observer, ArenaTransportEvent{
		CorrelationID: requestID,
		Role:          "participant",
		Action:        ArenaTransportReject,
		Outcome:       appobservability.ArenaOutcomeRejected,
		ReasonCode:    "arena_invalid_payload",
	})
	require.Error(t, err)
	require.Len(t, arenaTransportLogEntries(t, output.String()), len(tests))
}

func arenaTransportLogEntries(t *testing.T, raw string) []map[string]any {
	t.Helper()

	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	lines := strings.Split(raw, "\n")
	entries := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry))
		if entry["message"] == "arena event" {
			entries = append(entries, entry)
		}
	}
	return entries
}
