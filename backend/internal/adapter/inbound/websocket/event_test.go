package websocket

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	arenaws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/arena"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestArenaRootProtocol(t *testing.T) {
	t.Parallel()

	t.Run("preserves Duel wire bytes", func(t *testing.T) {
		encoded, err := marshalEvent(EventQueueJoined, map[string]int{"position": 2})
		require.NoError(t, err)
		require.Equal(t, `{"type":"queue_joined","payload":{"position":2}}`, string(encoded)) //nolint:testifylint // Exact Duel bytes are the compatibility contract.

		encoded, err = marshalError(ErrorUnknownEvent, "unknown event")
		require.NoError(t, err)
		require.Equal(t, `{"type":"error","code":"unknown_event","message":"unknown event"}`, string(encoded)) //nolint:testifylint // Exact Duel bytes are the compatibility contract.
	})

	tournamentID := uuid.MustParse("71000000-0000-4000-8000-000000000001")
	participantID := uuid.MustParse("71000000-0000-4000-8000-000000000002")
	cancellationID := uuid.MustParse("71000000-0000-4000-8000-000000000003")

	t.Run("keeps active payloads role specific", func(t *testing.T) {
		participant, err := MarshalArenaParticipant(ArenaParticipantPayload{
			UsesSnapshot: true,
			Envelopes:    []arenaws.ParticipantRealtimeEnvelope{},
		})
		require.NoError(t, err)
		require.Contains(t, string(participant), `"type":"arena_participant"`)
		decodedParticipant, err := DecodeArenaParticipantMessage(participant)
		require.NoError(t, err)
		require.NotNil(t, decodedParticipant.Participant)
		require.ErrorIs(t, decodeArenaPublicOnly(participant), ErrArenaRoleMismatch)
		require.ErrorIs(t, decodeArenaOperatorOnly(participant), ErrArenaRoleMismatch)

		public, err := MarshalArenaPublic(ArenaPublicPayload{
			UsesSnapshot: true,
			Envelopes:    []ArenaPublicEnvelope{},
		})
		require.NoError(t, err)
		require.Contains(t, string(public), `"type":"arena_public"`)
		decodedPublic, err := DecodeArenaPublicMessage(public)
		require.NoError(t, err)
		require.NotNil(t, decodedPublic.Public)
		require.ErrorIs(t, decodeArenaParticipantOnly(public), ErrArenaRoleMismatch)
		require.ErrorIs(t, decodeArenaOperatorOnly(public), ErrArenaRoleMismatch)

		operator, err := MarshalArenaOperator(ArenaOperatorPayload{
			UsesSnapshot: true,
			Envelopes:    []ArenaOperatorEnvelope{},
		})
		require.NoError(t, err)
		require.Contains(t, string(operator), `"type":"arena_operator"`)
		decodedOperator, err := DecodeArenaOperatorMessage(operator)
		require.NoError(t, err)
		require.NotNil(t, decodedOperator.Operator)
		require.ErrorIs(t, decodeArenaParticipantOnly(operator), ErrArenaRoleMismatch)
		require.ErrorIs(t, decodeArenaPublicOnly(operator), ErrArenaRoleMismatch)
	})

	t.Run("closes each role with its own terminal payload", func(t *testing.T) {
		participant, err := MarshalArenaParticipantTerminal(ArenaParticipantTerminalPayload{
			TournamentID: tournamentID, ParticipantID: participantID, State: "cancelled",
		})
		require.NoError(t, err)
		decodedParticipant, err := DecodeArenaParticipantMessage(participant)
		require.NoError(t, err)
		require.Equal(t, participantID, decodedParticipant.Terminal.ParticipantID)
		require.ErrorIs(t, decodeArenaPublicOnly(participant), ErrArenaRoleMismatch)

		public, err := MarshalArenaPublicTerminal(ArenaPublicTerminalPayload{
			TournamentID: tournamentID, State: "cancelled",
		})
		require.NoError(t, err)
		decodedPublic, err := DecodeArenaPublicMessage(public)
		require.NoError(t, err)
		require.Equal(t, tournamentID, decodedPublic.Terminal.TournamentID)
		require.ErrorIs(t, decodeArenaOperatorOnly(public), ErrArenaRoleMismatch)

		operator, err := MarshalArenaOperatorTerminal(ArenaOperatorTerminalPayload{
			TournamentID: tournamentID, CancellationID: cancellationID, State: "cancelled", Reason: "approved by operator",
		})
		require.NoError(t, err)
		decodedOperator, err := DecodeArenaOperatorMessage(operator)
		require.NoError(t, err)
		require.Equal(t, "approved by operator", decodedOperator.Terminal.Reason)
		require.ErrorIs(t, decodeArenaParticipantOnly(operator), ErrArenaRoleMismatch)
		for _, encoded := range [][]byte{participant, public} {
			require.False(t, bytes.Contains(encoded, []byte("approved by operator")))
		}
	})

	t.Run("accepts only typed read-side commands", func(t *testing.T) {
		cursor := arenaws.RealtimeCursor{
			SchemaVersion:      arenaws.ArenaRealtimeSchemaVersion,
			TournamentID:       tournamentID,
			LastSequence:       12,
			ProjectionRevision: 9,
		}
		for _, role := range []ArenaRole{ArenaRoleParticipant, ArenaRolePublic, ArenaRoleOperator} {
			connectBody, err := json.Marshal(IncomingEvent{
				Type: EventArenaConnect,
				Payload: mustJSON(t, ArenaConnectPayload{
					Role: role, TournamentID: tournamentID,
				}),
			})
			require.NoError(t, err)
			connect, err := DecodeArenaCommand(role, connectBody)
			require.NoError(t, err)
			require.Equal(t, tournamentID, connect.Connect.TournamentID)
			otherRole := ArenaRoleParticipant
			if role == otherRole {
				otherRole = ArenaRolePublic
			}
			_, err = DecodeArenaCommand(otherRole, connectBody)
			require.ErrorIs(t, err, ErrArenaRoleMismatch)

			body, err := json.Marshal(IncomingEvent{
				Type: EventArenaResume,
				Payload: mustJSON(t, ArenaResumePayload{
					Role: role, Cursor: cursor,
				}),
			})
			require.NoError(t, err)
			command, err := DecodeArenaCommand(role, body)
			require.NoError(t, err)
			require.Equal(t, cursor, command.Resume.Cursor)

			_, err = DecodeArenaCommand(otherRole, body)
			require.ErrorIs(t, err, ErrArenaRoleMismatch)
		}

		_, err := DecodeArenaCommand(ArenaRoleParticipant, []byte(`{"type":"arena_submit","payload":{"flag":"private"}}`))
		require.ErrorIs(t, err, ErrArenaCommandForbidden)
		rejection := ArenaRejectionFor(err)
		require.Equal(t, ArenaRejectionForbidden, rejection.Code)

		_, err = DecodeArenaCommand(ArenaRoleParticipant, []byte(`{"type":"not_an_arena_command"}`))
		require.ErrorIs(t, err, ErrArenaUnknownEvent)
		_, err = DecodeArenaCommand(ArenaRoleParticipant, []byte(`{"type":"arena_resume","payload":`))
		require.ErrorIs(t, err, ErrArenaInvalidJSON)
		_, err = DecodeArenaCommand(ArenaRoleParticipant, []byte(`{"type":"arena_resume","payload":{"role":"participant","cursor":{}}}`))
		require.ErrorIs(t, err, ErrArenaInvalidPayload)
	})

	t.Run("serializes typed rejections for every role", func(t *testing.T) {
		encoded, err := MarshalArenaRejected(ArenaRejectionFor(ErrArenaCommandForbidden))
		require.NoError(t, err)
		require.Equal(t, `{"type":"arena_rejected","code":"arena_forbidden","message":"Arena command is forbidden"}`, string(encoded)) //nolint:testifylint // Field placement is part of the root protocol.
		participant, err := DecodeArenaParticipantMessage(encoded)
		require.NoError(t, err)
		require.Equal(t, ArenaRejectionForbidden, participant.Rejected.Code)
		public, err := DecodeArenaPublicMessage(encoded)
		require.NoError(t, err)
		require.Equal(t, ArenaRejectionForbidden, public.Rejected.Code)
		operator, err := DecodeArenaOperatorMessage(encoded)
		require.NoError(t, err)
		require.Equal(t, ArenaRejectionForbidden, operator.Rejected.Code)
	})
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return encoded
}

