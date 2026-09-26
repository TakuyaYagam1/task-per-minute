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
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	publiccatalog "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/publiccatalog"
)

func (r *TournamentCatalogPostgres) ListPublicTournaments(
	ctx context.Context,
	query inbound.PublicTournamentCatalogQuery,
) (publiccatalog.PublicTournamentListPage, error) {
	if r == nil || r.tx == nil || query.Limit < 1 || query.Limit > publiccatalog.MaxPageSize ||
		!query.Group.IsValid() || !query.Sort.IsValid() {
		return publiccatalog.PublicTournamentListPage{}, domain.ErrValidation
	}

	var cursor inbound.PublicTournamentCatalogCursor
	hasCursor := query.After != nil
	cursorGroupRank := int32(0)
	if hasCursor {
		cursor = *query.After
		if cursor.GroupRank < 0 || cursor.GroupRank > 2 {
			return publiccatalog.PublicTournamentListPage{}, domain.ErrValidation
		}
		cursorGroupRank = int32(cursor.GroupRank)
	}
	rows, err := r.tx.Querier(ctx).ListPublicTournamentCatalog(ctx, sqlc.ListPublicTournamentCatalogParams{
		HasCursor:       hasCursor,
		Sort:            string(query.Sort),
		CursorGroupRank: cursorGroupRank,
		CursorCreatedAt: publicCatalogCursorTime(cursor.CreatedAt),
		CursorID:        cursor.TournamentID,
		CursorName:      cursor.Name,
		PageLimit:       int32(query.Limit + 1),
		Search:          query.Search,
		CatalogGroup:    string(query.Group),
	})
	if err != nil {
		return publiccatalog.PublicTournamentListPage{}, fmt.Errorf(
			"TournamentPublicCatalogPostgres - ListPublicTournaments - Querier.ListPublicTournamentCatalog: %w",
			err,
		)
	}

	hasMore := len(rows) > query.Limit
	if hasMore {
		rows = rows[:query.Limit]
	}
	items := make([]publiccatalog.PublicTournamentRecord, 0, len(rows))
	for _, row := range rows {
		record, mapErr := mapPublicTournamentCatalogRow(row)
		if mapErr != nil {
			return publiccatalog.PublicTournamentListPage{}, mapErr
		}
		items = append(items, record)
	}
	return publiccatalog.PublicTournamentListPage{Items: items, HasMore: hasMore}, nil
}

func (r *TournamentCatalogPostgres) GetPublicTournamentByPublicID(
	ctx context.Context,
	publicID string,
) (publiccatalog.PublicTournamentRecord, error) {
	if r == nil || r.tx == nil || publicID == "" {
		return publiccatalog.PublicTournamentRecord{}, domain.ErrValidation
	}
	row, err := r.tx.Querier(ctx).GetPublicTournamentCatalogItem(ctx, publicID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return publiccatalog.PublicTournamentRecord{}, domain.ErrTournamentNotFound
		}
		return publiccatalog.PublicTournamentRecord{}, fmt.Errorf(
			"TournamentPublicCatalogPostgres - GetPublicTournamentByPublicID - Querier.GetPublicTournamentCatalogItem: %w",
			err,
		)
	}
	return mapPublicTournamentCatalogItemRow(row)
}

func mapPublicTournamentCatalogRow(row sqlc.ListPublicTournamentCatalogRow) (publiccatalog.PublicTournamentRecord, error) {
	return mapPublicTournamentCatalogValues(
		row.TournamentID, row.PublicID, row.Name, row.OrderName, row.Preset, row.State,
		row.PublicGroup, row.PublicStage, row.PlannedRosterSize, row.RosterSize,
		row.CreatedAt, row.StartedAt, row.FinishedAt, row.ScheduledAt,
	)
}

func mapPublicTournamentCatalogItemRow(row sqlc.GetPublicTournamentCatalogItemRow) (publiccatalog.PublicTournamentRecord, error) {
	return mapPublicTournamentCatalogValues(
		row.TournamentID, row.PublicID, row.Name, row.OrderName, row.Preset, row.State,
		row.PublicGroup, row.PublicStage, row.PlannedRosterSize, row.RosterSize,
		row.CreatedAt, row.StartedAt, row.FinishedAt, row.ScheduledAt,
	)
}

func mapPublicTournamentCatalogValues(
	tournamentID uuid.UUID,
	publicID string,
	name string,
	orderName string,
	preset string,
	state string,
	group string,
	stage string,
	plannedRosterSize int32,
	rosterSize int64,
	createdAt pgtype.Timestamptz,
	startedAt pgtype.Timestamptz,
	finishedAt pgtype.Timestamptz,
	scheduledAt pgtype.Timestamptz,
) (publiccatalog.PublicTournamentRecord, error) {
	validatedRosterSize, err := catalogValidatedRosterSize(rosterSize)
	if err != nil || plannedRosterSize < 0 || plannedRosterSize > int32(domain.TournamentMaxParticipants) {
		return publiccatalog.PublicTournamentRecord{}, domain.ErrInternal
	}
	createdAtValue := createdAt.Time.UTC()
	if !createdAt.Valid || !domain.IsValidServerTime(createdAtValue) {
		return publiccatalog.PublicTournamentRecord{}, domain.ErrInternal
	}
	return publiccatalog.PublicTournamentRecord{
		TournamentID:      tournamentID,
		PublicID:          publicID,
		Name:              name,
		OrderName:         orderName,
		Preset:            domain.TournamentPreset(preset),
		State:             domain.TournamentState(state),
		Group:             inbound.PublicTournamentCatalogGroup(group),
		Stage:             domain.TournamentState(stage),
		PlannedRosterSize: int(plannedRosterSize),
		RosterSize:        validatedRosterSize,
		CreatedAt:         createdAtValue,
		StartedAt:         catalogUTCTimePointer(catalogNullableTime(startedAt)),
		FinishedAt:        catalogUTCTimePointer(catalogNullableTime(finishedAt)),
		ScheduledAt:       catalogUTCTimePointer(catalogNullableTime(scheduledAt)),
	}, nil
}

func publicCatalogCursorTime(value time.Time) pgtype.Timestamptz {
	if value.IsZero() {
		return pgtype.Timestamptz{Time: time.Unix(0, 0).UTC(), Valid: true}
	}
	return pgtype.Timestamptz{Time: value, Valid: true}
}

var _ publiccatalog.Repository = (*TournamentCatalogPostgres)(nil)
