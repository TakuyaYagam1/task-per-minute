package arena

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestArenaParticipantRealtime(t *testing.T) {
	tournamentID := testUUID("10000000-0000-4000-8000-000000000001")
	otherTournamentID := testUUID("20000000-0000-4000-8000-000000000001")
	playerID := testUUID("30000000-0000-4000-8000-000000000001")
	otherPlayerID := testUUID("30000000-0000-4000-8000-000000000002")
	principal := ParticipantRealtimePrincipal{
		Authenticated: true,
		TournamentID:  tournamentID,
		PlayerID:      playerID,
	}
	request := ParticipantRealtimeRequest{Principal: principal, TournamentID: tournamentID}

	t.Run("authentication is required before reading", func(t *testing.T) {
		unauthenticated := request
		unauthenticated.Principal.Authenticated = false
		source := participantRealtimeSourceFunc(func(context.Context, ParticipantRealtimeReadQuery) (ParticipantRealtimeReadModel, error) {
			t.Fatal("source called for an unauthenticated principal")
			return ParticipantRealtimeReadModel{}, nil
		})

		_, err := ParticipantRealtimeView(context.Background(), source, unauthenticated)
		if !errors.Is(err, ErrParticipantRealtimeUnauthenticated) {
			t.Fatalf("ParticipantRealtimeView() error = %v", err)
		}
	})

	t.Run("request tournament must match the authenticated scope", func(t *testing.T) {
		wrongTournament := request
		wrongTournament.TournamentID = otherTournamentID
		source := participantRealtimeSourceFunc(func(context.Context, ParticipantRealtimeReadQuery) (ParticipantRealtimeReadModel, error) {
			t.Fatal("source called for a cross-tournament request")
			return ParticipantRealtimeReadModel{}, nil
		})

		_, err := ParticipantRealtimeView(context.Background(), source, wrongTournament)
		if !errors.Is(err, ErrParticipantRealtimeTournamentScope) {
			t.Fatalf("ParticipantRealtimeView() error = %v", err)
		}
	})

	t.Run("authoritative player must match the principal", func(t *testing.T) {
		state := participantRealtimeState(t, tournamentID, otherPlayerID)
		_, err := ParticipantRealtimeView(context.Background(), participantRealtimeStaticSource(state), request)
		if !errors.Is(err, ErrParticipantRealtimePlayerScope) {
			t.Fatalf("ParticipantRealtimeView() error = %v", err)
		}
	})

	t.Run("public and operator events are rejected", func(t *testing.T) {
		roleEvents := []struct {
			name     string
			envelope RealtimeEnvelope
		}{
			{name: "public", envelope: participantRealtimePublicEnvelope(t, tournamentID)},
			{name: "operator", envelope: participantRealtimeOperatorEnvelope(t, tournamentID)},
		}
		for _, roleEvent := range roleEvents {
			t.Run(roleEvent.name, func(t *testing.T) {
				state := participantRealtimeState(t, tournamentID, playerID)
				state.Events = []RealtimeEnvelope{roleEvent.envelope}
				_, err := ParticipantRealtimeView(context.Background(), participantRealtimeStaticSource(state), request)
				if !errors.Is(err, ErrParticipantRealtimeRolePayload) {
					t.Fatalf("ParticipantRealtimeView() error = %v", err)
				}
			})
		}
	})

	t.Run("first connection returns one participant snapshot", func(t *testing.T) {
		state := participantRealtimeState(t, tournamentID, playerID)
		result, err := ParticipantRealtimeView(context.Background(), participantRealtimeStaticSource(state), request)
		if err != nil {
			t.Fatalf("ParticipantRealtimeView() error = %v", err)
		}
		if !result.UsesSnapshot || len(result.Envelopes) != 1 {
			t.Fatalf("result = snapshot %t, sequences %v", result.UsesSnapshot, participantRealtimeSequences(result.Envelopes))
		}
		if got := result.Envelopes[0].Participant.PlayerID; got != playerID {
			t.Fatalf("participant player ID = %s, want %s", got, playerID)
		}

		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		requireJSONKeys(t, encoded, "uses_snapshot", "envelopes")
		requireNoSecretNames(t, encoded)
		for _, forbidden := range []string{`"public"`, `"operator"`, `"command"`, `"mutation"`, `"method"`} {
			if bytes.Contains(bytes.ToLower(encoded), []byte(forbidden)) {
				t.Fatalf("participant JSON contains %s: %s", forbidden, encoded)
			}
		}
	})

	t.Run("valid cursor resumes ordered events with a bounded read", func(t *testing.T) {
		state := participantRealtimeState(t, tournamentID, playerID)
		cursorRequest := request
		cursorRequest.Cursor = &RealtimeCursor{
			SchemaVersion:      ArenaRealtimeSchemaVersion,
			TournamentID:       tournamentID,
			LastSequence:       10,
			ProjectionRevision: 10,
		}
		var query ParticipantRealtimeReadQuery
		source := participantRealtimeSourceFunc(func(_ context.Context, got ParticipantRealtimeReadQuery) (ParticipantRealtimeReadModel, error) {
			query = got
			return state, nil
		})

		result, err := ParticipantRealtimeView(context.Background(), source, cursorRequest)
		if err != nil {
			t.Fatalf("ParticipantRealtimeView() error = %v", err)
		}
		if result.UsesSnapshot || fmt.Sprint(participantRealtimeSequences(result.Envelopes)) != "[11 14]" {
			t.Fatalf("result = snapshot %t, sequences %v", result.UsesSnapshot, participantRealtimeSequences(result.Envelopes))
		}
		if query.TournamentID != tournamentID || query.PlayerID != playerID || query.MaxEvents != ParticipantRealtimeMaxReplayEvents {
			t.Fatalf("read query = %#v", query)
		}
		for _, envelope := range result.Envelopes {
			if envelope.Participant.PlayerID != playerID {
				t.Fatalf("event %d player ID = %s", envelope.Sequence, envelope.Participant.PlayerID)
			}
		}

		result.Envelopes[0].Participant.Assignment.Task.Title = "changed result"
		if state.Events[0].Participant.Assignment.Task.Title == "changed result" {
			t.Fatal("returned event aliases the source event")
		}
		state.Events[1].Participant.Assignment.Task.Title = "changed source"
		if result.Envelopes[1].Participant.Assignment.Task.Title == "changed source" {
			t.Fatal("source event aliases the returned event")
		}
	})

	t.Run("stale cursor falls back to exactly one snapshot copy", func(t *testing.T) {
		state := participantRealtimeState(t, tournamentID, playerID)
		cursorRequest := request
		cursorRequest.Cursor = &RealtimeCursor{
			SchemaVersion:      ArenaRealtimeSchemaVersion,
			TournamentID:       tournamentID,
			LastSequence:       9,
			ProjectionRevision: 9,
		}

		result, err := ParticipantRealtimeView(context.Background(), participantRealtimeStaticSource(state), cursorRequest)
		if err != nil {
			t.Fatalf("ParticipantRealtimeView() error = %v", err)
		}
		if !result.UsesSnapshot || len(result.Envelopes) != 1 || result.Envelopes[0].Sequence != 14 {
			t.Fatalf("result = snapshot %t, sequences %v", result.UsesSnapshot, participantRealtimeSequences(result.Envelopes))
		}
		result.Envelopes[0].Participant.Assignment.Task.Title = "changed result"
		if state.Snapshot.Payload.Assignment.Task.Title == "changed result" {
			t.Fatal("returned snapshot aliases the authoritative snapshot")
		}
	})

	t.Run("malformed cursor returns a sanitized cursor error", func(t *testing.T) {
		cursorRequest := request
		cursorRequest.Cursor = &RealtimeCursor{TournamentID: otherTournamentID, LastSequence: -1}
		_, err := ParticipantRealtimeView(context.Background(), participantRealtimeStaticSource(participantRealtimeState(t, tournamentID, playerID)), cursorRequest)
		if !errors.Is(err, ErrParticipantRealtimeCursor) {
			t.Fatalf("ParticipantRealtimeView() error = %v", err)
		}
	})

	t.Run("replay event count is bounded", func(t *testing.T) {
		state := participantRealtimeState(t, tournamentID, playerID)
		state.Events = make([]RealtimeEnvelope, ParticipantRealtimeMaxReplayEvents+1)
		_, err := ParticipantRealtimeView(context.Background(), participantRealtimeStaticSource(state), request)
		if !errors.Is(err, ErrParticipantRealtimeEventLimit) {
			t.Fatalf("ParticipantRealtimeView() error = %v", err)
		}
	})

	t.Run("source details are not exposed", func(t *testing.T) {
		source := participantRealtimeSourceFunc(func(context.Context, ParticipantRealtimeReadQuery) (ParticipantRealtimeReadModel, error) {
			return ParticipantRealtimeReadModel{}, errors.New("internal shard detail")
		})
		_, err := ParticipantRealtimeView(context.Background(), source, request)
		if !errors.Is(err, ErrParticipantRealtimeUnavailable) {
			t.Fatalf("ParticipantRealtimeView() error = %v", err)
		}
	})
}

