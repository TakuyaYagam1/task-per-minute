//go:build integration

package result

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/gameseed"
	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/resultaudit"
	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/seriesseed"
	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/waveseed"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	auditrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/audit"
	resultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	resultauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/authority"
	correctionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/correction"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// CursorAuditSeedInput is the explicit identity boundary for the extra audit
// event inserted while an audit cursor is being read.
type CursorAuditSeedInput struct {
	TournamentID   uuid.UUID
	RosterID       uuid.UUID
	ParticipantIDs [2]uuid.UUID
	SettledAt      time.Time
	AuditEventID   uuid.UUID
}

// RunResultAuditRepositoryPinsSnapshotAcrossPages preserves cursor snapshot
// semantics with the child-owned correction fixture and explicit pool.
func RunResultAuditRepositoryPinsSnapshotAcrossPages(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, resetResultTables(ctx, pool))
	t.Cleanup(func() { require.NoError(t, resetResultTables(ctx, pool)) })

	fixture, err := createCorrectionFixture(ctx, pool)
	require.NoError(t, err)
	corrections := correctionrepo.NewCorrectionPostgres(postgres.NewTxManager(pool))
	firstInput := newCorrectionInput(ctx, t, pool, fixture, fixture.correction.Result, fixture.correction.Projection, 1, fixture.correction.NextTime)
	firstInput.IDs.AuditEventID = uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff")
	first, err := corrections.Rebuild(ctx, firstInput)
	require.NoError(t, err)

	audit := auditrepo.NewAuditPostgres(postgres.NewTxManager(pool))
	filter := auditrepo.AuditFilter{TournamentID: fixture.draft.TournamentID, PageSize: 1}
	firstPage, err := audit.List(ctx, filter)
	require.NoError(t, err)
	require.Len(t, firstPage.Records, 1)
	require.NotNil(t, firstPage.NextCursor)
	require.NotEmpty(t, firstPage.NextCursor.SnapshotBound)
	snapshotBound := firstPage.NextCursor.SnapshotBound

	insertedAuditID := SettleCursorSafeAuditRecord(t, pool, CursorAuditSeedInput{
		TournamentID: fixture.draft.TournamentID,
		RosterID:     fixture.draft.RosterID,
		ParticipantIDs: [2]uuid.UUID{
			fixture.draft.ParticipantIDs[0], fixture.draft.ParticipantIDs[1],
		},
		SettledAt:    firstInput.CorrectedAt,
		AuditEventID: uuid.MustParse("00000000-0000-0000-0000-000000000001"),
	})
	secondInput := newCorrectionInput(ctx, t, pool, fixture, first.ResultCommit, first.Projection, 0, fixture.correction.NextTime.Add(time.Second))
	var currentProjectionID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT id
		FROM projection_revisions
		WHERE tournament_id = $1 AND roster_id = $2 AND state = 'published'`,
		fixture.draft.TournamentID, fixture.draft.RosterID).Scan(&currentProjectionID))
	secondInput.ExpectedProjectionRevisionID = currentProjectionID
	_, err = corrections.Rebuild(ctx, secondInput)
	require.NoError(t, err)

	records := append([]auditrepo.AuditRecord(nil), firstPage.Records...)
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

	fresh := listAuditRecords(ctx, t, audit, auditrepo.AuditFilter{
		TournamentID: fixture.draft.TournamentID,
		PageSize:     1,
	})
	require.Len(t, fresh, 7)
}

// SettleCursorSafeAuditRecord creates the extra series and result event that
// races with the audit cursor. It keeps series, wave, lock, and settlement
// writes explicit and leaves transaction ownership to their production APIs.
func SettleCursorSafeAuditRecord(
	tb testing.TB,
	pool *pgxpool.Pool,
	input CursorAuditSeedInput,
) uuid.UUID {
	tb.Helper()
	ctx := context.Background()
	lockedAt := input.SettledAt.Add(-time.Second)
	seriesID, err := createBackdatedAuditSeries(ctx, pool, input, lockedAt.Add(-time.Second))
	require.NoError(tb, err)
	active, err := gameseed.CreateActiveAttempt(ctx, pool, gameseed.ActiveAttemptInput{
		SeriesID: seriesID, RosterID: input.RosterID, SlotNumber: 1,
		Category: domain.CategoryWeb, CreatedAt: lockedAt.Add(-time.Second),
	})
	require.NoError(tb, err)
	_, err = resultaudit.LockSeries(ctx, pool, resultaudit.LockInput{
		Scope: resultaudit.Scope{
			TournamentID:   input.TournamentID,
			RosterID:       input.RosterID,
			SeriesID:       seriesID,
			AttemptID:      active.AttemptID,
			ParticipantIDs: input.ParticipantIDs,
		},
		LockedAt: lockedAt,
	})
	require.NoError(tb, err)

	ids := resultrepo.ResultSettlementIDs{
		CommitID: uuid.New(), ResultEventID: uuid.New(), ResultEventIdempotencyKey: uuid.New(),
		GameResultRevisionID: uuid.New(), SeriesScoreRevisionID: uuid.New(),
		AuditEventID:  input.AuditEventID,
		OutboxEventID: uuid.New(), OutboxIdempotencyKey: uuid.New(),
		ProjectionEvidenceID: uuid.New(), CommitIdempotencyKey: uuid.New(),
	}
	actorID := input.ParticipantIDs[0]
	digest := sha256.Sum256([]byte("snapshot traversal concurrent result"))
	result, changed, err := resultauthority.NewResultPostgres(postgres.NewTxManager(pool)).Settle(
		ctx,
		resultrepo.ResultSettlementInput{
			IDs: ids,
			Scope: resultrepo.ResultScope{
				TournamentID: input.TournamentID, RosterID: input.RosterID,
				SeriesID: seriesID, AttemptID: active.AttemptID,
			},
			GameState:       domain.GameStateCompleted,
			GameReason:      domain.GameResultReasonOperatorForfeit,
			GameWinnerID:    &actorID,
			Score:           domain.SeriesScore{FirstParticipantWins: 1},
			NextSeriesState: domain.SeriesStateLocked,
			ActorKind:       "operator", ActorID: &actorID,
			ProjectionArtifactKinds: []domain.ArtifactKind{
				domain.ArtifactKindGameResult, domain.ArtifactKindSeriesScore,
			},
			ProjectionPayloadDigest: digest,
			SettledAt:               input.SettledAt,
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
	pool *pgxpool.Pool,
	input CursorAuditSeedInput,
	createdAt time.Time,
) (uuid.UUID, error) {
	seriesID, err := seriesseed.CreateSeries(ctx, pool, seriesseed.Input{
		TournamentID:   input.TournamentID,
		RosterID:       input.RosterID,
		ParticipantIDs: []uuid.UUID{input.ParticipantIDs[0], input.ParticipantIDs[1]},
		Format:         "bo3",
	})
	if err != nil {
		return uuid.Nil, err
	}
	wave, err := waveseed.CreateWave(ctx, pool, waveseed.Input{
		TournamentID:   input.TournamentID,
		RosterID:       input.RosterID,
		ParticipantIDs: []uuid.UUID{input.ParticipantIDs[0], input.ParticipantIDs[1]},
		CreatedAt:      createdAt,
	})
	if err != nil {
		return uuid.Nil, err
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO wave_series (wave_id, tournament_id, roster_id, series_id, created_at)
		VALUES ($1, $2, $3, $4, $5)`,
		wave.WaveID, input.TournamentID, input.RosterID, seriesID, createdAt); err != nil {
		return uuid.Nil, err
	}
	var scoreRevisionID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT current_revision_id FROM series_score_heads WHERE series_id = $1`, seriesID).Scan(&scoreRevisionID); err != nil {
		return uuid.Nil, err
	}
	authorityID := uuid.NewSHA1(wave.WaveID, []byte("result-projection-wave-initialization"))
	payload := []byte(`{"schema":"result-projection-series-score-genesis-v1"}`)
	payloadDigest := sha256.Sum256(payload)
	if _, err := pool.Exec(ctx, `
		INSERT INTO result_projection_node_authorities (
			id, tournament_id, roster_id, source_kind, wave_id, created_at
		)
		VALUES ($1, $2, $3, 'wave_initialization', $4, $5)`,
		authorityID, input.TournamentID, input.RosterID, wave.WaveID, createdAt); err != nil {
		return uuid.Nil, err
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO result_projection_nodes (
			id, authority_id, tournament_id, roster_id, artifact_kind,
			entity_id, revision_number, previous_node_id,
			payload, payload_digest, created_at
		)
		VALUES ($1, $2, $3, $4, 'series_score', $5, 1, NULL, $6::JSON, $7, $8)`,
		scoreRevisionID, authorityID, input.TournamentID, input.RosterID,
		seriesID, payload, payloadDigest[:], createdAt); err != nil {
		return uuid.Nil, err
	}
	return seriesID, nil
}
