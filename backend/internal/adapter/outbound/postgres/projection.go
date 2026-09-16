package postgres

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	projectionpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	projectionSourceInitial          = "initial"
	projectionSourceOfficialResult   = "official_result"
	projectionSourceGoldenPosition   = "golden_position"
	projectionSourceOperatorRebuild  = "operator_rebuild"
	projectionSourceStageProgression = "stage_progression"

	projectionDependencyArtifact       = "artifact"
	projectionDependencyOfficialResult = "official_result"
	projectionDependencyGoldenPosition = "golden_position"
)

var ErrProjectionNotFound = projectionpostgres.ErrProjectionNotFound

type ProjectionPostgres = projectionpostgres.ProjectionPostgres
type ProjectionScope = projectionpostgres.ProjectionScope
type ProjectionIDs = projectionpostgres.ProjectionIDs

// ProjectionSource retains StageProgressionCommandID through the child alias.
type ProjectionSource = projectionpostgres.ProjectionSource
type ProjectionMemberInput = projectionpostgres.ProjectionMemberInput
type ProjectionDependencyInput = projectionpostgres.ProjectionDependencyInput
type ProjectionArtifactInput = projectionpostgres.ProjectionArtifactInput
type ProjectionPublishInput = projectionpostgres.ProjectionPublishInput
type ProjectionArtifactRecord = projectionpostgres.ProjectionArtifactRecord
type ProjectionRecord = projectionpostgres.ProjectionRecord

func NewProjectionPostgres(tx *TxManager) *ProjectionPostgres {
	return projectionpostgres.NewProjectionPostgres(tx)
}

func publishInitialTournamentProjection(
	ctx context.Context,
	tx *TxManager,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	createdAt time.Time,
) (*ProjectionRecord, error) {
	return projectionpostgres.PublishInitialTournamentProjection(ctx, tx, tournamentID, rosterID, createdAt)
}

func createProjectionArtifact(
	ctx context.Context,
	querier *sqlc.Queries,
	in ProjectionPublishInput,
	artifact ProjectionArtifactInput,
) error {
	return projectionpostgres.CreateProjectionArtifact(ctx, querier, in, artifact)
}

func loadProjectionRecord(
	ctx context.Context,
	querier *sqlc.Queries,
	scope ProjectionScope,
	revisionID uuid.UUID,
) (*ProjectionRecord, error) {
	return projectionpostgres.LoadProjectionRecord(ctx, querier, scope, revisionID)
}

func validProjectionPublishInput(in ProjectionPublishInput) bool {
	return projectionpostgres.ValidateProjectionPublishInput(in)
}

func validProjectionArtifact(artifact ProjectionArtifactInput) bool {
	return projectionpostgres.ValidateProjectionArtifact(artifact)
}

func projectionArtifactKind(kind domain.ArtifactKind) string {
	return projectionpostgres.ProjectionArtifactKind(kind)
}

func projectionCASWriteError(operation string, err error) error {
	return projectionpostgres.ProjectionCASWriteError(operation, err)
}

func validTrimmedText(value string) bool {
	return value != "" && strings.TrimSpace(value) == value
}