type participantRealtimeSourceFunc func(context.Context, ParticipantRealtimeReadQuery) (ParticipantRealtimeReadModel, error)

func (read participantRealtimeSourceFunc) ReadParticipantRealtime(ctx context.Context, query ParticipantRealtimeReadQuery) (ParticipantRealtimeReadModel, error) {
	return read(ctx, query)
}

func participantRealtimeStaticSource(state ParticipantRealtimeReadModel) ParticipantRealtimeReadSource {
	return participantRealtimeSourceFunc(func(context.Context, ParticipantRealtimeReadQuery) (ParticipantRealtimeReadModel, error) {
		return state, nil
	})
}

func participantRealtimeState(t *testing.T, tournamentID, playerID uuid.UUID) ParticipantRealtimeReadModel {
	t.Helper()
	event11 := resumeEnvelope(t, tournamentID, playerID, 11, 11)
	event14 := resumeEnvelope(t, tournamentID, playerID, 14, 14)
	snapshot := resumeEnvelope(t, tournamentID, playerID, 14, 15)
	return ParticipantRealtimeReadModel{
		Snapshot: ParticipantRealtimeSnapshot{
			Metadata: participantRealtimeMetadata(snapshot),
			Payload:  snapshot.Participant.clone(),
		},
		Available: RealtimeAvailableRange{
			OldestSequence:            10,
			LatestSequence:            14,
			CurrentProjectionRevision: 15,
		},
		Events: []RealtimeEnvelope{event11, event14},
	}
}

