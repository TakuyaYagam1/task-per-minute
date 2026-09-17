//go:build integration

package integration_test

import (
	"testing"

	"github.com/google/uuid"

	projectionintegration "github.com/TakuyaYagam1/task-per-minute/integration_test/projection"
	projectionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
)

func TestProjectionRepositoryPublishesScopedStandingsAndBracketHistory(t *testing.T) {
	projectionintegration.RunProjectionRepositoryPublishesScopedStandingsAndBracketHistory(t, sharedPool)
}

func projectionRepositoryArtifacts(
	t testing.TB,
	participantIDs []uuid.UUID,
	goldenPositionCommitID uuid.UUID,
	version string,
) []projectionrepo.ProjectionArtifactInput {
	return projectionintegration.ProjectionRepositoryArtifacts(
		t,
		participantIDs,
		goldenPositionCommitID,
		version,
	)
}
