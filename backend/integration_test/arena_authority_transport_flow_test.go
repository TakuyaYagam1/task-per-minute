//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	rootws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket"
	arenaws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/arena"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arenausecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestArenaAuthorityTransportFlow(t *testing.T) {
	t.Run("audit retention and filters", TestArenaAuditRepository)
	t.Run("incident bundle uses durable redacted audit events", testArenaDurableIncidentBundle)
	t.Run("participant and public realtime recover from authoritative storage", testArenaRealtimeRecovery)
	t.Run("correction converges atomically", TestArenaCorrectionRepository)
	t.Run("cross mode reservation has one owner", TestArenaCasualReservationRace)
	t.Run("task delivery remains attempt scoped", TestArenaAssignmentRepositoryCommitsProofAndDeliversExactlyOnce)
	t.Run("role and tournament boundaries fail closed", TestArenaSecurityBoundaries)
	t.Run("Arena execution leaves casual state unchanged", TestArenaExecutionIsolation)
}

func testArenaDurableIncidentBundle(t *testing.T) {
	ctx := context.Background()
	resetArenaMigrationTables(t)
	t.Cleanup(func() { resetArenaMigrationTables(t) })

	fixture := createArenaCorrectionRepositoryFixture(t, ctx)
	corrections := postgres.NewArenaCorrectionPostgres(postgres.NewTxManager(sharedPool))
	input := newArenaCorrectionInput(
		t,
		ctx,
		fixture,
		fixture.result,
		fixture.projection,
		1,
		fixture.nextTime,
	)
	rebuilt, err := corrections.Rebuild(ctx, input)
	require.NoError(t, err)

	audit := postgres.NewArenaAuditPostgres(postgres.NewTxManager(sharedPool))
	records := listArenaAuditRecords(t, ctx, audit, postgres.ArenaAuditFilter{
		TournamentID: fixture.resultFixture.draft.tournamentID,
		PageSize:     1,
	})
	require.NotEmpty(t, records)

	events := make([]arenausecase.AuditEvent, 0, len(records))
	generatedAt := records[0].CreatedAt
	for _, record := range records {
		events = append(events, arenaAuditUseCaseEvent(record))
		if record.CreatedAt.After(generatedAt) {
			generatedAt = record.CreatedAt
		}
	}
	bundle, err := arenausecase.GenerateIncidentBundle(arenausecase.IncidentBundleSnapshot{
		TournamentID:       fixture.resultFixture.draft.tournamentID,
		ProjectionRevision: rebuilt.Projection.Revision.RevisionNumber,
		GeneratedAt:        generatedAt.Add(time.Microsecond),
		Events:             events,
	})
	require.NoError(t, err)
	require.NoError(t, arenausecase.VerifyIncidentBundle(bundle))
	require.NotEmpty(t, bundle.CanonicalContent)
	for _, forbidden := range [][]byte{
		[]byte("secret_token"),
		[]byte("session_token"),
		[]byte("ip_address"),
		[]byte("submitted_flag"),
	} {
		require.False(t, bytes.Contains(bundle.CanonicalContent, forbidden))
	}

	tampered := bundle
	tampered.CanonicalContent = append([]byte(nil), bundle.CanonicalContent...)
	tampered.CanonicalContent[len(tampered.CanonicalContent)-1] ^= 1
	require.ErrorIs(t, arenausecase.VerifyIncidentBundle(tampered), arenausecase.ErrInvalidIncidentBundle)
}

