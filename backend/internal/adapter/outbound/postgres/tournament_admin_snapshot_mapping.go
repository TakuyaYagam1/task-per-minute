package postgres

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	snapshotpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/snapshot"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
)

type tournamentAdminSnapshotSeriesGraph struct {
	values           []domain.Series
	revisionByID     map[uuid.UUID]int64
	gameRevisionByID map[uuid.UUID]int64
	gameRosterByID   map[uuid.UUID]uuid.UUID
	gameSeriesByID   map[uuid.UUID]uuid.UUID
	seriesRosterByID map[uuid.UUID]uuid.UUID
}

func tournamentAdminSnapshotLoadSeries(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID uuid.UUID,
	roster tournamentadmin.RosterView,
) (tournamentAdminSnapshotSeriesGraph, error) {
	graph, err := snapshotpostgres.LoadSeries(ctx, querier, tournamentID, roster)
	if err != nil {
		return tournamentAdminSnapshotSeriesGraph{}, err
	}
	return tournamentAdminSnapshotSeriesGraph{
		values:           graph.Values,
		revisionByID:     graph.RevisionByID,
		gameRevisionByID: graph.GameRevisionByID,
		gameRosterByID:   graph.GameRosterByID,
		gameSeriesByID:   graph.GameSeriesByID,
		seriesRosterByID: graph.SeriesRosterByID,
	}, nil
}

func tournamentAdminSnapshotWaveByID(
	waves []tournamentadmin.WaveView,
	id uuid.UUID,
) (tournamentadmin.WaveView, bool) {
	for _, wave := range waves {
		if wave.Wave.ID == id {
			return wave, true
		}
	}
	return tournamentadmin.WaveView{}, false
}

func tournamentAdminSnapshotCurrentGame(series domain.Series) (*domain.Game, bool) {
	for slotIndex := len(series.Slots) - 1; slotIndex >= 0; slotIndex-- {
		slot := series.Slots[slotIndex]
		if len(slot.Attempts) == 0 {
			continue
		}
		game := slot.Attempts[len(slot.Attempts)-1]
		return &game, true
	}
	return nil, false
}
