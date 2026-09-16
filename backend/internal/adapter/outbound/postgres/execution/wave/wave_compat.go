package wave

import (
	"context"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
)

// CreateWaveGenesisProjectionNode keeps the immutable score-genesis writer
// available to root-package callers that still build waves in their own
// transaction orchestration.
func CreateWaveGenesisProjectionNode(
	ctx context.Context,
	querier *sqlc.Queries,
	wave WaveCreateInput,
	series WaveSeriesInput,
) error {
	return createWaveGenesisProjectionNode(ctx, querier, wave, series)
}

// MapWaveRecord keeps wave snapshot mapping available to root-package callers
// that compose wave data with another aggregate.
func MapWaveRecord(
	row sqlc.Wave,
	members []sqlc.WaveMember,
	seriesRows []sqlc.Series,
	readiness []sqlc.WaveReadiness,
	window sqlc.ReadyWindow,
	hasWindow bool,
) (*WaveRecord, error) {
	return waveRecord(row, members, seriesRows, readiness, window, hasWindow)
}
