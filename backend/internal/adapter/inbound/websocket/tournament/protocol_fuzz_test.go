package tournament

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func FuzzRealtimeEnvelope(f *testing.F) {
	tournamentID := uuid.MustParse("95000000-0000-4000-8000-000000000001")
	snapshot, err := NewParticipantSnapshot(
		ParticipantSnapshotScope{TournamentID: tournamentID, PlayerID: uuid.MustParse("95000000-0000-4000-8000-000000000002")},
		ParticipantSnapshotInput{
			TournamentID: tournamentID,
			PlayerID:     uuid.MustParse("95000000-0000-4000-8000-000000000002"),
			Revision:     1,
			LastSequence: 1,
		},
	)
	if err != nil {
		f.Fatal(err)
	}
	envelope, err := NewRealtimeEnvelope(RealtimeEnvelopeMetadata{
		SchemaVersion:      TournamentRealtimeSchemaVersion,
		TournamentID:       tournamentID,
		Sequence:           1,
		EventID:            uuid.MustParse("95000000-0000-4000-8000-000000000003"),
		OccurredAt:         time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC),
		ProjectionRevision: 1,
	}, snapshot)
	if err != nil {
		f.Fatal(err)
	}
	seed, err := json.Marshal(envelope)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte(`{"schema_version":1,"schema_version":2}`))
	f.Add([]byte(`[[[[[]]]]]`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var decoded RealtimeEnvelope
		if err := json.Unmarshal(data, &decoded); err != nil {
			return
		}
		if err := decoded.Validate(); err != nil {
			t.Fatalf("successful decode produced invalid envelope: %v", err)
		}
		if _, err := json.Marshal(decoded); err != nil {
			t.Fatalf("successful decode did not re-encode: %v", err)
		}
	})
}

func FuzzRealtimeEnvelopeTimestamp(f *testing.F) {
	tournamentID := uuid.MustParse("95000000-0000-4000-8000-000000000021")
	snapshot, err := NewParticipantSnapshot(
		ParticipantSnapshotScope{TournamentID: tournamentID, PlayerID: uuid.MustParse("95000000-0000-4000-8000-000000000022")},
		ParticipantSnapshotInput{
			TournamentID: tournamentID,
			PlayerID:     uuid.MustParse("95000000-0000-4000-8000-000000000022"),
			Revision:     1,
			LastSequence: 0,
		},
	)
	if err != nil {
		f.Fatal(err)
	}
	envelope, err := NewRealtimeEnvelope(RealtimeEnvelopeMetadata{
		SchemaVersion: TournamentRealtimeSchemaVersion,
		TournamentID:  tournamentID,
		Sequence:      0,
		EventID:       uuid.MustParse("95000000-0000-4000-8000-000000000023"),
		OccurredAt:    time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC), ProjectionRevision: 1,
	}, snapshot)
	if err != nil {
		f.Fatal(err)
	}
	seed, err := json.Marshal(envelope)
	if err != nil {
		f.Fatal(err)
	}
	f.Add("2026-09-06T09:00:00Z")
	f.Add("2026-09-06T12:00:00+03:00")
	f.Add("not-a-time")
	f.Add("")
	f.Fuzz(func(t *testing.T, timestamp string) {
		var frame map[string]json.RawMessage
		if err := json.Unmarshal(seed, &frame); err != nil {
			t.Fatal(err)
		}
		encodedTimestamp, err := json.Marshal(timestamp)
		if err != nil {
			t.Fatal(err)
		}
		frame["occurred_at"] = encodedTimestamp
		data, err := json.Marshal(frame)
		if err != nil {
			t.Fatal(err)
		}
		var decoded RealtimeEnvelope
		if err := json.Unmarshal(data, &decoded); err == nil && decoded.Validate() != nil {
			t.Fatal("successful timestamp decode produced an invalid envelope")
		}
	})
}

func FuzzRoleSnapshotConstructors(f *testing.F) {
	f.Add("label", int64(1), uint8(0))
	f.Add("swiss", int64(1), uint8(1))
	f.Add("operator", int64(1), uint8(2))
	f.Add("", int64(0), uint8(1))
	f.Fuzz(func(t *testing.T, label string, revision int64, selector uint8) {
		tournamentID := uuid.MustParse("95000000-0000-4000-8000-000000000011")
		playerID := uuid.MustParse("95000000-0000-4000-8000-000000000012")
		switch selector % 3 {
		case 0:
			input := ParticipantSnapshotInput{TournamentID: tournamentID, PlayerID: playerID, Revision: revision, LastSequence: 1}
			if label != "" {
				input.Opponent = &OpponentCompetitionInput{
					TournamentID: tournamentID,
					PlayerID:     uuid.MustParse("95000000-0000-4000-8000-000000000013"),
					DisplayName:  label,
					SeriesID:     uuid.MustParse("95000000-0000-4000-8000-000000000014"),
					SeriesState:  label,
				}
			}
			if snapshot, err := NewParticipantSnapshot(ParticipantSnapshotScope{TournamentID: tournamentID, PlayerID: playerID}, input); err == nil && snapshot.Validate() != nil {
				t.Fatal("participant constructor returned an invalid snapshot")
			}
		case 1:
			input := PublicSnapshotInput{
				Revision: revision, LastSequence: 1,
				Tournament: PublicTournamentInput{TournamentID: tournamentID, Preset: "tournament_v1", State: label},
				Scoreboard: []PublicScoreboardEntryInput{}, Bracket: []PublicBracketMatchInput{},
				LiveSeries: []PublicSeriesInput{}, OfficialResults: []PublicOfficialResultInput{},
			}
			if snapshot, err := NewPublicSnapshot(tournamentID, input); err == nil && snapshot.Validate() != nil {
				t.Fatal("public constructor returned an invalid snapshot")
			}
		case 2:
			input := OperatorSnapshotInput{
				TournamentID: tournamentID, Revision: revision, LastSequence: 1,
				Waves: []OperatorWaveInput{}, Presence: []OperatorPresenceInput{},
				Replays: []OperatorReplayInput{}, AuditLinks: []OperatorAuditLinkInput{},
			}
			access := OperatorSnapshotAccess{Authenticated: label != "", TournamentID: tournamentID, OperatorID: uuid.MustParse("95000000-0000-4000-8000-000000000015")}
			if snapshot, err := NewOperatorSnapshot(access, input); err == nil && snapshot.Validate() != nil {
				t.Fatal("operator constructor returned an invalid snapshot")
			}
		}
	})
}
