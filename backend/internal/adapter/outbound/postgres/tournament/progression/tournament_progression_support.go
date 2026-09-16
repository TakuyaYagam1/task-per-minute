package progression

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	assignmentrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment"
	draftrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment/draft"
	waverepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/wave"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	projectionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
	resultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	executionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/execution"
	rosterrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/roster"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	projectionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
	catalogusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/catalog"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
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

type AssignmentCreateInput = assignmentrepo.AssignmentCreateInput
type DraftCreateInput = draftrepo.DraftCreateInput
type DraftAggregate = draftrepo.DraftAggregate
type AssignmentPostgres = assignmentrepo.AssignmentPostgres
type ExactNormalAssignmentPostgres = assignmentrepo.ExactNormalAssignmentPostgres
type WaveCreateInput = waverepo.WaveCreateInput
type WaveSeriesInput = waverepo.WaveSeriesInput
type ProjectionPostgres = projectionrepo.ProjectionPostgres

type ProjectionScope = projectionrepo.ProjectionScope
type ProjectionIDs = projectionrepo.ProjectionIDs
type ProjectionSource = projectionrepo.ProjectionSource
type ProjectionMemberInput = projectionrepo.ProjectionMemberInput
type ProjectionDependencyInput = projectionrepo.ProjectionDependencyInput
type ProjectionArtifactInput = projectionrepo.ProjectionArtifactInput
type ProjectionPublishInput = projectionrepo.ProjectionPublishInput
type ProjectionArtifactRecord = projectionrepo.ProjectionArtifactRecord
type ProjectionRecord = projectionrepo.ProjectionRecord

func NewAssignmentPostgres(tx *db.TxManager) *AssignmentPostgres {
	return assignmentrepo.NewAssignmentPostgres(tx)
}

func NewExactNormalAssignmentPostgres(tx *db.TxManager) *ExactNormalAssignmentPostgres {
	return assignmentrepo.NewExactNormalAssignmentPostgres(tx)
}

func NewProjectionPostgres(tx *db.TxManager) *ProjectionPostgres {
	return projectionrepo.NewProjectionPostgres(tx)
}

// PersistFinalSwissReceipt keeps the result publication bridge narrow while
// retaining the progression receipt implementation in this package.
func (r *TournamentProgressionPostgres) PersistFinalSwissReceipt(
	ctx context.Context,
	scope ProjectionScope,
	projectionID uuid.UUID,
	now time.Time,
) error {
	if r == nil || r.tx == nil || ctx == nil {
		return domain.ErrValidation
	}
	return r.persistFinalSwissReceipt(ctx, scope, projectionID, now)
}

type tournamentPreflightContent struct {
	configuration domain.ContentConfiguration
}

func loadTournamentPreflightContent(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID uuid.UUID,
) (tournamentPreflightContent, error) {
	loaded, err := rosterrepo.LoadPreflightContent(ctx, querier, tournamentID)
	if err != nil {
		return tournamentPreflightContent{}, err
	}
	return tournamentPreflightContent{configuration: loaded.Configuration}, nil
}

func loadProjectionRecord(
	ctx context.Context,
	querier *sqlc.Queries,
	scope ProjectionScope,
	revisionID uuid.UUID,
) (*ProjectionRecord, error) {
	return projectionrepo.LoadProjectionRecord(ctx, querier, scope, revisionID)
}

func validProjectionPublishInput(in ProjectionPublishInput) bool {
	return projectionrepo.ValidateProjectionPublishInput(in)
}

func nullableUUID(value *uuid.UUID) uuid.NullUUID {
	if value == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *value, Valid: true}
}

func projectionCASWriteError(operation string, err error) error {
	return projectionrepo.ProjectionCASWriteError(operation, err)
}

func swissCategoryRevisionParams(
	revision draftusecase.CategoryRevision,
	lock draftusecase.CategoryLock,
	normalPoolID uuid.UUID,
) (sqlc.CreateSwissCategoryRevisionParams, error) {
	return executionrepo.SwissCategoryRevisionParams(revision, lock, normalPoolID)
}

func createMaterializedSeriesPresence(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID, rosterID, seriesID uuid.UUID,
	participantIDs [2]uuid.UUID,
	connectedAt time.Time,
) error {
	for index, participantID := range participantIDs {
		presenceID := tournamentAdminExecutionID(seriesID, fmt.Sprintf("participant-%d-presence", index+1))
		created, err := querier.CreateSeriesPresence(ctx, sqlc.CreateSeriesPresenceParams{
			ID: presenceID, TournamentID: tournamentID, RosterID: rosterID,
			SeriesID: seriesID, ParticipantID: participantID, ConnectedAt: tstz(connectedAt),
		})
		if err != nil {
			return executionWriteError("create Series participant presence", err)
		}
		if created.ID != presenceID || created.TournamentID != tournamentID || created.RosterID != rosterID ||
			created.SeriesID != seriesID || created.ParticipantID != participantID || created.State != "connected" ||
			created.PresenceEpoch != 1 || created.Revision != 1 || created.DisconnectedAt.Valid {
			return fmt.Errorf("validate Series participant presence: %w", domain.ErrInternal)
		}
	}
	return nil
}

