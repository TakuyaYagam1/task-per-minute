package observability_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
	observabilitymocks "github.com/TakuyaYagam1/task-per-minute/internal/observability/mocks"
)

func TestTournamentStructuredLogging(t *testing.T) {
	t.Run("builds a canonical event", func(t *testing.T) {
		input := validTournamentEventInput()

		event, err := observability.NewTournamentEvent(input)

		require.NoError(t, err)
		require.Equal(t, observability.TournamentEvent{
			Event:         "tournament.transition",
			Outcome:       observability.TournamentOutcomeSuccess,
			CorrelationID: "corr-123",
			TournamentID:  "tournament-123",
			EntityKind:    "wave",
			EntityID:      "wave-123",
			Stage:         "qualifier",
			Transition:    "scheduled_to_active",
			Duration:      1500 * time.Millisecond,
			ReasonCode:    "completed",
			Revision:      7,
		}, event)
	})

	t.Run("requires command identity for command outcomes", func(t *testing.T) {
		input := validTournamentEventInput()
		input.Event = "tournament.command.accepted"

		_, err := observability.NewTournamentEvent(input)

		require.Error(t, err)

		input.CommandID = "command-123"
		event, err := observability.NewTournamentEvent(input)
		require.NoError(t, err)
		require.Equal(t, "command-123", event.CommandID)
	})

	t.Run("rejects missing required fields", func(t *testing.T) {
		tests := []struct {
			name   string
			mutate func(*observability.TournamentEventInput)
		}{
			{name: "event", mutate: func(input *observability.TournamentEventInput) { input.Event = "" }},
			{name: "outcome", mutate: func(input *observability.TournamentEventInput) { input.Outcome = "" }},
			{name: "correlation", mutate: func(input *observability.TournamentEventInput) { input.CorrelationID = "" }},
			{name: "tournament", mutate: func(input *observability.TournamentEventInput) { input.TournamentID = "" }},
			{name: "entity kind", mutate: func(input *observability.TournamentEventInput) { input.EntityKind = "" }},
			{name: "entity id", mutate: func(input *observability.TournamentEventInput) { input.EntityID = "" }},
			{name: "stage", mutate: func(input *observability.TournamentEventInput) { input.Stage = "" }},
			{name: "transition", mutate: func(input *observability.TournamentEventInput) { input.Transition = "" }},
			{name: "reason", mutate: func(input *observability.TournamentEventInput) { input.ReasonCode = "" }},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				input := validTournamentEventInput()
				test.mutate(&input)

				_, err := observability.NewTournamentEvent(input)

				require.Error(t, err)
			})
		}
	})

	t.Run("accepts only canonical outcomes", func(t *testing.T) {
		for _, outcome := range []string{
			observability.TournamentOutcomeSuccess,
			observability.TournamentOutcomeRetry,
			observability.TournamentOutcomeRejected,
			observability.TournamentOutcomeFailure,
		} {
			input := validTournamentEventInput()
			input.Outcome = outcome
			_, err := observability.NewTournamentEvent(input)
			require.NoError(t, err, outcome)
		}

		input := validTournamentEventInput()
		input.Outcome = "unknown"
		_, err := observability.NewTournamentEvent(input)
		require.Error(t, err)
	})

	t.Run("rejects negative scalar fields", func(t *testing.T) {
		input := validTournamentEventInput()
		input.Duration = -time.Nanosecond
		_, err := observability.NewTournamentEvent(input)
		require.Error(t, err)

		input = validTournamentEventInput()
		input.Revision = -1
		_, err = observability.NewTournamentEvent(input)
		require.Error(t, err)
	})

	t.Run("bounds every string field", func(t *testing.T) {
		long := strings.Repeat("a", 257)
		tests := []struct {
			name   string
			mutate func(*observability.TournamentEventInput)
		}{
			{name: "event", mutate: func(input *observability.TournamentEventInput) { input.Event = long }},
			{name: "correlation", mutate: func(input *observability.TournamentEventInput) { input.CorrelationID = long }},
			{name: "command", mutate: func(input *observability.TournamentEventInput) { input.CommandID = long }},
			{name: "tournament", mutate: func(input *observability.TournamentEventInput) { input.TournamentID = long }},
			{name: "entity kind", mutate: func(input *observability.TournamentEventInput) { input.EntityKind = long }},
			{name: "entity id", mutate: func(input *observability.TournamentEventInput) { input.EntityID = long }},
			{name: "stage", mutate: func(input *observability.TournamentEventInput) { input.Stage = long }},
			{name: "transition", mutate: func(input *observability.TournamentEventInput) { input.Transition = long }},
			{name: "reason", mutate: func(input *observability.TournamentEventInput) { input.ReasonCode = long }},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				input := validTournamentEventInput()
				test.mutate(&input)

				_, err := observability.NewTournamentEvent(input)

				require.Error(t, err)
			})
		}
	})

	t.Run("rejects control characters in every string field", func(t *testing.T) {
		tests := []struct {
			name   string
			mutate func(*observability.TournamentEventInput)
		}{
			{name: "event", mutate: func(input *observability.TournamentEventInput) { input.Event += "\nforged" }},
			{name: "outcome", mutate: func(input *observability.TournamentEventInput) { input.Outcome = "success\rforged" }},
			{name: "correlation", mutate: func(input *observability.TournamentEventInput) { input.CorrelationID += "\tforged" }},
			{name: "command", mutate: func(input *observability.TournamentEventInput) { input.CommandID = "command\x00forged" }},
			{name: "tournament", mutate: func(input *observability.TournamentEventInput) { input.TournamentID += "\nforged" }},
			{name: "entity kind", mutate: func(input *observability.TournamentEventInput) { input.EntityKind += "\nforged" }},
			{name: "entity id", mutate: func(input *observability.TournamentEventInput) { input.EntityID += "\nforged" }},
			{name: "stage", mutate: func(input *observability.TournamentEventInput) { input.Stage += "\nforged" }},
			{name: "transition", mutate: func(input *observability.TournamentEventInput) { input.Transition += "\nforged" }},
			{name: "reason", mutate: func(input *observability.TournamentEventInput) { input.ReasonCode += "\nforged" }},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				input := validTournamentEventInput()
				test.mutate(&input)

				_, err := observability.NewTournamentEvent(input)

				require.Error(t, err)
			})
		}
	})

	t.Run("rejects secret-bearing concepts", func(t *testing.T) {
		for _, canary := range []string{
			"flag_sentinel",
			"flagSentinel",
			"access_token",
			"authToken",
			"session-cookie",
			"Authorization",
			"PASSWORD",
			"task_payload",
			"task-content",
			"task payload",
			"credentials",
		} {
			input := validTournamentEventInput()
			input.Transition = canary

			_, err := observability.NewTournamentEvent(input)

			require.Error(t, err, canary)
		}
	})

	t.Run("requires a bounded reason code", func(t *testing.T) {
		input := validTournamentEventInput()
		input.ReasonCode = "not a code"

		_, err := observability.NewTournamentEvent(input)

		require.Error(t, err)
	})

	t.Run("emits validated events through the first observer", func(t *testing.T) {
		first, firstEvents := newTournamentEventCapture(t)
		second, secondEvents := newTournamentEventCapture(t)
		observer := observability.FirstTournamentEventObserver(nil, first, second)

		err := observability.EmitTournamentEvent(t.Context(), observer, validTournamentEventInput())

		require.NoError(t, err)
		require.Len(t, firstEvents(), 1)
		require.Empty(t, secondEvents())
		require.Equal(t, "corr-123", firstEvents()[0].CorrelationID)
	})

	t.Run("fans out validated events to every observer", func(t *testing.T) {
		first, firstEvents := newTournamentEventCapture(t)
		second, secondEvents := newTournamentEventCapture(t)
		observer := observability.NewTournamentEventFanout(nil, first, second)

		err := observability.EmitTournamentEvent(t.Context(), observer, validTournamentEventInput())

		require.NoError(t, err)
		require.Len(t, firstEvents(), 1)
		require.Len(t, secondEvents(), 1)
		require.Equal(t, firstEvents(), secondEvents())
	})

	t.Run("isolates one observer panic from the remaining fanout", func(t *testing.T) {
		panicking := observabilitymocks.NewMockTournamentEventObserver(t)
		panicking.EXPECT().ObserveTournamentEvent(mock.Anything, mock.Anything).
			Run(func(context.Context, observability.TournamentEvent) { panic("observer failed") }).Once()
		capture, events := newTournamentEventCapture(t)
		observer := observability.NewTournamentEventFanout(panicking, capture)

		require.NotPanics(t, func() {
			require.NoError(t, observability.EmitTournamentEvent(t.Context(), observer, validTournamentEventInput()))
		})
		require.Len(t, events(), 1)
	})

	t.Run("does not observe invalid input", func(t *testing.T) {
		capture, events := newTournamentEventCapture(t)
		input := validTournamentEventInput()
		input.ReasonCode = ""

		err := observability.EmitTournamentEvent(t.Context(), capture, input)

		require.Error(t, err)
		require.Empty(t, events())
	})

	t.Run("nil observers are safe", func(t *testing.T) {
		require.Nil(t, observability.FirstTournamentEventObserver(nil, nil))
		require.NoError(t, observability.EmitTournamentEvent(t.Context(), nil, validTournamentEventInput()))
		require.NotPanics(t, func() {
			observer := observability.NewTournamentStructuredLogger(nil)
			if observer != nil {
				observer.ObserveTournamentEvent(t.Context(), observability.TournamentEvent{})
			}
		})
	})

	for _, test := range []struct {
		name    string
		outcome string
		level   string
	}{
		{name: "success is info", outcome: observability.TournamentOutcomeSuccess, level: "info"},
		{name: "retry is warning", outcome: observability.TournamentOutcomeRetry, level: "warn"},
		{name: "rejection is warning", outcome: observability.TournamentOutcomeRejected, level: "warn"},
		{name: "failure is error", outcome: observability.TournamentOutcomeFailure, level: "error"},
	} {
		t.Run(test.name, func(t *testing.T) {
			entry := emitTournamentLogEntry(t, test.outcome, "")

			require.Equal(t, test.level, entry["level"])
			require.Equal(t, "tournament event", entry["message"])
			require.Equal(t, "tournament.transition", entry["event"])
			require.Equal(t, test.outcome, entry["outcome"])
			require.Equal(t, "corr-123", entry["correlation_id"])
			require.Equal(t, "tournament-123", entry["tournament_id"])
			require.Equal(t, "wave", entry["entity_kind"])
			require.Equal(t, "wave-123", entry["entity_id"])
			require.Equal(t, "qualifier", entry["stage"])
			require.Equal(t, "scheduled_to_active", entry["transition"])
			require.InDelta(t, float64(1500), entry["duration_ms"], 0)
			require.Equal(t, "completed", entry["reason_code"])
			require.InDelta(t, float64(7), entry["revision"], 0)
			require.Equal(t, "request-123", entry["request_id"])
			require.NotContains(t, entry, "command_id")
			require.NotContains(t, entry, "error")
			require.NotContains(t, entry, "fields")
		})
	}

	t.Run("emits optional command identity", func(t *testing.T) {
		entry := emitTournamentLogEntry(t, observability.TournamentOutcomeSuccess, "command-123")

		require.Equal(t, "command-123", entry["command_id"])
	})
}

