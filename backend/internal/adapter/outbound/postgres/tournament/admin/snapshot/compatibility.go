package snapshot

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
)

// SeriesGraph exposes the validated series indexes needed by the still-rooted
// normal-pause authority loader during the package migration.
type SeriesGraph struct {
	Values           []domain.Series
	RevisionByID     map[uuid.UUID]int64
	GameRevisionByID map[uuid.UUID]int64
	GameRosterByID   map[uuid.UUID]uuid.UUID
	GameSeriesByID   map[uuid.UUID]uuid.UUID
	SeriesRosterByID map[uuid.UUID]uuid.UUID
}

func (r *TournamentAdminSnapshotPostgres) LoadOperatorSnapshot(
	ctx context.Context,
	query tournamentadmin.SnapshotQuery,
) (tournamentadmin.OperatorSnapshotView, error) {
	return r.loadOperatorSnapshot(ctx, query)
}

func LoadSeries(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID uuid.UUID,
	roster tournamentadmin.RosterView,
) (SeriesGraph, error) {
	graph, err := tournamentAdminSnapshotLoadSeries(ctx, querier, tournamentID, roster)
	if err != nil {
		return SeriesGraph{}, err
	}
	return SeriesGraph{
		Values:           graph.values,
		RevisionByID:     graph.revisionByID,
		GameRevisionByID: graph.gameRevisionByID,
		GameRosterByID:   graph.gameRosterByID,
		GameSeriesByID:   graph.gameSeriesByID,
		SeriesRosterByID: graph.seriesRosterByID,
	}, nil
}