func testArenaRealtimeRecovery(t *testing.T) {
	ctx := context.Background()
	resetArenaMigrationTables(t)
	t.Cleanup(func() { resetArenaMigrationTables(t) })

	fixture := newArenaRepositoryFixture()
	createdAt := time.Date(2026, time.September, 2, 9, 0, 0, 0, time.UTC)
	tournament, roster := createArenaRepositoryTournament(t, ctx, fixture, createdAt)
	players := createArenaMigrationPlayers(t, ctx, 2)
	participant := addArenaRepositoryParticipant(
		t,
		ctx,
		fixture,
		roster.ID,
		players[0],
		1,
		domain.ArenaAttendanceStateCheckedIn,
		createdAt.Add(time.Second),
	)

	source := rootws.NewArenaProductionSnapshotSource(fixture.tournaments, fixture.tournaments)
	participantFlow, err := rootws.NewArenaParticipantFlow(source)
	require.NoError(t, err)
	staleCursor := &arenaws.RealtimeCursor{
		SchemaVersion:      arenaws.ArenaRealtimeSchemaVersion,
		TournamentID:       tournament.ID,
		LastSequence:       1,
		ProjectionRevision: 1,
	}
	participantPayload, err := participantFlow.OpenArenaParticipant(ctx, rootws.ArenaParticipantConnectionRequest{
		Principal: arenaws.ParticipantRealtimePrincipal{
			Authenticated: true,
			TournamentID:  tournament.ID,
			PlayerID:      participant.PlayerID,
		},
		TournamentID: tournament.ID,
		Cursor:       staleCursor,
	})
	require.NoError(t, err)
	require.True(t, participantPayload.UsesSnapshot)
	require.Len(t, participantPayload.Envelopes, 1)
	require.Equal(t, participant.PlayerID, participantPayload.Envelopes[0].Participant.PlayerID)
	require.Equal(t, tournament.ID, participantPayload.Envelopes[0].TournamentID)

	publicFlow, err := rootws.NewArenaPublicFlow(source, &arenaws.PublicRealtimeConfig{
		MaxConnections:  1,
		MaxReplayEvents: 16,
	})
	require.NoError(t, err)
	publicContext, cancelPublic := context.WithCancel(ctx)
	publicPayload, err := publicFlow.OpenArenaPublic(publicContext, rootws.ArenaPublicConnectionRequest{
		TournamentID: tournament.ID,
		Cursor:       staleCursor,
	})
	require.NoError(t, err)
	cancelPublic()
	require.True(t, publicPayload.UsesSnapshot)
	require.Len(t, publicPayload.Envelopes, 1)
	require.Equal(t, tournament.ID, publicPayload.Envelopes[0].Public.Tournament.TournamentID)
	encoded, err := json.Marshal(publicPayload)
	require.NoError(t, err)
	for _, forbidden := range []string{"participant_id", "assignment", "submitted_flag", "operator"} {
		require.NotContains(t, string(encoded), forbidden)
	}

	_, err = participantFlow.OpenArenaParticipant(ctx, rootws.ArenaParticipantConnectionRequest{
		Principal: arenaws.ParticipantRealtimePrincipal{
			Authenticated: true,
			TournamentID:  tournament.ID,
			PlayerID:      players[1],
		},
		TournamentID: tournament.ID,
	})
	require.ErrorIs(t, err, arenaws.ErrParticipantRealtimeUnavailable)
}

func arenaAuditUseCaseEvent(record postgres.ArenaAuditRecord) arenausecase.AuditEvent {
	return arenausecase.AuditEvent{
		AuditEventID:             record.AuditEventID,
		TournamentID:             record.TournamentID,
		RosterID:                 record.RosterID,
		SeriesID:                 record.SeriesID,
		ResultEventID:            record.ResultEventID,
		ActorKind:                arenausecase.ArenaResultActorKind(record.ActorKind),
		ActorID:                  cloneArenaUUID(record.ActorID),
		EventType:                record.EventType,
		RedactedPayload:          append(json.RawMessage(nil), record.RedactedPayload...),
		OccurredAt:               record.OccurredAt,
		CreatedAt:                record.CreatedAt,
		ResultState:              record.ResultState,
		ResultReason:             record.ResultReason,
		WinnerID:                 cloneArenaUUID(record.WinnerID),
		OfficialResultRevisionID: record.OfficialResultRevisionID,
		EntityKind:               arenausecase.AuditEntityKind(record.EntityKind),
		EntityID:                 record.EntityID,
		RevisionNumber:           record.RevisionNumber,
		IsCurrent:                record.IsCurrent,
		IsSuperseded:             record.IsSuperseded,
	}
}

func cloneArenaUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
