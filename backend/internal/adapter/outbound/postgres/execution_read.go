package postgres

import (
	"context"

	wave "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/wave"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
)

func createWaveGenesisProjectionNode(
	ctx context.Context,
	querier *sqlc.Queries,
	waveInput WaveCreateInput,
	series WaveSeriesInput,
) error {
	return wave.CreateWaveGenesisProjectionNode(ctx, querier, waveInput, series)
}

func waveRecord(
	row sqlc.Wave,
	members []sqlc.WaveMember,
	seriesRows []sqlc.Series,
	readiness []sqlc.WaveReadiness,
	window sqlc.ReadyWindow,
	hasWindow bool,
) (*WaveRecord, error) {
	return wave.MapWaveRecord(row, members, seriesRows, readiness, window, hasWindow)
}
