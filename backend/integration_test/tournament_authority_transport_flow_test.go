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
	tournamentws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/tournament"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	auditrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/audit"
	projectionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
	correctionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/correction"
	snapshotrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/snapshot"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	audit "github.com/TakuyaYagam1/task-per-minute/internal/usecase/audit"
)

func TestTournamentAuthorityTransportFlow(t *testing.T) {
	t.Run("audit retention and filters", TestResultAuditRepository)
	t.Run("incident bundle uses durable redacted audit events", testTournamentDurableIncidentBundle)
	t.Run("participant and public realtime recover from authoritative storage", testTournamentRealtimeRecovery)
	t.Run("correction converges atomically", TestResultCorrectionRepository)
	t.Run("task delivery remains attempt scoped", TestAssignmentRepositoryCommitsProofAndDeliversExactlyOnce)
}

func testTournamentDurableIncidentBundle(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createCorrectionRepositoryFixture(ctx, t)
	corrections := correctionrepo.NewCorrectionPostgres(postgres.NewTxManager(sharedPool))
	input := newCorrectionInput(
		ctx, t,
		fixture,
		fixture.result,
		fixture.projection,
		1,
		fixture.nextTime,
	)
	rebuilt, err := corrections.Rebuild(ctx, input)
	require.NoError(t, err)

	auditRepository := auditrepo.NewAuditPostgres(postgres.NewTxManager(sharedPool))
	records := listAuditRecords(ctx, t, auditRepository, auditrepo.AuditFilter{
		TournamentID: fixture.resultFixture.draft.tournamentID,
		PageSize:     1,
	})
	require.NotEmpty(t, records)

	events := make([]audit.AuditEvent, 0, len(records))
	generatedAt := records[0].CreatedAt
	for _, record := range records {
		events = append(events, tournamentAuditUseCaseEvent(record))
		if record.CreatedAt.After(generatedAt) {
			generatedAt = record.CreatedAt
		}
	}
	bundle, err := audit.GenerateIncidentBundle(audit.IncidentBundleSnapshot{
		TournamentID:       fixture.resultFixture.draft.tournamentID,
		ProjectionRevision: rebuilt.Projection.Revision.RevisionNumber,
		GeneratedAt:        generatedAt.Add(time.Microsecond),
		Events:             events,
	})
	require.NoError(t, err)
	require.NoError(t, audit.VerifyIncidentBundle(bundle))
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
	require.ErrorIs(t, audit.VerifyIncidentBundle(tampered), audit.ErrInvalidIncidentBundle)
}

