//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestCorrectionRevisionRollback(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createCorrectionRepositoryFixture(ctx, t)
	corrections := postgres.NewCorrectionPostgres(postgres.NewTxManager(sharedPool))
	input := newCorrectionInput(ctx, t, fixture, fixture.result, fixture.projection, 1, fixture.nextTime)
	corrected, err := corrections.Rebuild(ctx, input)
	require.NoError(t, err)
	require.EqualValues(t, 2, corrected.ResultCommit.GameRevision.RevisionNumber)
	require.True(t, corrected.ResultCommit.GameRevision.PreviousRevisionID.Valid)
	require.Equal(t, fixture.result.GameRevision.ID, corrected.ResultCommit.GameRevision.PreviousRevisionID.UUID)
	require.EqualValues(t, 3, corrected.ResultCommit.ScoreRevision.RevisionNumber)
	require.True(t, corrected.ResultCommit.ScoreRevision.PreviousRevisionID.Valid)
	require.Equal(t, fixture.result.ScoreRevision.ID, corrected.ResultCommit.ScoreRevision.PreviousRevisionID.UUID)
	require.NotNil(t, corrected.ResultCommit.SeriesRevision)
	require.EqualValues(t, 2, corrected.ResultCommit.SeriesRevision.RevisionNumber)
	require.True(t, corrected.Projection.Revision.PreviousRevisionID.Valid)
	require.Equal(t, fixture.projection.Revision.ID, corrected.Projection.Revision.PreviousRevisionID.UUID)

	before := loadCorrectionRevisionHeads(ctx, t, fixture)
	failedInput := newCorrectionInput(
		ctx, t,
		fixture,
		corrected.ResultCommit,
		corrected.Projection,
		0,
		fixture.nextTime.Add(time.Second),
	)
	failedInput.ProjectionArtifacts[0].Dependencies = append(
		failedInput.ProjectionArtifacts[0].Dependencies,
		postgres.ProjectionDependencyInput{
			ID: uuid.New(), Kind: "artifact",
			DependsOnArtifactID: &failedInput.ProjectionArtifacts[0].ID,
		},
	)
	_, err = corrections.Rebuild(ctx, failedInput)
	require.ErrorIs(t, err, domain.ErrConflict)

	after := loadCorrectionRevisionHeads(ctx, t, fixture)
	require.Equal(t, before, after)

	restartedResults := postgres.NewResultPostgres(postgres.NewTxManager(sharedPool))
	restartedProjections := postgres.NewProjectionPostgres(postgres.NewTxManager(sharedPool))
	scope := correctionRevisionResultScope(fixture)
	currentResult, err := restartedResults.Current(ctx, scope)
	require.NoError(t, err)
	require.Equal(t, corrected.ResultCommit.GameRevision.ID, currentResult.GameRevision.ID)
	resultHistory, err := restartedResults.History(ctx, scope)
	require.NoError(t, err)
	require.Len(t, resultHistory, 2)
	require.Equal(t, []int64{1, 2}, []int64{
		resultHistory[0].GameRevisionNumber,
		resultHistory[1].GameRevisionNumber,
	})
	require.Equal(t, []int64{2, 3}, []int64{
		resultHistory[0].ScoreRevisionNumber,
		resultHistory[1].ScoreRevisionNumber,
	})
	currentProjection, err := restartedProjections.Current(ctx, correctionRevisionProjectionScope(fixture))
	require.NoError(t, err)
	require.Equal(t, corrected.Projection.Revision.ID, currentProjection.Revision.ID)
	projectionHistory, err := restartedProjections.History(ctx, correctionRevisionProjectionScope(fixture))
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(projectionHistory), 2)
	previous := projectionHistory[len(projectionHistory)-2]
	current := projectionHistory[len(projectionHistory)-1]
	require.Equal(t, fixture.projection.Revision.ID, previous.ID)
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
	tb testing.TB,
	fixture correctionRepositoryFixture,
) correctionRevisionHeadSnapshot {
	tb.Helper()

	var snapshot correctionRevisionHeadSnapshot
	err := sharedPool.QueryRow(ctx, `
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
		fixture.resultFixture.attemptID,
		fixture.resultFixture.draft.seriesID,
		fixture.resultFixture.draft.tournamentID,
		fixture.resultFixture.draft.rosterID,
		fixture.waveID,
		fixture.windowID,
	).Scan(
		&snapshot.resultEvents,
		&snapshot.resultCommits,
		&snapshot.gameRevisions,
		&snapshot.scoreRevisions,
		&snapshot.seriesRevisions,
		&snapshot.projectionRevisions,
		&snapshot.projectionCutoffs,
		&snapshot.projectionArtifacts,
		&snapshot.projectionLinks,
		&snapshot.projectionMembers,
		&snapshot.projectionEdges,
		&snapshot.auditEvents,
		&snapshot.projectionEvidence,
		&snapshot.outboxEvents,
		&snapshot.waveState,
		&snapshot.readyWindowState,
		&snapshot.readinessRecords,
		&snapshot.gameHead,
		&snapshot.scoreHead,
		&snapshot.seriesHead,
		&snapshot.projectionHead,
	)
	require.NoError(tb, err)
	return snapshot
}

func correctionRevisionResultScope(fixture correctionRepositoryFixture) postgres.ResultScope {
	return postgres.ResultScope{
		TournamentID: fixture.resultFixture.draft.tournamentID,
		RosterID:     fixture.resultFixture.draft.rosterID,
		SeriesID:     fixture.resultFixture.draft.seriesID,
		AttemptID:    fixture.resultFixture.attemptID,
	}
}

func correctionRevisionProjectionScope(fixture correctionRepositoryFixture) postgres.ProjectionScope {
	return postgres.ProjectionScope{
		TournamentID: fixture.resultFixture.draft.tournamentID,
		RosterID:     fixture.resultFixture.draft.rosterID,
	}
}