func tournamentAdminExecutionID(namespace uuid.UUID, role string) uuid.UUID {
	return uuid.NewSHA1(namespace, []byte(role))
}

func executionWriteError(operation string, err error) error {
	return mapRepositoryWriteError("TournamentAdminExecutionPostgres - "+operation, err)
}

func mapRepositoryWriteError(operation string, err error) error {
	return resultrepo.MapRepositoryWriteError(operation, err)
}

func nullableUUIDValue(value uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: value, Valid: value != uuid.Nil}
}

func tstz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func nullableTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time
	return &result
}

func utcTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := value.UTC()
	return &result
}

func tournamentSummaryRecord(row sqlc.GetTournamentSummaryRow) (*catalogusecase.CatalogTournamentRecord, error) {
	rosterSize, err := validatedRosterSize(row.RosterSize)
	if err != nil {
		return nil, err
	}
	record := &catalogusecase.CatalogTournamentRecord{
		ID: row.ID, RosterID: row.RosterID, Preset: domain.TournamentPreset(row.Preset),
		Name: row.Name, PublicID: row.PublicID, PlannedRosterSize: int(row.PlannedRosterSize),
		ContentRevision: row.ContentRevision,
		State:           domain.TournamentState(row.State), Revision: row.Revision, RosterSize: rosterSize,
		CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
		StartedAt: utcTimePointer(nullableTime(row.StartedAt)), FinishedAt: utcTimePointer(nullableTime(row.FinishedAt)),
	}
	if row.PausedFromState != nil {
		state := domain.TournamentState(*row.PausedFromState)
		record.PausedFromState = &state
	}
	return record, nil
}

func validatedRosterSize(value int64) (int, error) {
	if value < 0 || value > int64(domain.TournamentMaxParticipants) {
		return 0, domain.ErrInternal
	}
	return int(value), nil
}

func validLifecycleTournamentView(view inbound.TournamentView, tournamentID uuid.UUID) bool {
	if view.ID != tournamentID || view.RosterID == uuid.Nil || !view.Preset.IsValid() ||
		view.Revision < 1 || view.RosterSize < 0 || view.RosterSize > domain.TournamentMaxParticipants ||
		!domain.IsValidServerTime(view.CreatedAt) || !domain.IsValidServerTime(view.UpdatedAt) ||
		view.UpdatedAt.Before(view.CreatedAt) ||
		(domain.Tournament{State: view.State, PausedFromState: view.PausedFromState}).Validate() != nil {
		return false
	}
	return validLifecycleEventTime(view.StartedAt, view.CreatedAt, view.UpdatedAt) &&
		validLifecycleEventTime(view.FinishedAt, view.CreatedAt, view.UpdatedAt) &&
		(view.StartedAt == nil || view.FinishedAt == nil || !view.FinishedAt.Before(*view.StartedAt))
}

func validLifecycleEventTime(value *time.Time, createdAt time.Time, updatedAt time.Time) bool {
	return value == nil || domain.IsValidServerTime(*value) &&
		!value.Before(createdAt) && !value.After(updatedAt)
}

// ProgressionUUIDPointer keeps the immutable terminal mapping helper
// available to root transaction workflows during the package migration.
func ProgressionUUIDPointer(value uuid.NullUUID) *uuid.UUID {
	return progressionUUIDPointer(value)
}

// ProgressionRoundRevisionIDs keeps Swiss round authority mapping available to
// root projection materialization workflows.
func ProgressionRoundRevisionIDs(
	rounds []sqlc.LockTournamentProgressionSwissRoundsRow,
	proofs []sqlc.SwissRoundLockProof,
) map[uuid.UUID]uuid.UUID {
	return progressionRoundRevisionIDs(rounds, proofs)
}

// ProgressionCanonicalSwissInput keeps canonical standings input construction
// available to root result publication workflows.
func ProgressionCanonicalSwissInput(
	tournamentID uuid.UUID,
	roundRevisionIDs map[uuid.UUID]uuid.UUID,
	participants []sqlc.LockTournamentProgressionParticipantsRow,
	ledger []sqlc.LockTournamentProgressionSwissLedgerRow,
) (projectionusecase.CanonicalMaterializationInput, error) {
	return progressionCanonicalSwissInput(tournamentID, roundRevisionIDs, participants, ledger)
}

// ProgressionReceiptRoundProofs keeps receipt proof reconstruction available to
// root Swiss proof workflows.
func ProgressionReceiptRoundProofs(
	authority tournamentprogression.Authority,
	roots []sqlc.SwissRoundLockProof,
	members []sqlc.SwissRoundLockProofMember,
	series []sqlc.SwissRoundLockProofSeries,
) (map[uuid.UUID]swissusecase.RoundLockProof, error) {
	return progressionReceiptRoundProofs(authority, roots, members, series)
}
