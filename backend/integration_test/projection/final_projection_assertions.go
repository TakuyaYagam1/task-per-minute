//go:build integration

package projection

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	publication "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection/publication"
)

// AssertFinalProjectionNotPersisted verifies the all-or-nothing boundary of a
// final publication attempt against an explicit integration pool.
func AssertFinalProjectionNotPersisted(
	ctx context.Context,
	tb testing.TB,
	pool *pgxpool.Pool,
	final publication.FinalPublication,
	expectedTournamentRevision int64,
) {
	tb.Helper()
	require.NotNil(tb, pool)

	var (
		partialWriteCounts [7]int
		tournamentState    string
		tournamentRevision int64
	)
	require.NoError(tb, pool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM projection_cutoffs WHERE id = $2),
			(SELECT COUNT(*) FROM projection_revisions WHERE id = $1),
			(SELECT COUNT(*) FROM projection_artifacts WHERE produced_by_revision_id = $1),
			(SELECT COUNT(*) FROM projection_artifact_members AS member
				JOIN projection_artifacts AS artifact ON artifact.id = member.artifact_id
				WHERE artifact.produced_by_revision_id = $1),
			(SELECT COUNT(*) FROM projection_dependencies AS dependency
				JOIN projection_artifacts AS artifact ON artifact.id = dependency.artifact_id
				WHERE artifact.produced_by_revision_id = $1),
			(SELECT COUNT(*) FROM projection_revision_artifacts WHERE revision_id = $1),
			(SELECT COUNT(*) FROM outbox_events WHERE projection_revision_id = $1)
		`, final.IDs.RevisionID, final.IDs.CutoffID).Scan(
		&partialWriteCounts[0],
		&partialWriteCounts[1],
		&partialWriteCounts[2],
		&partialWriteCounts[3],
		&partialWriteCounts[4],
		&partialWriteCounts[5],
		&partialWriteCounts[6],
	))
	require.NoError(tb, pool.QueryRow(ctx, `
		SELECT state, revision
		FROM tournaments
		WHERE id = $1`, final.Scope.TournamentID).Scan(&tournamentState, &tournamentRevision))
	require.Equal(tb, [7]int{}, partialWriteCounts)
	require.Equal(tb, string(domain.TournamentStatePlayoffs), tournamentState)
	require.Equal(tb, expectedTournamentRevision, tournamentRevision)
}
