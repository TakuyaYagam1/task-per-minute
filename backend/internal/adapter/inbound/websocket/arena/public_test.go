package arena

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestArenaPublicRealtime(t *testing.T) {
	tournamentID := testUUID("11000000-0000-4000-8000-000000000001")
	otherTournamentID := testUUID("22000000-0000-4000-8000-000000000001")
	state := publicRealtimeTestState(t, tournamentID)

	t.Run("anonymous initial open returns one authoritative public snapshot", func(t *testing.T) {
		adapter := publicRealtimeTestAdapter(t, &publicRealtimeTestSource{state: state}, 2, 2)
		connection, err := adapter.PublicRealtimeOpen(context.Background(), PublicRealtimeOpenRequest{TournamentID: tournamentID})
		if err != nil {
			t.Fatalf("PublicRealtimeOpen() error = %v", err)
		}
		t.Cleanup(connection.PublicRealtimeClose)

		envelopes := connection.PublicRealtimeEnvelopes()
		if !connection.PublicRealtimeUsesSnapshot() || len(envelopes) != 1 {
			t.Fatalf("initial open = snapshot %t, sequences %v", connection.PublicRealtimeUsesSnapshot(), publicRealtimeTestSequences(envelopes))
		}
		if envelopes[0].EventID != state.SnapshotMetadata.EventID || envelopes[0].Public == nil {
			t.Fatalf("initial envelope = %#v", envelopes[0])
		}
		encoded, err := json.Marshal(envelopes)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		requireNoSecretNames(t, encoded)
		if bytes.Contains(encoded, []byte("private-sentinel")) {
			t.Fatalf("public JSON contains private sentinel: %s", encoded)
		}

		envelopes[0].Public.Scoreboard[0].DisplayName = "mutated output"
		state.Snapshot.Scoreboard[0].DisplayName = "mutated source"
		fresh := connection.PublicRealtimeEnvelopes()
		if fresh[0].Public.Scoreboard[0].DisplayName != "red" {
			t.Fatalf("connection output aliases mutable data: %q", fresh[0].Public.Scoreboard[0].DisplayName)
		}
	})

	t.Run("valid cursor resumes ordered public events", func(t *testing.T) {
		adapter := publicRealtimeTestAdapter(t, &publicRealtimeTestSource{state: state}, 2, 2)
		connection, err := adapter.PublicRealtimeOpen(context.Background(), PublicRealtimeOpenRequest{
			TournamentID: tournamentID,
			Cursor: &RealtimeCursor{
				SchemaVersion:      ArenaRealtimeSchemaVersion,
				TournamentID:       tournamentID,
				LastSequence:       12,
				ProjectionRevision: 12,
			},
		})
		if err != nil {
			t.Fatalf("PublicRealtimeOpen() error = %v", err)
		}
		t.Cleanup(connection.PublicRealtimeClose)
		if connection.PublicRealtimeUsesSnapshot() {
			t.Fatal("valid cursor unexpectedly used snapshot")
		}
		if got := publicRealtimeTestSequences(connection.PublicRealtimeEnvelopes()); fmt.Sprint(got) != "[13 14]" {
			t.Fatalf("resume sequences = %v, want [13 14]", got)
		}
	})

	t.Run("stale and wrong-scope cursors fall back to exactly one snapshot", func(t *testing.T) {
		for _, cursor := range []*RealtimeCursor{
			{SchemaVersion: ArenaRealtimeSchemaVersion, TournamentID: tournamentID, LastSequence: 11, ProjectionRevision: 11},
			{SchemaVersion: ArenaRealtimeSchemaVersion, TournamentID: otherTournamentID, LastSequence: 12, ProjectionRevision: 12},
		} {
			adapter := publicRealtimeTestAdapter(t, &publicRealtimeTestSource{state: state}, 2, 2)
			connection, err := adapter.PublicRealtimeOpen(context.Background(), PublicRealtimeOpenRequest{TournamentID: tournamentID, Cursor: cursor})
			if err != nil {
				t.Fatalf("PublicRealtimeOpen() error = %v", err)
			}
			if !connection.PublicRealtimeUsesSnapshot() || len(connection.PublicRealtimeEnvelopes()) != 1 {
				t.Fatalf("fallback = snapshot %t, sequences %v", connection.PublicRealtimeUsesSnapshot(), publicRealtimeTestSequences(connection.PublicRealtimeEnvelopes()))
			}
			connection.PublicRealtimeClose()
		}
	})

	t.Run("source cannot cross tournament scope", func(t *testing.T) {
		adapter := publicRealtimeTestAdapter(t, &publicRealtimeTestSource{state: state}, 1, 2)
		connection, err := adapter.PublicRealtimeOpen(context.Background(), PublicRealtimeOpenRequest{TournamentID: otherTournamentID})
		if !errors.Is(err, ErrPublicRealtimeInvalidScope) || connection != nil {
			t.Fatalf("PublicRealtimeOpen() = %#v, %v", connection, err)
		}
	})

	t.Run("participant and operator events are rejected", func(t *testing.T) {
		participant := testParticipantSnapshot(t, tournamentID)
		participant.Revision = 12
		participant.Assignment.Task.Title = "private-sentinel"
		participantEvent := publicRealtimeTestRoleEnvelope(t, tournamentID, 12, 12, participant)

		operator, err := NewOperatorSnapshot(
			OperatorSnapshotAccess{Authenticated: true, TournamentID: tournamentID, OperatorID: testUUID("33000000-0000-4000-8000-000000000001")},
			testOperatorSnapshotInput(tournamentID),
		)
		if err != nil {
			t.Fatal(err)
		}
		operator.Revision = 12
		operatorEvent := publicRealtimeTestRoleEnvelope(t, tournamentID, 12, 12, operator)

		for _, event := range []RealtimeEnvelope{participantEvent, operatorEvent} {
			candidate := state
			candidate.Events = []RealtimeEnvelope{event}
			adapter := publicRealtimeTestAdapter(t, &publicRealtimeTestSource{state: candidate}, 1, 2)
			connection, openErr := adapter.PublicRealtimeOpen(context.Background(), PublicRealtimeOpenRequest{TournamentID: tournamentID})
			if !errors.Is(openErr, ErrPublicRealtimeRolePayload) || connection != nil {
				t.Fatalf("PublicRealtimeOpen() = %#v, %v", connection, openErr)
			}
		}
	})

	t.Run("connection limit releases once and is reusable under races", func(t *testing.T) {
		adapter := publicRealtimeTestAdapter(t, &publicRealtimeTestSource{state: state}, 1, 2)
		first, err := adapter.PublicRealtimeOpen(context.Background(), PublicRealtimeOpenRequest{TournamentID: tournamentID})
		if err != nil {
			t.Fatal(err)
		}
		refused, err := adapter.PublicRealtimeOpen(context.Background(), PublicRealtimeOpenRequest{TournamentID: tournamentID})
		if !errors.Is(err, ErrPublicRealtimeConnectionLimit) || refused != nil {
			t.Fatalf("second open = %#v, %v", refused, err)
		}
		first.PublicRealtimeClose()
		first.PublicRealtimeClose()

		reused, err := adapter.PublicRealtimeOpen(context.Background(), PublicRealtimeOpenRequest{TournamentID: tournamentID})
		if err != nil {
			t.Fatalf("open after release error = %v", err)
		}
		const workers = 32
		var wait sync.WaitGroup
		for range workers {
			wait.Add(1)
			go func() {
				defer wait.Done()
				reused.PublicRealtimeClose()
			}()
		}
		wait.Wait()
		final, err := adapter.PublicRealtimeOpen(context.Background(), PublicRealtimeOpenRequest{TournamentID: tournamentID})
		if err != nil {
			t.Fatalf("open after concurrent close error = %v", err)
		}
		final.PublicRealtimeClose()
	})

	t.Run("replay output is bounded and falls back to snapshot", func(t *testing.T) {
		adapter := publicRealtimeTestAdapter(t, &publicRealtimeTestSource{state: state}, 1, 1)
		connection, err := adapter.PublicRealtimeOpen(context.Background(), PublicRealtimeOpenRequest{
			TournamentID: tournamentID,
			Cursor:       &RealtimeCursor{SchemaVersion: ArenaRealtimeSchemaVersion, TournamentID: tournamentID, LastSequence: 12, ProjectionRevision: 12},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer connection.PublicRealtimeClose()
		if !connection.PublicRealtimeUsesSnapshot() || len(connection.PublicRealtimeEnvelopes()) != 1 {
			t.Fatalf("bounded replay = snapshot %t, sequences %v", connection.PublicRealtimeUsesSnapshot(), publicRealtimeTestSequences(connection.PublicRealtimeEnvelopes()))
		}
	})

	t.Run("mutation-shaped open input is rejected", func(t *testing.T) {
		var request PublicRealtimeOpenRequest
		body := []byte(`{"tournament_id":"` + tournamentID.String() + `","command":{"pause":true}}`)
		if err := json.Unmarshal(body, &request); !errors.Is(err, ErrPublicRealtimeReadOnly) {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
	})
}

type publicRealtimeTestSource struct {
	state PublicRealtimeReadResult
}

func (source *publicRealtimeTestSource) PublicRealtimeRead(context.Context, uuid.UUID) (PublicRealtimeReadResult, error) {
	return source.state, nil
}

func publicRealtimeTestAdapter(t *testing.T, source PublicRealtimeReadSource, maxConnections, maxReplayEvents int) *PublicRealtimeAdapter {
	t.Helper()
	adapter, err := NewPublicRealtimeAdapter(source, PublicRealtimeConfig{MaxConnections: maxConnections, MaxReplayEvents: maxReplayEvents})
	if err != nil {
		t.Fatalf("NewPublicRealtimeAdapter() error = %v", err)
	}
	return adapter
}

func publicRealtimeTestState(t *testing.T, tournamentID uuid.UUID) PublicRealtimeReadResult {
	t.Helper()
	return PublicRealtimeReadResult{
		Snapshot: publicRealtimeTestSnapshot(t, tournamentID, 14, 14),
		SnapshotMetadata: RealtimeEnvelopeMetadata{
			SchemaVersion:      ArenaRealtimeSchemaVersion,
			TournamentID:       tournamentID,
			Sequence:           14,
			EventID:            testUUID("44000000-0000-4000-8000-000000000099"),
			OccurredAt:         time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC),
			ProjectionRevision: 14,
		},
		Available: RealtimeAvailableRange{OldestSequence: 12, LatestSequence: 14, CurrentProjectionRevision: 14},
		Events: []RealtimeEnvelope{
			publicRealtimeTestEnvelope(t, tournamentID, 12, 12),
			publicRealtimeTestEnvelope(t, tournamentID, 13, 13),
			publicRealtimeTestEnvelope(t, tournamentID, 14, 14),
		},
	}
}

func publicRealtimeTestSnapshot(t *testing.T, tournamentID uuid.UUID, sequence, revision int64) PublicSnapshot {
	t.Helper()
	input := testPublicSnapshotInput(tournamentID)
	input.LastSequence = sequence
	input.Revision = revision
	snapshot, err := NewPublicSnapshot(tournamentID, input)
	if err != nil {
		t.Fatalf("NewPublicSnapshot() error = %v", err)
	}
	return snapshot
}

func publicRealtimeTestEnvelope(t *testing.T, tournamentID uuid.UUID, sequence, revision int64) RealtimeEnvelope {
	t.Helper()
	return publicRealtimeTestRoleEnvelope(t, tournamentID, sequence, revision, publicRealtimeTestSnapshot(t, tournamentID, sequence, revision))
}

func publicRealtimeTestRoleEnvelope(t *testing.T, tournamentID uuid.UUID, sequence, revision int64, payload any) RealtimeEnvelope {
	t.Helper()
	envelope, err := NewRealtimeEnvelope(RealtimeEnvelopeMetadata{
		SchemaVersion:      ArenaRealtimeSchemaVersion,
		TournamentID:       tournamentID,
		Sequence:           sequence,
		EventID:            uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("public/%s/%d/%d", tournamentID, sequence, revision))),
		OccurredAt:         time.Date(2026, 9, 2, 9, 0, 0, int(sequence), time.UTC),
		ProjectionRevision: revision,
	}, payload)
	if err != nil {
		t.Fatalf("NewRealtimeEnvelope() error = %v", err)
	}
	return envelope
}

func publicRealtimeTestSequences(envelopes []RealtimeEnvelope) []int64 {
	sequences := make([]int64, len(envelopes))
	for index := range envelopes {
		sequences[index] = envelopes[index].Sequence
	}
	return sequences
}
