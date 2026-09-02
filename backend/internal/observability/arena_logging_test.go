package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	logkit "github.com/wahrwelt-kit/go-logkit"
)

func TestArenaStructuredLogging(t *testing.T) {
	t.Run("builds a canonical event", func(t *testing.T) {
		input := validArenaEventInput()

		event, err := NewArenaEvent(input)

		require.NoError(t, err)
		require.Equal(t, ArenaEvent{
			Event:         "arena.transition",
			Outcome:       ArenaOutcomeSuccess,
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
		input := validArenaEventInput()
		input.Event = "arena.command.accepted"

		_, err := NewArenaEvent(input)

		require.Error(t, err)

		input.CommandID = "command-123"
		event, err := NewArenaEvent(input)
		require.NoError(t, err)
		require.Equal(t, "command-123", event.CommandID)
	})

	t.Run("rejects missing required fields", func(t *testing.T) {
		tests := []struct {
			name   string
			mutate func(*ArenaEventInput)
		}{
			{name: "event", mutate: func(input *ArenaEventInput) { input.Event = "" }},
			{name: "outcome", mutate: func(input *ArenaEventInput) { input.Outcome = "" }},
			{name: "correlation", mutate: func(input *ArenaEventInput) { input.CorrelationID = "" }},
			{name: "tournament", mutate: func(input *ArenaEventInput) { input.TournamentID = "" }},
			{name: "entity kind", mutate: func(input *ArenaEventInput) { input.EntityKind = "" }},
			{name: "entity id", mutate: func(input *ArenaEventInput) { input.EntityID = "" }},
			{name: "stage", mutate: func(input *ArenaEventInput) { input.Stage = "" }},
			{name: "transition", mutate: func(input *ArenaEventInput) { input.Transition = "" }},
			{name: "reason", mutate: func(input *ArenaEventInput) { input.ReasonCode = "" }},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				input := validArenaEventInput()
				test.mutate(&input)

				_, err := NewArenaEvent(input)

				require.Error(t, err)
			})
		}
	})

	t.Run("accepts only canonical outcomes", func(t *testing.T) {
		for _, outcome := range []string{
			ArenaOutcomeSuccess,
			ArenaOutcomeRetry,
			ArenaOutcomeRejected,
			ArenaOutcomeFailure,
		} {
			input := validArenaEventInput()
			input.Outcome = outcome
			_, err := NewArenaEvent(input)
			require.NoError(t, err, outcome)
		}

		input := validArenaEventInput()
		input.Outcome = "unknown"
		_, err := NewArenaEvent(input)
		require.Error(t, err)
	})

	t.Run("rejects negative scalar fields", func(t *testing.T) {
		input := validArenaEventInput()
		input.Duration = -time.Nanosecond
		_, err := NewArenaEvent(input)
		require.Error(t, err)

		input = validArenaEventInput()
		input.Revision = -1
		_, err = NewArenaEvent(input)
		require.Error(t, err)
	})

	t.Run("bounds every string field", func(t *testing.T) {
		long := strings.Repeat("a", 257)
		tests := []struct {
			name   string
			mutate func(*ArenaEventInput)
		}{
			{name: "event", mutate: func(input *ArenaEventInput) { input.Event = long }},
			{name: "correlation", mutate: func(input *ArenaEventInput) { input.CorrelationID = long }},
			{name: "command", mutate: func(input *ArenaEventInput) { input.CommandID = long }},
			{name: "tournament", mutate: func(input *ArenaEventInput) { input.TournamentID = long }},
			{name: "entity kind", mutate: func(input *ArenaEventInput) { input.EntityKind = long }},
			{name: "entity id", mutate: func(input *ArenaEventInput) { input.EntityID = long }},
			{name: "stage", mutate: func(input *ArenaEventInput) { input.Stage = long }},
			{name: "transition", mutate: func(input *ArenaEventInput) { input.Transition = long }},
			{name: "reason", mutate: func(input *ArenaEventInput) { input.ReasonCode = long }},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				input := validArenaEventInput()
				test.mutate(&input)

				_, err := NewArenaEvent(input)

				require.Error(t, err)
			})
		}
	})

	t.Run("rejects control characters in every string field", func(t *testing.T) {
		tests := []struct {
			name   string
			mutate func(*ArenaEventInput)
		}{
			{name: "event", mutate: func(input *ArenaEventInput) { input.Event += "\nforged" }},
			{name: "outcome", mutate: func(input *ArenaEventInput) { input.Outcome = "success\rforged" }},
			{name: "correlation", mutate: func(input *ArenaEventInput) { input.CorrelationID += "\tforged" }},
			{name: "command", mutate: func(input *ArenaEventInput) { input.CommandID = "command\x00forged" }},
			{name: "tournament", mutate: func(input *ArenaEventInput) { input.TournamentID += "\nforged" }},
			{name: "entity kind", mutate: func(input *ArenaEventInput) { input.EntityKind += "\nforged" }},
			{name: "entity id", mutate: func(input *ArenaEventInput) { input.EntityID += "\nforged" }},
			{name: "stage", mutate: func(input *ArenaEventInput) { input.Stage += "\nforged" }},
			{name: "transition", mutate: func(input *ArenaEventInput) { input.Transition += "\nforged" }},
			{name: "reason", mutate: func(input *ArenaEventInput) { input.ReasonCode += "\nforged" }},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				input := validArenaEventInput()
				test.mutate(&input)

				_, err := NewArenaEvent(input)

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
			input := validArenaEventInput()
			input.Transition = canary

			_, err := NewArenaEvent(input)

			require.Error(t, err, canary)
		}
	})

	t.Run("requires a bounded reason code", func(t *testing.T) {
		input := validArenaEventInput()
		input.ReasonCode = "not a code"

		_, err := NewArenaEvent(input)

		require.Error(t, err)
	})

	t.Run("emits validated events through the first observer", func(t *testing.T) {
		first := &arenaEventCapture{}
		second := &arenaEventCapture{}
		observer := FirstArenaEventObserver(nil, first, second)

		err := EmitArenaEvent(t.Context(), observer, validArenaEventInput())

		require.NoError(t, err)
		require.Len(t, first.events, 1)
		require.Empty(t, second.events)
		require.Equal(t, "corr-123", first.events[0].CorrelationID)
	})

	t.Run("fans out validated events to every observer", func(t *testing.T) {
		first := &arenaEventCapture{}
		second := &arenaEventCapture{}
		observer := NewArenaEventFanout(nil, first, second)

		err := EmitArenaEvent(t.Context(), observer, validArenaEventInput())

		require.NoError(t, err)
		require.Len(t, first.events, 1)
		require.Len(t, second.events, 1)
		require.Equal(t, first.events, second.events)
	})

	t.Run("does not observe invalid input", func(t *testing.T) {
		capture := &arenaEventCapture{}
		input := validArenaEventInput()
		input.ReasonCode = ""

		err := EmitArenaEvent(t.Context(), capture, input)

		require.Error(t, err)
		require.Empty(t, capture.events)
	})

	t.Run("nil observers are safe", func(t *testing.T) {
		require.Nil(t, FirstArenaEventObserver(nil, nil))
		require.NoError(t, EmitArenaEvent(t.Context(), nil, validArenaEventInput()))
		require.NotPanics(t, func() {
			observer := NewArenaStructuredLogger(nil)
			if observer != nil {
				observer.ObserveArenaEvent(t.Context(), ArenaEvent{})
			}
		})
	})

	for _, test := range []struct {
		name    string
		outcome string
		level   string
	}{
		{name: "success is info", outcome: ArenaOutcomeSuccess, level: "info"},
		{name: "retry is warning", outcome: ArenaOutcomeRetry, level: "warn"},
		{name: "rejection is warning", outcome: ArenaOutcomeRejected, level: "warn"},
		{name: "failure is error", outcome: ArenaOutcomeFailure, level: "error"},
	} {
		t.Run(test.name, func(t *testing.T) {
			entry := emitArenaLogEntry(t, test.outcome, "")

			require.Equal(t, test.level, entry["level"])
			require.Equal(t, "arena event", entry["message"])
			require.Equal(t, "arena.transition", entry["event"])
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
		entry := emitArenaLogEntry(t, ArenaOutcomeSuccess, "command-123")

		require.Equal(t, "command-123", entry["command_id"])
	})
}

