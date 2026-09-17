package catalog

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	catalogusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/catalog"
)

// CreateTournamentDraft is the catalog usecase repository entry point. It
// deliberately composes the child base writer so content binding and draft
// creation stay in one transaction owned by this capability.
func (r *TournamentCatalogPostgres) CreateTournamentDraft(
	ctx context.Context,
	command catalogusecase.TournamentCreateCommand,
	createdAt time.Time,
) (*catalogusecase.CatalogTournamentRecord, *catalogusecase.CatalogRosterRecord, error) {
	if r == nil || r.tx == nil {
		return nil, nil, domain.ErrValidation
	}
	tournament, roster, err := r.Create(ctx, TournamentCreateInput{
		ID: command.TournamentID, RosterID: command.RosterID, Name: command.Name, PublicID: command.PublicID,
		PlannedRosterSize: command.PlannedRosterSize, ContentRevision: command.ContentRevision, CreatedAt: createdAt,
	})
	if err != nil {
		return nil, nil, err
	}
	return catalogTournamentRecord(tournament, roster.ID, 0), catalogRosterRecord(roster), nil
}

func (r *TournamentCatalogPostgres) GetTournament(
	ctx context.Context,
	id uuid.UUID,
) (*catalogusecase.CatalogTournamentRecord, error) {
	if r == nil || r.tx == nil || id == uuid.Nil {
		return nil, domain.ErrValidation
	}
	row, err := r.tx.Querier(ctx).GetTournamentSummary(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, catalogusecase.ErrTournamentNotFound
		}
		return nil, fmt.Errorf("TournamentPostgres - GetTournament - Querier.GetTournamentSummary: %w", err)
	}
	return catalogTournamentSummaryRecord(row)
}

func (r *TournamentCatalogPostgres) ListTournaments(
	ctx context.Context,
) ([]catalogusecase.CatalogTournamentRecord, error) {
	if r == nil || r.tx == nil {
		return nil, domain.ErrValidation
	}
	rows, err := r.tx.Querier(ctx).ListTournamentSummaries(ctx)
	if err != nil {
		return nil, fmt.Errorf(
			"TournamentPostgres - ListTournaments - Querier.ListTournamentSummaries: %w",
			err,
		)
	}
	out := make([]catalogusecase.CatalogTournamentRecord, 0, len(rows))
	for _, row := range rows {
		record, mapErr := catalogTournamentListSummaryRecord(row)
		if mapErr != nil {
			return nil, mapErr
		}
		out = append(out, *record)
	}
	return out, nil
}

func catalogTournamentRecord(
	row sqlc.Tournament,
	rosterID uuid.UUID,
	rosterSize int,
) *catalogusecase.CatalogTournamentRecord {
	record := &catalogusecase.CatalogTournamentRecord{
		ID: row.ID, RosterID: rosterID, Preset: domain.TournamentPreset(row.Preset),
		Name: row.Name, PublicID: row.PublicID, PlannedRosterSize: int(row.PlannedRosterSize),
		ContentRevision: row.ContentRevision, State: domain.TournamentState(row.State),
		Revision: row.Revision, RosterSize: rosterSize,
		CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
		StartedAt:  catalogUTCTimePointer(catalogNullableTime(row.StartedAt)),
		FinishedAt: catalogUTCTimePointer(catalogNullableTime(row.FinishedAt)),
	}
	if row.PausedFromState != nil {
		state := domain.TournamentState(*row.PausedFromState)
		record.PausedFromState = &state
	}
	return record
}

func catalogRosterRecord(row sqlc.Roster) *catalogusecase.CatalogRosterRecord {
	return &catalogusecase.CatalogRosterRecord{ID: row.ID, TournamentID: row.TournamentID}
}

func catalogTournamentSummaryRecord(
	row sqlc.GetTournamentSummaryRow,
) (*catalogusecase.CatalogTournamentRecord, error) {
	rosterSize, err := catalogValidatedRosterSize(row.RosterSize)
	if err != nil {
		return nil, err
	}
	record := &catalogusecase.CatalogTournamentRecord{
		ID: row.ID, RosterID: row.RosterID, Preset: domain.TournamentPreset(row.Preset),
		Name: row.Name, PublicID: row.PublicID, PlannedRosterSize: int(row.PlannedRosterSize),
		ContentRevision: row.ContentRevision, State: domain.TournamentState(row.State),
		Revision: row.Revision, RosterSize: rosterSize,
		CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
		StartedAt:  catalogUTCTimePointer(catalogNullableTime(row.StartedAt)),
		FinishedAt: catalogUTCTimePointer(catalogNullableTime(row.FinishedAt)),
	}
	if row.PausedFromState != nil {
		state := domain.TournamentState(*row.PausedFromState)
		record.PausedFromState = &state
	}
	return record, nil
}

func catalogTournamentListSummaryRecord(
	row sqlc.ListTournamentSummariesRow,
) (*catalogusecase.CatalogTournamentRecord, error) {
	return catalogTournamentSummaryRecord(sqlc.GetTournamentSummaryRow(row))
}

func catalogValidatedRosterSize(value int64) (int, error) {
	if value < 0 || value > int64(domain.TournamentMaxParticipants) {
		return 0, domain.ErrInternal
	}
	return int(value), nil
}

func catalogNullableTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time
	return &result
}

func catalogUTCTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := value.UTC()
	return &normalized
}

var _ catalogusecase.TournamentRepository = (*TournamentCatalogPostgres)(nil)