func participantRealtimeMetadata(envelope RealtimeEnvelope) RealtimeEnvelopeMetadata {
	return RealtimeEnvelopeMetadata{
		SchemaVersion:      envelope.SchemaVersion,
		TournamentID:       envelope.TournamentID,
		Sequence:           envelope.Sequence,
		EventID:            envelope.EventID,
		OccurredAt:         envelope.OccurredAt,
		ProjectionRevision: envelope.ProjectionRevision,
	}
}

func participantRealtimePublicEnvelope(t *testing.T, tournamentID uuid.UUID) RealtimeEnvelope {
	t.Helper()
	envelope, err := NewRealtimeEnvelope(RealtimeEnvelopeMetadata{
		SchemaVersion:      ArenaRealtimeSchemaVersion,
		TournamentID:       tournamentID,
		Sequence:           12,
		EventID:            testUUID("40000000-0000-4000-8000-000000000001"),
		OccurredAt:         time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC),
		ProjectionRevision: 9,
	}, testPublicSnapshot(t, tournamentID))
	if err != nil {
		t.Fatalf("NewRealtimeEnvelope() error = %v", err)
	}
	return envelope
}

func participantRealtimeOperatorEnvelope(t *testing.T, tournamentID uuid.UUID) RealtimeEnvelope {
	t.Helper()
	operatorID := testUUID("50000000-0000-4000-8000-000000000001")
	snapshot, err := NewOperatorSnapshot(
		OperatorSnapshotAccess{Authenticated: true, TournamentID: tournamentID, OperatorID: operatorID},
		testOperatorSnapshotInput(tournamentID),
	)
	if err != nil {
		t.Fatalf("NewOperatorSnapshot() error = %v", err)
	}
	envelope, err := NewRealtimeEnvelope(RealtimeEnvelopeMetadata{
		SchemaVersion:      ArenaRealtimeSchemaVersion,
		TournamentID:       tournamentID,
		Sequence:           12,
		EventID:            testUUID("60000000-0000-4000-8000-000000000001"),
		OccurredAt:         time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC),
		ProjectionRevision: 9,
	}, snapshot)
	if err != nil {
		t.Fatalf("NewRealtimeEnvelope() error = %v", err)
	}
	return envelope
}

func participantRealtimeSequences(envelopes []ParticipantRealtimeEnvelope) []int64 {
	sequences := make([]int64, len(envelopes))
	for index := range envelopes {
		sequences[index] = envelopes[index].Sequence
	}
	return sequences
}
