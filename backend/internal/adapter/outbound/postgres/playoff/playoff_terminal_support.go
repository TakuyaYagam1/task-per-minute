package playoff

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	assignmentrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment"
	draftrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment/draft"
	waverepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/wave"
	projectionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
	terminalrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/recovery/terminal"
	resultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	rosterrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/roster"
	participantdraftrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

type AssignmentCreateInput = assignmentrepo.AssignmentCreateInput
type DraftCreateInput = draftrepo.DraftCreateInput
type DraftAggregate = draftrepo.DraftAggregate
type WaveCreateInput = waverepo.WaveCreateInput
type WaveSeriesInput = waverepo.WaveSeriesInput

type ProjectionScope = projectionrepo.ProjectionScope
type ProjectionRecord = projectionrepo.ProjectionRecord
type ProjectionArtifactRecord = projectionrepo.ProjectionArtifactRecord

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

func participantDraftExecution(
	aggregate *DraftAggregate,
	targetRevision int64,
) (*draftusecase.Execution, error) {
	return participantdraftrepo.ParticipantDraftExecution(aggregate, targetRevision)
}

func recoverySeries(row sqlc.Series, graph []sqlc.ListRecoverySeriesGraphRow) (domain.Series, error) {
	return terminalrepo.RecoverySeries(row, graph)
}

func createWaveGenesisProjectionNode(
	ctx context.Context,
	querier *sqlc.Queries,
	wave WaveCreateInput,
	series WaveSeriesInput,
) error {
	return waverepo.CreateWaveGenesisProjectionNode(ctx, querier, wave, series)
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

func progressionScoreMilli(value pgtype.Numeric) (*int64, error) {
	if !value.Valid || value.Int == nil || value.NaN || value.InfinityModifier != pgtype.Finite {
		return nil, fmt.Errorf("invalid projection score")
	}
	scaled := new(big.Int).Set(value.Int)
	exponent := int64(value.Exp) + 3
	if scaled.Sign() == 0 {
		result := int64(0)
		return &result, nil
	}
	if exponent < -18 || exponent > 18 {
		return nil, fmt.Errorf("invalid projection score scale")
	}
	if exponent > 0 {
		scaled.Mul(scaled, new(big.Int).Exp(big.NewInt(10), big.NewInt(exponent), nil))
	} else if exponent < 0 {
		var remainder big.Int
		scaled.QuoRem(scaled, new(big.Int).Exp(big.NewInt(10), big.NewInt(-exponent), nil), &remainder)
		if remainder.Sign() != 0 {
			return nil, fmt.Errorf("fractional projection milli-score")
		}
	}
	if !scaled.IsInt64() {
		return nil, fmt.Errorf("projection score overflow")
	}
	result := scaled.Int64()
	return &result, nil
}

func marshalJSON(operation string, value any) ([]byte, error) {
	reflected := reflect.ValueOf(value)
	if reflected.IsValid() && reflected.Kind() == reflect.Slice && reflected.IsNil() {
		return []byte("[]"), nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%s - marshal JSON: %w", operation, err)
	}
	return data, nil
}
