package websocket

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	tournamentws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/tournament"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/wirelimits"
)

func TestTournamentMessageDecodeRejectsDuplicateKeysAndBounds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
	}{
		{name: "duplicate event type", body: `{"type":"tournament.rejected","type":"tournament.public","code":"tournament.unavailable","message":"unavailable"}`},
		{name: "case folded duplicate", body: `{"type":"tournament.rejected","TYPE":"tournament.public","code":"tournament.unavailable","message":"unavailable"}`},
		{name: "nested duplicate", body: `{"type":"tournament.rejected","code":"tournament.unavailable","message":"unavailable","nested":{"id":1,"id":2}}`},
		{name: "oversized string", body: `{"type":"tournament.rejected","code":"tournament.unavailable","message":"` + strings.Repeat("a", wirelimits.MaxStringBytes+1) + `"}`},
		{name: "oversized frame", body: `{"type":"tournament.rejected","code":"tournament.unavailable","message":"` + strings.Repeat("a", wirelimits.MaxMessageBytes) + `"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := DecodeTournamentPublicMessage([]byte(tt.body))
			require.ErrorIs(t, err, ErrTournamentInvalidPayload)
		})
	}
}

func TestTournamentRolePayloadsCopySource(t *testing.T) {
	t.Parallel()
	for _, role := range []uint8{0, 1, 2} {
		role := role
		t.Run(tournamentRoleName(role), func(t *testing.T) {
			t.Parallel()
			probe := tournamentPayloadProbe("static")
			frame := tournamentRoleFrame(t, role, probe)
			require.NotContains(t, string(frame), probe)
			requireNoTournamentPrivateKeys(t, frame)
		})
	}
}

func FuzzTournamentRolePayloadsCopySource(f *testing.F) {
	f.Add(uint8(0), "participant")
	f.Add(uint8(1), "public")
	f.Add(uint8(2), "operator")
	f.Fuzz(func(t *testing.T, role uint8, value string) {
		probe := tournamentPayloadProbe(value)
		frame := tournamentRoleFrame(t, role%3, probe)
		require.NotContains(t, string(frame), probe)
		requireNoTournamentPrivateKeys(t, frame)
	})
}

func FuzzTournamentMessageDecode(f *testing.F) {
	f.Add([]byte(`{"type":"tournament.rejected","code":"tournament.unavailable","message":"unavailable"}`))
	f.Add([]byte(`{"type":"tournament.rejected","type":"tournament.public"}`))
	f.Add([]byte(`[]`))
	f.Fuzz(func(_ *testing.T, data []byte) {
		_, _ = DecodeTournamentParticipantMessage(data)
		_, _ = DecodeTournamentPublicMessage(data)
		_, _ = DecodeTournamentOperatorMessage(data)
	})
}

func FuzzTournamentRolePayloadConstructors(f *testing.F) {
	f.Add(int64(7), uint8(0))
	f.Add(int64(1), uint8(0))
	f.Add(int64(0), uint8(1))
	f.Fuzz(func(t *testing.T, revision int64, mutation uint8) {
		tournamentID := tournamentSourceID(901)
		playerID := tournamentSourceID(902)
		reader := tournamentServerSnapshotReader(t, tournamentID, playerID)
		source, err := NewTournamentProductionSnapshotSource(reader)
		require.NoError(t, err)

		participantEnvelope, participantErr := tournamentws.ParticipantRealtimeView(context.Background(), source, tournamentws.ParticipantRealtimeRequest{
			Principal:    tournamentws.ParticipantRealtimePrincipal{Authenticated: true, TournamentID: tournamentID, PlayerID: playerID},
			TournamentID: tournamentID,
		})
		if participantErr == nil {
			participantEnvelope.ProjectionRevision = revision
			if payload, err := NewTournamentParticipantPayload(participantEnvelope); err == nil {
				require.NoError(t, validateTournamentParticipantPayload(payload))
			}
		}

		publicRead, publicErr := source.PublicRealtimeRead(context.Background(), tournamentID)
		if publicErr == nil {
			publicEnvelope, err := tournamentws.NewRealtimeEnvelope(publicRead.SnapshotMetadata, publicRead.Snapshot)
			if err == nil {
				publicEnvelope.ProjectionRevision = revision
				if mutation%2 == 1 {
					publicEnvelope.TournamentID = tournamentSourceID(903)
				}
				if payload, err := NewTournamentPublicPayload(publicEnvelope); err == nil {
					require.NoError(t, validateTournamentPublicPayload(payload))
				}
			}
		}

		operatorEnvelope, operatorErr := source.ReadOperatorRealtime(context.Background(), tournamentws.OperatorRealtimeQuery{
			TournamentID: tournamentID,
			OperatorID:   tournamentSourceID(904),
		})
		if operatorErr == nil {
			operatorEnvelope.ProjectionRevision = revision
			if payload, err := NewTournamentOperatorPayload(operatorEnvelope); err == nil {
				require.NoError(t, validateTournamentOperatorPayload(payload))
			}
		}
	})
}

func TestTournamentMarshalRejectsFrameBeyondTransportLimit(t *testing.T) {
	t.Parallel()
	rejection := TournamentRejection{
		Code:    TournamentRejectionUnavailable,
		Message: strings.Repeat("a", wirelimits.MaxStringBytes+1),
	}
	_, err := MarshalTournamentRejected(rejection)
	require.ErrorIs(t, err, ErrTournamentInvalidPayload)
}

func TestTournamentEventJSONRemainsStable(t *testing.T) {
	t.Parallel()
	encoded, err := MarshalTournamentRejected(TournamentRejection{Code: TournamentRejectionUnavailable, Message: "unavailable"})
	require.NoError(t, err)
	var decoded map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.ElementsMatch(t, []string{"type", "code", "message"}, mapKeys(decoded))
}

func mapKeys(object map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	return keys
}

func tournamentRoleFrame(t testing.TB, role uint8, probe string) []byte {
	t.Helper()
	tournamentID := tournamentSourceID(951)
	metadata := tournamentRoleMetadata(tournamentID)
	switch role {
	case 0:
		playerID := tournamentSourceID(952)
		snapshot, err := tournamentws.NewParticipantSnapshot(
			tournamentws.ParticipantSnapshotScope{TournamentID: tournamentID, PlayerID: playerID},
			tournamentws.ParticipantSnapshotInput{
				TournamentID: tournamentID,
				PlayerID:     playerID,
				Revision:     metadata.ProjectionRevision,
				LastSequence: metadata.Sequence,
				Assignment: &tournamentws.ParticipantAssignmentInput{
					TournamentID: tournamentID,
					PlayerID:     playerID,
					AssignmentID: tournamentSourceID(953),
					AttemptID:    tournamentSourceID(954),
					SeriesID:     tournamentSourceID(955),
					GameID:       tournamentSourceID(956),
					WaveID:       tournamentSourceID(957),
					Task: tournamentws.ParticipantTaskInput{
						SnapshotID:       tournamentSourceID(958),
						TaskID:           tournamentSourceID(959),
						Title:            "Packet relay",
						Category:         "web",
						Difficulty:       "medium",
						TimeLimitSeconds: 300,
					},
				},
				Opponent: &tournamentws.OpponentCompetitionInput{
					TournamentID: tournamentID,
					PlayerID:     tournamentSourceID(960),
					DisplayName:  "blue",
					SeriesID:     tournamentSourceID(955),
					Ready:        true,
					SeriesState:  "active",
				},
			},
		)
		require.NoError(t, err)
		envelope, err := tournamentws.NewRealtimeEnvelope(metadata, snapshot)
		require.NoError(t, err)
		participant := tournamentws.ParticipantRealtimeEnvelope{
			SchemaVersion:      envelope.SchemaVersion,
			TournamentID:       envelope.TournamentID,
			Sequence:           envelope.Sequence,
			EventID:            envelope.EventID,
			OccurredAt:         envelope.OccurredAt,
			ProjectionRevision: envelope.ProjectionRevision,
			ResumeID:           envelope.ResumeID,
			Participant:        *envelope.Participant,
		}
		payload, err := NewTournamentParticipantPayload(participant)
		require.NoError(t, err)
		participant.Participant.Assignment.Task.Title = probe
		participant.Participant.Opponent.DisplayName = probe
		frame, err := MarshalTournamentParticipant(payload)
		require.NoError(t, err)
		return frame
	case 1:
		startedAt := time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC)
		snapshot, err := tournamentws.NewPublicSnapshot(tournamentID, tournamentws.PublicSnapshotInput{
			Revision:     metadata.ProjectionRevision,
			LastSequence: metadata.Sequence,
			Tournament: tournamentws.PublicTournamentInput{
				TournamentID: tournamentID,
				Preset:       "tournament_v1",
				State:        "swiss",
				RosterSize:   8,
				StartedAt:    &startedAt,
			},
			Scoreboard: []tournamentws.PublicScoreboardEntryInput{{
				TournamentID: tournamentID, Rank: 1, DisplayName: "red", Points: 3, Buchholz: 2, EffectiveTimeMS: 4000,
			}},
			Bracket:         []tournamentws.PublicBracketMatchInput{},
			LiveSeries:      []tournamentws.PublicSeriesInput{},
			OfficialResults: []tournamentws.PublicOfficialResultInput{},
			Draft: &tournamentws.PublicDraftInput{
				TournamentID:       tournamentID,
				SeriesID:           tournamentSourceID(961),
				Format:             "bo3",
				State:              "active",
				Pool:               []string{"web"},
				SelectedCategories: []string{"web"},
				Actions: []tournamentws.PublicDraftActionInput{{
					Turn: 1, Action: "pick", Category: "web", ActorDisplayName: "red", OccurredAt: startedAt,
				}},
			},
		})
		require.NoError(t, err)
		envelope, err := tournamentws.NewRealtimeEnvelope(metadata, snapshot)
		require.NoError(t, err)
		payload, err := NewTournamentPublicPayload(envelope)
		require.NoError(t, err)
		envelope.Public.Scoreboard[0].DisplayName = probe
		envelope.Public.Draft.Pool[0] = probe
		*envelope.Public.Tournament.StartedAt = envelope.Public.Tournament.StartedAt.Add(time.Minute)
		frame, err := MarshalTournamentPublic(payload)
		require.NoError(t, err)
		return frame
	default:
		deadline := time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC)
		seriesID := tournamentSourceID(962)
		snapshot, err := tournamentws.NewOperatorSnapshot(
			tournamentws.OperatorSnapshotAccess{Authenticated: true, TournamentID: tournamentID, OperatorID: tournamentSourceID(963)},
			tournamentws.OperatorSnapshotInput{
				TournamentID: tournamentID,
				Revision:     metadata.ProjectionRevision,
				LastSequence: metadata.Sequence,
				Waves: []tournamentws.OperatorWaveInput{{
					TournamentID:   tournamentID,
					WaveID:         tournamentSourceID(964),
					State:          "ready_window_open",
					WindowDeadline: &deadline,
					Members: []tournamentws.OperatorWaveMemberInput{{
						ParticipantID: tournamentSourceID(965), SeriesID: &seriesID, Ready: true, ReadinessRevision: 1,
					}},
				}},
				Presence:   []tournamentws.OperatorPresenceInput{},
				Replays:    []tournamentws.OperatorReplayInput{},
				AuditLinks: []tournamentws.OperatorAuditLinkInput{},
			},
		)
		require.NoError(t, err)
		envelope, err := tournamentws.NewRealtimeEnvelope(metadata, snapshot)
		require.NoError(t, err)
		payload, err := NewTournamentOperatorPayload(envelope)
		require.NoError(t, err)
		envelope.Operator.Waves[0].State = probe
		*envelope.Operator.Waves[0].Members[0].SeriesID = tournamentSourceID(966)
		*envelope.Operator.Waves[0].WindowDeadline = envelope.Operator.Waves[0].WindowDeadline.Add(time.Minute)
		frame, err := MarshalTournamentOperator(payload)
		require.NoError(t, err)
		return frame
	}
}

func tournamentRoleMetadata(tournamentID uuid.UUID) tournamentws.RealtimeEnvelopeMetadata {
	return tournamentws.RealtimeEnvelopeMetadata{
		SchemaVersion:      tournamentws.TournamentRealtimeSchemaVersion,
		TournamentID:       tournamentID,
		Sequence:           1,
		EventID:            tournamentSourceID(967),
		OccurredAt:         time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC),
		ProjectionRevision: 1,
	}
}

func tournamentRoleName(role uint8) string {
	switch role {
	case 0:
		return "participant"
	case 1:
		return "public"
	default:
		return "operator"
	}
}

func tournamentPayloadProbe(value string) string {
	digest := sha256.Sum256([]byte(value))
	return "probe-" + hex.EncodeToString(digest[:])
}

func requireNoTournamentPrivateKeys(t testing.TB, frame []byte) {
	t.Helper()
	var decoded any
	require.NoError(t, json.Unmarshal(frame, &decoded))
	forbidden := map[string]struct{}{
		"audit_actor": {}, "command": {}, "credential": {}, "flag": {}, "hidden_hint": {},
		"password": {}, "raw_connection": {}, "secret": {}, "source_file_url": {}, "submission": {}, "task_url": {},
	}
	requireNoForbiddenKey(t, decoded, forbidden)
}

func requireNoForbiddenKey(t testing.TB, value any, forbidden map[string]struct{}) {
	t.Helper()
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			requireNoForbiddenKey(t, item, forbidden)
		}
	case map[string]any:
		for key, item := range typed {
			_, found := forbidden[strings.ToLower(key)]
			require.Falsef(t, found, "forbidden realtime key %q", key)
			requireNoForbiddenKey(t, item, forbidden)
		}
	}
}
