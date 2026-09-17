//go:build integration

package integration_test

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/correctionseed"
	projectionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
)

func correctionProjectionArtifacts(
	participantIDs []uuid.UUID,
	sourceRevisionID uuid.UUID,
	seriesID uuid.UUID,
	version string,
) []projectionrepo.ProjectionArtifactInput {
	return correctionseed.ProjectionArtifacts(participantIDs, sourceRevisionID, seriesID, version)
}
