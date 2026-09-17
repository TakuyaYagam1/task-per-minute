//go:build integration

package result

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/correctionseed"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	waverepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/wave"
	projectionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
	resultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	correctionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/correction"
	admincorrectionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/correction"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
)

// RunTournamentAdminCorrectionAuthorityHydratesPlayoffStage runs the moved
// authority flow against the caller-owned pool. The exported runner lets the
// root integration package retain its historical test entry point while the
// fixture and assertions live with the result capability.
func RunTournamentAdminCorrectionAuthorityHydratesPlayoffStage(
	t *testing.T,
	pool *pgxpool.Pool,
) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, resetResultTables(ctx, pool))
	t.Cleanup(func() { require.NoError(t, resetResultTables(ctx, pool)) })

	fixture, err := createCorrectionFixture(ctx, pool)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		UPDATE tournaments
		SET state = 'playoffs', started_at = created_at
		WHERE id = $1`, fixture.draft.TournamentID)
	require.NoError(t, err)

	tx := postgres.NewTxManager(pool)
	repository := admincorrectionrepo.NewTournamentAdminCorrectionPostgres(tx)
	var authority tournamentadmin.CorrectionWorkflowAuthority
	err = tx.Do(ctx, func(txCtx context.Context) error {
		var loadErr error
		authority, loadErr = repository.LockCorrectionAuthority(
			txCtx,
			fixture.draft.TournamentID,
			fixture.draft.SeriesID,
			fixture.audit.Fixture.Scope.AttemptID,
		)
		return loadErr
	})
	require.NoError(t, err)
	require.Equal(t, fixture.draft.TournamentID, authority.Stage.TournamentID)
	require.Equal(t, authority.Core.TournamentState, authority.Stage.TournamentState)
	require.Equal(t, authority.Core.TournamentRevision, authority.Stage.TournamentRevision)
}

// RunResultCorrectionRepository is the compatibility runner for the complete
// correction repository flow. It deliberately accepts explicit state instead
// of reaching into the root integration package's sharedPool.
func RunResultCorrectionRepository(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	t.Run("rebuilds the complete DAG and rolls back late failures", func(t *testing.T) {
		ctx := context.Background()
		require.NoError(t, resetResultTables(ctx, pool))
		t.Cleanup(func() { require.NoError(t, resetResultTables(ctx, pool)) })

		fixture, err := createCorrectionFixture(ctx, pool)
		require.NoError(t, err)
		repository := correctionrepo.NewCorrectionPostgres(postgres.NewTxManager(pool))
		traversal, err := repository.Traverse(ctx, fixture.correction.ResultScope, fixture.correction.Result.GameRevision.ID)
		require.NoError(t, err)
		require.True(t, traversal.SourceIsCurrent)
		require.Empty(t, traversal.CutoffCode)
		require.Len(t, traversal.Descendants, 3)

		input := newCorrectionInput(ctx, t, pool, fixture, fixture.correction.Result, fixture.correction.Projection, 1, fixture.correction.NextTime)
		record, err := repository.Rebuild(ctx, input)
		require.NoError(t, err)
		require.EqualValues(t, 2, record.ResultCommit.GameRevision.RevisionNumber)
		require.EqualValues(t, 3, record.ResultCommit.ScoreRevision.RevisionNumber)
		require.EqualValues(t, 2, record.ResultCommit.SeriesRevision.RevisionNumber)
		require.Equal(t, "tournament.result.corrected", record.ResultCommit.Audit.Action)
		require.Equal(t, input.ProjectionIDs.RevisionID, record.Projection.Revision.ID)
		require.Len(t, record.Projection.Artifacts, 3)
		require.Equal(t, []uuid.UUID{fixture.correction.WindowID}, record.ClosedReadyWindows)

		var (
			persistedOutboxID       uuid.UUID
			persistedResultEventID  uuid.UUID
			persistedSeriesID       uuid.UUID
			persistedEvidenceID     uuid.UUID
			persistedIdempotency    uuid.UUID
			persistedSequence       int64
			eventProjectionID       uuid.UUID
			eventProjectionNo       int64
			eventProjectionOrdinal  int16
			sourceProjectionID      uuid.UUID
			sourceProjectionNo      int64
			sourceProjectionOrdinal int16
		)
		require.NoError(t, pool.QueryRow(ctx, `
			SELECT event.id,
				source.result_event_id, source.series_id, source.projection_evidence_id,
				event.idempotency_key, event.sequence,
				event.projection_revision_id, event.projection_revision, event.projection_ordinal,
				source.projection_revision_id, source.projection_revision, source.projection_ordinal
			FROM outbox_events AS event
			INNER JOIN outbox_result_sources AS source ON source.outbox_event_id = event.id
			WHERE event.id = $1`, record.ResultCommit.Outbox.ID).Scan(
			&persistedOutboxID, &persistedResultEventID, &persistedSeriesID,
			&persistedEvidenceID, &persistedIdempotency, &persistedSequence,
			&eventProjectionID, &eventProjectionNo, &eventProjectionOrdinal,
			&sourceProjectionID, &sourceProjectionNo, &sourceProjectionOrdinal,
		))
		require.Equal(t, record.ResultCommit.Outbox.ID, persistedOutboxID)
		require.Equal(t, record.ResultCommit.Event.ID, persistedResultEventID)
		require.Equal(t, input.Scope.SeriesID, persistedSeriesID)
		require.Equal(t, input.IDs.ProjectionEvidenceID, persistedEvidenceID)
		require.Equal(t, input.IDs.OutboxIdempotencyKey, persistedIdempotency)
		require.Equal(t, record.ResultCommit.Outbox.Sequence, persistedSequence)
		require.Equal(t, record.Projection.Revision.ID, eventProjectionID)
		require.Equal(t, record.Projection.Revision.RevisionNumber, eventProjectionNo)
		require.GreaterOrEqual(t, eventProjectionOrdinal, int16(1))
		require.Equal(t, eventProjectionID, sourceProjectionID)
		require.Equal(t, eventProjectionNo, sourceProjectionNo)
		require.Equal(t, eventProjectionOrdinal, sourceProjectionOrdinal)

		var (
			oldProjectionState string
			oldArtifactCount   int
			windowState        string
			waveState          string
			readinessCount     int
			auditCount         int
		)
		err = pool.QueryRow(ctx, `
			SELECT state FROM projection_revisions WHERE id = $1`, fixture.correction.Projection.Revision.ID).Scan(&oldProjectionState)
		require.NoError(t, err)
		require.Equal(t, "superseded", oldProjectionState)
		err = pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM projection_artifacts WHERE produced_by_revision_id = $1`, fixture.correction.Projection.Revision.ID).Scan(&oldArtifactCount)
		require.NoError(t, err)
		require.Equal(t, 3, oldArtifactCount)
		err = pool.QueryRow(ctx, `
			SELECT ready_window.state, wave.state,
				(SELECT COUNT(*) FROM wave_readiness WHERE ready_window_id = ready_window.id)
			FROM ready_windows AS ready_window
			INNER JOIN waves AS wave ON wave.id = ready_window.wave_id
			WHERE ready_window.id = $1`, fixture.correction.WindowID).Scan(&windowState, &waveState, &readinessCount)
		require.NoError(t, err)
		require.Equal(t, "superseded", windowState)
		require.Equal(t, "superseded", waveState)
		require.Equal(t, len(fixture.correction.Participants), readinessCount)
		err = pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM audit_events WHERE tournament_id = $1`, fixture.draft.TournamentID).Scan(&auditCount)
		require.NoError(t, err)
		require.Equal(t, 2, auditCount)

		failedInput := newCorrectionInput(ctx, t, pool, fixture, record.ResultCommit, record.Projection, 0, fixture.correction.NextTime.Add(time.Second))
		failedInput.ProjectionArtifacts[0].Dependencies = append(
			failedInput.ProjectionArtifacts[0].Dependencies,
			projectionrepo.ProjectionDependencyInput{ID: uuid.New(), Kind: "artifact", DependsOnArtifactID: &failedInput.ProjectionArtifacts[0].ID},
		)
		_, err = repository.Rebuild(ctx, failedInput)
		require.Error(t, err)

		var (
			commitCount             int
			outboxCount             int
			evidenceCount           int
			outboxForAttemptCount   int
			evidenceForAttemptCount int
			currentRevisionID       uuid.UUID
			currentProjection       uuid.UUID
		)
		err = pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM result_commits WHERE attempt_id = $1`, fixture.audit.Fixture.Scope.AttemptID).Scan(&commitCount)
		require.NoError(t, err)
		require.Equal(t, 2, commitCount)
		err = pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM outbox_events WHERE id = $1`, failedInput.IDs.OutboxEventID).Scan(&outboxCount)
		require.NoError(t, err)
		require.Zero(t, outboxCount)
		err = pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM result_projection_evidence WHERE id = $1`, failedInput.IDs.ProjectionEvidenceID).Scan(&evidenceCount)
		require.NoError(t, err)
		require.Zero(t, evidenceCount)
		err = pool.QueryRow(ctx, `
			SELECT
				(SELECT COUNT(*)
				 FROM outbox_events AS event
				 INNER JOIN outbox_result_sources AS source ON source.outbox_event_id = event.id
				 INNER JOIN result_events AS result_event ON result_event.id = source.result_event_id
				 WHERE result_event.attempt_id = $1),
				(SELECT COUNT(*)
				 FROM result_projection_evidence
				 WHERE result_event_id IN (SELECT id FROM result_events WHERE attempt_id = $1))`, fixture.audit.Fixture.Scope.AttemptID).Scan(&outboxForAttemptCount, &evidenceForAttemptCount)
		require.NoError(t, err)
		require.Equal(t, 2, outboxForAttemptCount)
		require.Equal(t, 2, evidenceForAttemptCount)
		err = pool.QueryRow(ctx, `
			SELECT current_revision_id
			FROM official_result_heads
			WHERE entity_kind = 'game_attempt' AND entity_id = $1`, fixture.audit.Fixture.Scope.AttemptID).Scan(&currentRevisionID)
		require.NoError(t, err)
		require.Equal(t, record.ResultCommit.GameRevision.ID, currentRevisionID)
		err = pool.QueryRow(ctx, `
			SELECT id FROM projection_revisions
			WHERE tournament_id = $1 AND state = 'published'`, fixture.draft.TournamentID).Scan(&currentProjection)
		require.NoError(t, err)
		require.Equal(t, record.Projection.Revision.ID, currentProjection)
	})

	t.Run("rejects a started Wave cutoff without mutations", func(t *testing.T) {
		ctx := context.Background()
		require.NoError(t, resetResultTables(ctx, pool))
		t.Cleanup(func() { require.NoError(t, resetResultTables(ctx, pool)) })

		fixture, err := createCorrectionFixture(ctx, pool)
		require.NoError(t, err)
		waveRepository := waverepo.NewWavePostgres(postgres.NewTxManager(pool))
		waveRevision := fixture.correction.WaveRevision
		for _, participantID := range fixture.correction.Participants[1:] {
			record, _, err := waveRepository.MarkReady(
				ctx, fixture.draft.TournamentID, fixture.correction.WaveID,
				fixture.correction.WindowID, participantID, waveRevision,
				fixture.correction.ReadinessRevisions[participantID], fixture.correction.NextTime.Add(-time.Second),
			)
			require.NoError(t, err)
			waveRevision = record.Revision
		}
		startedAt := fixture.correction.NextTime.Add(-500 * time.Millisecond)
		_, changed, err := waveRepository.Start(
			ctx, fixture.draft.TournamentID, fixture.correction.WaveID,
			fixture.correction.WindowID, waveRevision, startedAt,
		)
		require.NoError(t, err)
		require.True(t, changed)

		repository := correctionrepo.NewCorrectionPostgres(postgres.NewTxManager(pool))
		traversal, err := repository.Traverse(ctx, fixture.correction.ResultScope, fixture.correction.Result.GameRevision.ID)
		require.NoError(t, err)
		require.Equal(t, "wave_started", traversal.CutoffCode)
		require.Empty(t, traversal.Descendants)

		input := newCorrectionInput(ctx, t, pool, fixture, fixture.correction.Result, fixture.correction.Projection, 1, fixture.correction.NextTime)
		_, err = repository.Rebuild(ctx, input)
		require.ErrorIs(t, err, correctionrepo.ErrCorrectionCutoff)
		var commitCount int
		err = pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM result_commits WHERE attempt_id = $1`, fixture.audit.Fixture.Scope.AttemptID).Scan(&commitCount)
		require.NoError(t, err)
		require.Equal(t, 1, commitCount)
	})
}

func newCorrectionInput(
	ctx context.Context,
	t *testing.T,
	pool *pgxpool.Pool,
	fixture correctionFixture,
	currentResult *resultrepo.ResultCommitRecord,
	currentProjection *projectionrepo.ProjectionRecord,
	winnerIndex int,
	correctedAt time.Time,
) correctionrepo.CorrectionInput {
	t.Helper()
	input, err := correctionseed.BuildCorrectionInput(ctx, pool, correctionseed.RebuildInput{
		Scope:             fixture.correction,
		CurrentResult:     currentResult,
		CurrentProjection: currentProjection,
		WinnerIndex:       winnerIndex,
		CorrectedAt:       correctedAt,
	})
	require.NoError(t, err)
	return input
}