func testTournamentRealtimeRecovery(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createGoldenMigrationFixture(ctx, t, 4)
	goldenPositionCommitID := createProjectionGoldenSource(ctx, t, fixture)
	projection := projectionrepo.NewProjectionPostgres(postgres.NewTxManager(sharedPool))
	createdAt := fixture.createdAt.Add(8 * time.Second)
	published, err := projection.Publish(ctx, projectionrepo.ProjectionPublishInput{
		IDs: projectionrepo.ProjectionIDs{RevisionID: uuid.New(), CutoffID: uuid.New()},
		Scope: projectionrepo.ProjectionScope{
			TournamentID: fixture.tournamentID,
			RosterID:     fixture.rosterID,
		},
		Source: projectionrepo.ProjectionSource{
			Kind:                   "golden_position",
			GoldenPositionCommitID: &goldenPositionCommitID,
			Reason:                 "publish realtime recovery fixture",
		},
		Artifacts:          projectionRepositoryArtifacts(t, fixture.participantIDs, goldenPositionCommitID, "realtime"),
		SupersessionReason: "replace realtime recovery fixture",
		CutoffAt:           createdAt,
		CreatedAt:          createdAt,
		PublishedAt:        createdAt.Add(time.Second),
	})
	require.NoError(t, err)

	var playerID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx,
		"SELECT player_id FROM participants WHERE id = $1",
		fixture.participantIDs[0],
	).Scan(&playerID))
	reader := snapshotrepo.NewTournamentSnapshotPostgres(postgres.NewTxManager(sharedPool))
	source, err := rootws.NewTournamentProductionSnapshotSource(reader)
	require.NoError(t, err)
	participantFlow, err := rootws.NewTournamentParticipantFlow(source)
	require.NoError(t, err)
	participantPayload, err := participantFlow.OpenTournamentParticipant(ctx, rootws.TournamentParticipantConnectionRequest{
		Principal: tournamentws.ParticipantRealtimePrincipal{
			Authenticated: true,
			TournamentID:  fixture.tournamentID,
			PlayerID:      playerID,
		},
		TournamentID: fixture.tournamentID,
	})
	require.NoError(t, err)
	require.Equal(t, int64(0), participantPayload.Envelope.Sequence)
	require.Equal(t, published.Revision.RevisionNumber, participantPayload.Envelope.ProjectionRevision)
	require.Equal(t, playerID, participantPayload.Envelope.Participant.PlayerID)
	require.Equal(t, fixture.tournamentID, participantPayload.Envelope.TournamentID)

	publicFlow, err := rootws.NewTournamentPublicFlow(source, &tournamentws.PublicRealtimeConfig{
		MaxConnections: 1,
	})
	require.NoError(t, err)
	publicContext, cancelPublic := context.WithCancel(ctx)
	publicPayload, err := publicFlow.OpenTournamentPublic(publicContext, rootws.TournamentPublicConnectionRequest{
		TournamentID: fixture.tournamentID,
	})
	require.NoError(t, err)
	cancelPublic()
	require.Equal(t, int64(0), publicPayload.Envelope.Sequence)
	require.Equal(t, published.Revision.RevisionNumber, publicPayload.Envelope.ProjectionRevision)
	require.Equal(t, fixture.tournamentID, publicPayload.Envelope.Public.Tournament.TournamentID)
	encoded, err := json.Marshal(publicPayload)
	require.NoError(t, err)
	for _, forbidden := range []string{"participant_id", "assignment", "submitted_flag", "operator"} {
		require.NotContains(t, string(encoded), forbidden)
	}

	_, err = participantFlow.OpenTournamentParticipant(ctx, rootws.TournamentParticipantConnectionRequest{
		Principal: tournamentws.ParticipantRealtimePrincipal{
			Authenticated: true,
			TournamentID:  fixture.tournamentID,
			PlayerID:      uuid.New(),
		},
		TournamentID: fixture.tournamentID,
	})
	require.ErrorIs(t, err, tournamentws.ErrParticipantRealtimeUnavailable)
}

func tournamentAuditUseCaseEvent(record auditrepo.AuditRecord) audit.AuditEvent {
	return audit.AuditEvent{
		AuditEventID:             record.AuditEventID,
		TournamentID:             record.TournamentID,
		RosterID:                 record.RosterID,
		SeriesID:                 record.SeriesID,
		ResultEventID:            record.ResultEventID,
		ActorKind:                domain.ResultActorKind(record.ActorKind),
		ActorID:                  cloneTournamentUUID(record.ActorID),
		EventType:                record.EventType,
		RedactedPayload:          append(json.RawMessage(nil), record.RedactedPayload...),
		OccurredAt:               record.OccurredAt,
		CreatedAt:                record.CreatedAt,
		ResultState:              record.ResultState,
		ResultReason:             record.ResultReason,
		WinnerID:                 cloneTournamentUUID(record.WinnerID),
		OfficialResultRevisionID: record.OfficialResultRevisionID,
		EntityKind:               audit.EntityKind(record.EntityKind),
		EntityID:                 record.EntityID,
		RevisionNumber:           record.RevisionNumber,
		IsCurrent:                record.IsCurrent,
		IsSuperseded:             record.IsSuperseded,
	}
}

func cloneTournamentUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