func decodeArenaParticipantOnly(data []byte) error {
	_, err := DecodeArenaParticipantMessage(data)
	return err
}

func decodeArenaPublicOnly(data []byte) error {
	_, err := DecodeArenaPublicMessage(data)
	return err
}

func decodeArenaOperatorOnly(data []byte) error {
	_, err := DecodeArenaOperatorMessage(data)
	return err
}

func TestTaskAssignedPayloadPreservesSparseHintScheduleIndexes(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	duel := &domain.Duel{ID: uuid.New(), StartedAt: startedAt, Deadline: startedAt.Add(100 * time.Second)}
	task := websocketTestTask()
	task.TimeLimit = 100
	task.Hints = []string{"", "", "third"}

	payload := taskAssignedPayload(duel, task)

	require.Equal(t, []HintScheduleEntry{
		{HintIndex: 3, UnlockAt: startedAt.Add(75 * time.Second)},
	}, payload.Task.HintSchedule)
}

func TestTaskAssignedPayloadOmitsNoHintSchedule(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	duel := &domain.Duel{ID: uuid.New(), StartedAt: startedAt, Deadline: startedAt.Add(100 * time.Second)}
	task := websocketTestTask()
	task.Hints = []string{"", "", ""}

	payload := taskAssignedPayload(duel, task)

	require.Empty(t, payload.Task.HintSchedule)
}

func websocketTestTask() *domain.Task {
	return &domain.Task{
		ID:          uuid.New(),
		Title:       "task",
		Description: "description",
		Category:    domain.CategoryWeb,
		Difficulty:  domain.DifficultyEasy,
		TimeLimit:   60,
		Flag:        "FLAG{task}",
		Hints:       []string{"first", "second", "third"},
	}
}
