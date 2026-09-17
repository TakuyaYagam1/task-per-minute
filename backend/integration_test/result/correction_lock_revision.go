//go:build integration

package result

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	projectionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
	resultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	resultauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/authority"
	correctionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/correction"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func runResultCorrectionRepositoryRechecksCurrentRevisionUnderLock(
	t *testing.T,
	pool *pgxpool.Pool,
) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, resetResultTables(ctx, pool))
	t.Cleanup(func() { require.NoError(t, resetResultTables(ctx, pool)) })

	fixture, err := createCorrectionFixture(ctx, pool)
	require.NoError(t, err)
	repository := correctionrepo.NewCorrectionPostgres(postgres.NewTxManager(pool))
	firstInput := newCorrectionInput(ctx, t, pool, fixture, fixture.correction.Result, fixture.correction.Projection, 1, fixture.correction.NextTime)
	secondInput := newCorrectionInput(ctx, t, pool, fixture, fixture.correction.Result, fixture.correction.Projection, 0, fixture.correction.NextTime)
	for range 2 {
		traversal, err := repository.Traverse(ctx, firstInput.Scope, firstInput.SourceRevisionID)
		require.NoError(t, err)
		require.True(t, traversal.SourceIsCurrent)
		require.Empty(t, traversal.CutoffCode)
	}

	start := make(chan struct{})
	outcomes := make(chan error, 2)
	for _, input := range []correctionrepo.CorrectionInput{firstInput, secondInput} {
		go func(in correctionrepo.CorrectionInput) {
			<-start
			_, err := repository.Rebuild(ctx, in)
			outcomes <- err
		}(input)
	}
	close(start)

	errs := []error{<-outcomes, <-outcomes}
	succeeded := 0
	conflicted := 0
	for _, err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, domain.ErrConflict):
			conflicted++
		default:
			require.NoError(t, err)
		}
	}
	require.Equal(t, 1, succeeded)
	require.Equal(t, 1, conflicted)

	var commitCount int
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM result_commits
		WHERE attempt_id = $1`, fixture.audit.Fixture.Scope.AttemptID).Scan(&commitCount)
	require.NoError(t, err)
	require.Equal(t, 2, commitCount)
}

func runCorrectionRevisionRollback(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, resetResultTables(ctx, pool))
	t.Cleanup(func() { require.NoError(t, resetResultTables(ctx, pool)) })

	fixture, err := createCorrectionFixture(ctx, pool)
	require.NoError(t, err)
	corrections := correctionrepo.NewCorrectionPostgres(postgres.NewTxManager(pool))
	input := newCorrectionInput(ctx, t, pool, fixture, fixture.correction.Result, fixture.correction.Projection, 1, fixture.correction.NextTime)
	corrected, err := corrections.Rebuild(ctx, input)
	require.NoError(t, err)
	require.EqualValues(t, 2, corrected.ResultCommit.GameRevision.RevisionNumber)
	require.True(t, corrected.ResultCommit.GameRevision.PreviousRevisionID.Valid)
	require.Equal(t, fixture.correction.Result.GameRevision.ID, corrected.ResultCommit.GameRevision.PreviousRevisionID.UUID)
	require.EqualValues(t, 3, corrected.ResultCommit.ScoreRevision.RevisionNumber)
	require.True(t, corrected.ResultCommit.ScoreRevision.PreviousRevisionID.Valid)
	require.Equal(t, fixture.correction.Result.ScoreRevision.ID, corrected.ResultCommit.ScoreRevision.PreviousRevisionID.UUID)
	require.NotNil(t, corrected.ResultCommit.SeriesRevision)
	require.EqualValues(t, 2, corrected.ResultCommit.SeriesRevision.RevisionNumber)
	require.True(t, corrected.Projection.Revision.PreviousRevisionID.Valid)
	require.Equal(t, fixture.correction.Projection.Revision.ID, corrected.Projection.Revision.PreviousRevisionID.UUID)

	before := loadCorrectionRevisionHeads(ctx, t, pool, fixture)
	failedInput := newCorrectionInput(ctx, t, pool, fixture, corrected.ResultCommit, corrected.Projection, 0, fixture.correction.NextTime.Add(time.Second))
	failedInput.ProjectionArtifacts[0].Dependencies = append(
		failedInput.ProjectionArtifacts[0].Dependencies,
		projectionrepo.ProjectionDependencyInput{ID: uuid.New(), Kind: "artifact", DependsOnArtifactID: &failedInput.ProjectionArtifacts[0].ID},
	)
	_, err = corrections.Rebuild(ctx, failedInput)
	require.ErrorIs(t, err, domain.ErrConflict)

	after := loadCorrectionRevisionHeads(ctx, t, pool, fixture)
	require.Equal(t, before, after)

	restartedResults := resultauthority.NewResultPostgres(postgres.NewTxManager(pool))
	restartedProjections := projectionrepo.NewProjectionPostgres(postgres.NewTxManager(pool))
	scope := correctionRevisionResultScope(fixture)
	currentResult, err := restartedResults.Current(ctx, scope)
	require.NoError(t, err)
	require.Equal(t, corrected.ResultCommit.GameRevision.ID, currentResult.GameRevision.ID)
	resultHistory, err := restartedResults.History(ctx, scope)
	require.NoError(t, err)
	require.Len(t, resultHistory, 2)
	require.Equal(t, []int64{1, 2}, []int64{resultHistory[0].GameRevisionNumber, resultHistory[1].GameRevisionNumber})
	require.Equal(t, []int64{2, 3}, []int64{resultHistory[0].ScoreRevisionNumber, resultHistory[1].ScoreRevisionNumber})
	currentProjection, err := restartedProjections.Current(ctx, correctionRevisionProjectionScope(fixture))
	require.NoError(t, err)
	require.Equal(t, corrected.Projection.Revision.ID, currentProjection.Revision.ID)
	projectionHistory, err := restartedProjections.History(ctx, correctionRevisionProjectionScope(fixture))
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(projectionHistory), 2)
	previous := projectionHistory[len(projectionHistory)-2]
	current := projectionHistory[len(projectionHistory)-1]
	require.Equal(t, fixture.correction.Projection.Revision.ID, previous.ID)
	require.Equal(t, corrected.Projection.Revision.ID, current.ID)
	require.Equal(t, "superseded", previous.State)
	require.Equal(t, "published", current.State)
}

type correctionRevisionHeadSnapshot struct {
	resultEvents        int
	resultCommits       int
	gameRevisions       int
	scoreRevisions      int
	seriesRevisions     int
	projectionRevisions int
	projectionCutoffs   int
	projectionArtifacts int
	projectionLinks     int
	projectionMembers   int
	projectionEdges     int
	auditEvents         int
	projectionEvidence  int
	outboxEvents        int
	waveState           string
	readyWindowState    string
	readinessRecords    int
	gameHead            uuid.UUID
	scoreHead           uuid.UUID
	seriesHead          uuid.UUID
	projectionHead      uuid.UUID
}

func loadCorrectionRevisionHeads(
	ctx context.Context,
	t *testing.T,
	pool *pgxpool.Pool,
	fixture correctionFixture,
) correctionRevisionHeadSnapshot {
	t.Helper()
	var snapshot correctionRevisionHeadSnapshot
	scope := fixture.audit.Fixture.Scope
	err := pool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM result_events WHERE attempt_id = $1),
			(SELECT COUNT(*) FROM result_commits WHERE attempt_id = $1),
			(SELECT COUNT(*) FROM official_result_revisions WHERE entity_kind = 'game_attempt' AND entity_id = $1),
			(SELECT COUNT(*) FROM series_score_revisions WHERE series_id = $2),
			(SELECT COUNT(*) FROM official_result_revisions WHERE entity_kind = 'series' AND entity_id = $2),
			(SELECT COUNT(*) FROM projection_revisions WHERE tournament_id = $3 AND roster_id = $4),
			(SELECT COUNT(*) FROM projection_cutoffs WHERE tournament_id = $3 AND roster_id = $4),
			(SELECT COUNT(*) FROM projection_artifacts WHERE tournament_id = $3 AND roster_id = $4),
			(SELECT COUNT(*) FROM projection_revision_artifacts WHERE tournament_id = $3 AND roster_id = $4),
			(SELECT COUNT(*) FROM projection_artifact_members WHERE tournament_id = $3 AND roster_id = $4),
			(SELECT COUNT(*) FROM projection_dependencies WHERE tournament_id = $3 AND roster_id = $4),
			(SELECT COUNT(*) FROM audit_events WHERE tournament_id = $3 AND roster_id = $4),
			(SELECT COUNT(*) FROM result_projection_evidence WHERE tournament_id = $3 AND roster_id = $4),
			(SELECT COUNT(*) FROM outbox_events WHERE tournament_id = $3 AND roster_id = $4),
			(SELECT state FROM waves WHERE id = $5),
			(SELECT state FROM ready_windows WHERE id = $6),
			(SELECT COUNT(*) FROM wave_readiness WHERE ready_window_id = $6),
			(SELECT current_revision_id FROM official_result_heads WHERE entity_kind = 'game_attempt' AND entity_id = $1),
			(SELECT current_revision_id FROM series_score_heads WHERE series_id = $2),
			(SELECT current_revision_id FROM official_result_heads WHERE entity_kind = 'series' AND entity_id = $2),
			(SELECT id FROM projection_revisions WHERE tournament_id = $3 AND roster_id = $4 AND state = 'published')`,
		scope.AttemptID, scope.SeriesID, scope.TournamentID, scope.RosterID,
		fixture.correction.WaveID, fixture.correction.WindowID,
	).Scan(
		&snapshot.resultEvents, &snapshot.resultCommits, &snapshot.gameRevisions,
		&snapshot.scoreRevisions, &snapshot.seriesRevisions, &snapshot.projectionRevisions,
		&snapshot.projectionCutoffs, &snapshot.projectionArtifacts, &snapshot.projectionLinks,
		&snapshot.projectionMembers, &snapshot.projectionEdges, &snapshot.auditEvents,
		&snapshot.projectionEvidence, &snapshot.outboxEvents, &snapshot.waveState,
		&snapshot.readyWindowState, &snapshot.readinessRecords, &snapshot.gameHead,
		&snapshot.scoreHead, &snapshot.seriesHead, &snapshot.projectionHead,
	)
	require.NoError(t, err)
	return snapshot
}

func correctionRevisionResultScope(fixture correctionFixture) resultrepo.ResultScope {
	scope := fixture.audit.Fixture.Scope
	return resultrepo.ResultScope{
		TournamentID: scope.TournamentID,
		RosterID:     scope.RosterID,
		SeriesID:     scope.SeriesID,
		AttemptID:    scope.AttemptID,
	}
}

func correctionRevisionProjectionScope(fixture correctionFixture) projectionrepo.ProjectionScope {
	scope := fixture.audit.Fixture.Scope
	return projectionrepo.ProjectionScope{TournamentID: scope.TournamentID, RosterID: scope.RosterID}
}