func validTournamentEventInput() observability.TournamentEventInput {
	return observability.TournamentEventInput{
		Event:         "tournament.transition",
		Outcome:       observability.TournamentOutcomeSuccess,
		CorrelationID: "corr-123",
		TournamentID:  "tournament-123",
		EntityKind:    "wave",
		EntityID:      "wave-123",
		Stage:         "qualifier",
		Transition:    "scheduled_to_active",
		Duration:      1500 * time.Millisecond,
		ReasonCode:    "completed",
		Revision:      7,
	}
}

func newTournamentEventCapture(
	t *testing.T,
) (*observabilitymocks.MockTournamentEventObserver, func() []observability.TournamentEvent) {
	t.Helper()
	var events []observability.TournamentEvent
	observer := observabilitymocks.NewMockTournamentEventObserver(t)
	observer.EXPECT().
		ObserveTournamentEvent(mock.Anything, mock.Anything).
		Run(func(_ context.Context, event observability.TournamentEvent) {
			events = append(events, event)
		}).
		Maybe()
	return observer, func() []observability.TournamentEvent {
		return append([]observability.TournamentEvent(nil), events...)
	}
}

type tournamentLogContextKey struct{}

func emitTournamentLogEntry(t *testing.T, outcome, commandID string) map[string]any {
	t.Helper()

	var output bytes.Buffer
	logger, err := logkit.New(
		logkit.WithLevel(logkit.DebugLevel),
		logkit.WithSyncWriter(&output),
		logkit.WithContextExtractor(func(ctx context.Context) logkit.Fields {
			requestID, _ := ctx.Value(tournamentLogContextKey{}).(string)
			return logkit.Fields{"request_id": requestID}
		}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, logger.Close()) })

	input := validTournamentEventInput()
	input.Outcome = outcome
	input.CommandID = commandID
	ctx := context.WithValue(t.Context(), tournamentLogContextKey{}, "request-123")
	require.NoError(t, observability.EmitTournamentEvent(
		ctx,
		observability.NewTournamentStructuredLogger(logger),
		input,
	))

	var entry map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(output.Bytes()), &entry))
	return entry
}
