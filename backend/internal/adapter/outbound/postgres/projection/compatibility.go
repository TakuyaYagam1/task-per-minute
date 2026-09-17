package projection

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// PublishInitialTournamentProjection persists the deterministic projection for a new tournament.
// It is kept exported for the root facade while callers migrate to this package.
func PublishInitialTournamentProjection(
	ctx context.Context,
	tx *db.TxManager,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	createdAt time.Time,
) (*ProjectionRecord, error) {
	return publishInitialTournamentProjection(ctx, tx, tournamentID, rosterID, createdAt)
}

// CreateProjectionArtifact persists one artifact with its members and dependencies.
// It is kept exported for root-package transaction workflows during migration.
func CreateProjectionArtifact(
	ctx context.Context,
	querier *sqlc.Queries,
	in ProjectionPublishInput,
	artifact ProjectionArtifactInput,
) error {
	return createProjectionArtifact(ctx, querier, in, artifact)
}

// LoadProjectionRecord loads one projection revision and its artifact graph.
// It is kept exported for root-package readers during migration.
func LoadProjectionRecord(
	ctx context.Context,
	querier *sqlc.Queries,
	scope ProjectionScope,
	revisionID uuid.UUID,
) (*ProjectionRecord, error) {
	return loadProjectionRecord(ctx, querier, scope, revisionID)
}

// ValidateProjectionPublishInput validates a generic projection publication.
// It is kept exported for root-package workflows during migration.
func ValidateProjectionPublishInput(in ProjectionPublishInput) bool {
	return validProjectionPublishInput(in)
}

// ValidateProjectionArtifact validates one generic projection artifact.
// It is kept exported for root-package workflows during migration.
func ValidateProjectionArtifact(artifact ProjectionArtifactInput) bool {
	return validProjectionArtifact(artifact)
}

// ProjectionArtifactKind returns the persisted artifact kind value.
// It is kept exported for root-package workflows during migration.
func ProjectionArtifactKind(kind domain.ArtifactKind) string {
	return projectionArtifactKind(kind)
}

// ProjectionCASWriteError maps a failed projection compare-and-set write.
// It is kept exported for root-package workflows during migration.
func ProjectionCASWriteError(operation string, err error) error {
	return projectionCASWriteError(operation, err)
}
