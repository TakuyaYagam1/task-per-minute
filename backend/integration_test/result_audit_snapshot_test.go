//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestResultAuditRepositoryPinsSnapshotAcrossPages(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createCorrectionRepositoryFixture(ctx, t)
	corrections := postgres.NewCorrectionPostgres(postgres.NewTxManager(sharedPool))
	firstInput := newCorrectionInput(
		ctx, t, fixture, fixture.result, fixture.projection, 1, fixture.nextTime,
	)
	firstInput.IDs.AuditEventID = uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff")
	first, err := corrections.Rebuild(ctx, firstInput)
	require.NoError(t, err)

	audit := postgres.NewAuditPostgres(postgres.NewTxManager(sharedPool))
	filter := postgres.AuditFilter{
		TournamentID: fixture.resultFixture.draft.tournamentID,
		PageSize:     1,
	}
	firstPage, err := audit.List(ctx, filter)
	require.NoError(t, err)
	require.Len(t, firstPage.Records, 1)
	require.NotNil(t, firstPage.NextCursor)
	require.NotEmpty(t, firstPage.NextCursor.SnapshotBound)
	snapshotBound := firstPage.NextCursor.SnapshotBound

	insertedAuditID := settleCursorSafeAuditRecord(
		ctx,
		t,
		fixture,
		firstInput.CorrectedAt,
		uuid.MustParse("00000000-0000-0000-0000-000000000001"),
	)
	secondInput := newCorrectionInput(
		ctx,
		t,
		fixture,
		first.ResultCommit,
		first.Projection,
		0,
		fixture.nextTime.Add(time.Second),
	)
	var currentProjectionID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT id
		FROM projection_revisions
		WHERE tournament_id = $1 AND roster_id = $2 AND state = 'published'`,
		fixture.resultFixture.draft.tournamentID,
		fixture.resultFixture.draft.rosterID,
	).Scan(&currentProjectionID))
	secondInput.ExpectedProjectionRevisionID = currentProjectionID
	_, err = corrections.Rebuild(ctx, secondInput)
	require.NoError(t, err)

	records := append([]postgres.AuditRecord(nil), firstPage.Records...)
	filter.Cursor = firstPage.NextCursor
	for filter.Cursor != nil {
		require.Equal(t, snapshotBound, filter.Cursor.SnapshotBound)
		page, listErr := audit.List(ctx, filter)
		require.NoError(t, listErr)
		records = append(records, page.Records...)
		filter.Cursor = page.NextCursor
	}
	require.Len(t, records, 4)
	current := map[uuid.UUID]struct{}{
		first.ResultCommit.GameRevision.ID:   {},
		first.ResultCommit.SeriesRevision.ID: {},
	}
	currentCount := 0
	for _, record := range records {
		_, expectedCurrent := current[record.OfficialResultRevisionID]
		require.Equal(t, expectedCurrent, record.IsCurrent)
		require.Equal(t, !expectedCurrent, record.IsSuperseded)
		if record.IsCurrent {
			currentCount++
		}
		require.NotEqual(t, secondInput.IDs.AuditEventID, record.AuditEventID)
		require.NotEqual(t, insertedAuditID, record.AuditEventID)
	}
	require.Equal(t, 2, currentCount)

	fresh := listAuditRecords(ctx, t, audit, postgres.AuditFilter{
		TournamentID: fixture.resultFixture.draft.tournamentID,
		PageSize:     1,
	})
	require.Len(t, fresh, 7)
}

func settleCursorSafeAuditRecord(
	ctx context.Context,
	tb testing.TB,
	fixture correctionRepositoryFixture,
	settledAt time.Time,
	auditEventID uuid.UUID,
) uuid.UUID {
	tb.Helper()

	lockedAt := settledAt.Add(-time.Second)
	seriesID := createBackdatedAuditSeries(ctx, tb, fixture, lockedAt.Add(-time.Second))
	slotID := createMigrationGameSlot(
		ctx, tb, seriesID, fixture.resultFixture.draft.rosterID, 1, "web",
	)
	attemptID := createActiveMigrationAttempt(
		ctx, tb, slotID, seriesID, fixture.resultFixture.draft.rosterID, lockedAt.Add(-time.Second),
	)
	draft := fixture.resultFixture.draft
	draft.seriesID = seriesID
	_ = lockMigrationSeries(ctx, tb, draft, lockedAt)

	ids := postgres.ResultSettlementIDs{
		CommitID: uuid.New(), ResultEventID: uuid.New(), ResultEventIdempotencyKey: uuid.New(),
		GameResultRevisionID: uuid.New(), SeriesScoreRevisionID: uuid.New(),
		AuditEventID: auditEventID, OutboxEventID: uuid.New(), OutboxIdempotencyKey: uuid.New(),
		ProjectionEvidenceID: uuid.New(), CommitIdempotencyKey: uuid.New(),
	}
	actorID := fixture.resultFixture.draft.participantIDs[0]
	digest := sha256.Sum256([]byte("snapshot traversal concurrent result"))
	result, changed, err := postgres.NewResultPostgres(postgres.NewTxManager(sharedPool)).Settle(
		ctx,
		postgres.ResultSettlementInput{
			IDs: ids,
			Scope: postgres.ResultScope{
				TournamentID: fixture.resultFixture.draft.tournamentID,
				RosterID:     fixture.resultFixture.draft.rosterID,
				SeriesID:     seriesID,
				AttemptID:    attemptID,
			},
			GameState:       domain.GameStateCompleted,
			GameReason:      domain.GameResultReasonOperatorForfeit,
			GameWinnerID:    &actorID,
			Score:           domain.SeriesScore{FirstParticipantWins: 1},
			NextSeriesState: domain.SeriesStateLocked,
			ActorKind:       "operator",
			ActorID:         &actorID,
			ProjectionArtifactKinds: []domain.ArtifactKind{
				domain.ArtifactKindGameResult,
				domain.ArtifactKindSeriesScore,
			},
			ProjectionPayloadDigest: digest,
			SettledAt:               settledAt,
			ExpectedAttemptRevision: 1,
			ExpectedAttemptState:    domain.GameStateActive,
			ExpectedSeriesRevision:  2,
			ExpectedSeriesState:     domain.SeriesStateLocked,
		},
	)
	require.NoError(tb, err)
	require.True(tb, changed)
	require.Equal(tb, ids.AuditEventID, result.Audit.ID)
	return ids.AuditEventID
}

func createBackdatedAuditSeries(
	ctx context.Context,
	tb testing.TB,
	fixture correctionRepositoryFixture,
	createdAt time.Time,
) uuid.UUID {
	tb.Helper()
	seriesID := createMigrationSeries(
		ctx,
		tb,
		fixture.resultFixture.draft.tournamentID,
		fixture.resultFixture.draft.rosterID,
		fixture.resultFixture.draft.participantIDs,
		"bo3",
	)
	waveID, _ := createMigrationWave(
		ctx,
		tb,
		fixture.resultFixture.draft.tournamentID,
		fixture.resultFixture.draft.rosterID,
		fixture.resultFixture.draft.participantIDs,
		createdAt,
	)
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO wave_series (wave_id, tournament_id, roster_id, series_id, created_at)
		VALUES ($1, $2, $3, $4, $5)`,
		waveID,
		fixture.resultFixture.draft.tournamentID,
		fixture.resultFixture.draft.rosterID,
		seriesID,
		createdAt,
	)
	require.NoError(tb, err)
	var scoreRevisionID uuid.UUID
	require.NoError(tb, sharedPool.QueryRow(ctx, `
		SELECT current_revision_id
		FROM series_score_heads
		WHERE series_id = $1`, seriesID).Scan(&scoreRevisionID))
	authorityID := uuid.NewSHA1(waveID, []byte("result-projection-wave-initialization"))
	payload := []byte(`{"schema":"result-projection-series-score-genesis-v1"}`)
	payloadDigest := sha256.Sum256(payload)
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO result_projection_node_authorities (
			id, tournament_id, roster_id, source_kind, wave_id, created_at
		)
		VALUES ($1, $2, $3, 'wave_initialization', $4, $5)`,
		authorityID,
		fixture.resultFixture.draft.tournamentID,
		fixture.resultFixture.draft.rosterID,
		waveID,
		createdAt,
	)
	require.NoError(tb, err)
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO result_projection_nodes (
			id, authority_id, tournament_id, roster_id, artifact_kind,
			entity_id, revision_number, previous_node_id,
			payload, payload_digest, created_at
		)
		VALUES ($1, $2, $3, $4, 'series_score', $5, 1, NULL, $6::JSON, $7, $8)`,
		scoreRevisionID,
		authorityID,
		fixture.resultFixture.draft.tournamentID,
		fixture.resultFixture.draft.rosterID,
		seriesID,
		payload,
		payloadDigest[:],
		createdAt,
	)
	require.NoError(tb, err)
	return seriesID
}
