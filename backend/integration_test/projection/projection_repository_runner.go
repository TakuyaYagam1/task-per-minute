//go:build integration

package projection

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	projectionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
)

func RunProjectionRepositoryPublishesScopedStandingsAndBracketHistory(
	t *testing.T,
	pool *pgxpool.Pool,
) {
	t.Helper()
	require.NotNil(t, pool)
	previous := sharedPool
	sharedPool = pool
	t.Cleanup(func() { sharedPool = previous })
	runProjectionRepositoryPublishesScopedStandingsAndBracketHistory(t)
}

func RunProjectionRepositoryRejectsChampionBeforeTerminalFinal(
	t *testing.T,
	pool *pgxpool.Pool,
) {
	t.Helper()
	require.NotNil(t, pool)
	previous := sharedPool
	sharedPool = pool
	t.Cleanup(func() { sharedPool = previous })
	runProjectionRepositoryRejectsChampionBeforeTerminalFinal(t)
}

func ProjectionRepositoryArtifacts(
	t testing.TB,
	participantIDs []uuid.UUID,
	goldenPositionCommitID uuid.UUID,
	version string,
) []projectionrepo.ProjectionArtifactInput {
	return projectionRepositoryArtifacts(t, participantIDs, goldenPositionCommitID, version)
}