func validArenaEventInput() ArenaEventInput {
	return ArenaEventInput{
		Event:         "arena.transition",
		Outcome:       ArenaOutcomeSuccess,
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

type arenaEventCapture struct {
	events []ArenaEvent
}

func (c *arenaEventCapture) ObserveArenaEvent(_ context.Context, event ArenaEvent) {
	c.events = append(c.events, event)
}

type arenaLogContextKey struct{}

func emitArenaLogEntry(t *testing.T, outcome, commandID string) map[string]any {
	t.Helper()

	var output bytes.Buffer
	logger, err := logkit.New(
		logkit.WithLevel(logkit.DebugLevel),
		logkit.WithSyncWriter(&output),
		logkit.WithContextExtractor(func(ctx context.Context) logkit.Fields {
			requestID, _ := ctx.Value(arenaLogContextKey{}).(string)
			return logkit.Fields{"request_id": requestID}
		}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, logger.Close()) })

	input := validArenaEventInput()
	input.Outcome = outcome
	input.CommandID = commandID
	ctx := context.WithValue(t.Context(), arenaLogContextKey{}, "request-123")
	require.NoError(t, EmitArenaEvent(ctx, NewArenaStructuredLogger(logger), input))

	var entry map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(output.Bytes()), &entry))
	return entry
}
