package postgres

import wave "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/wave"

var ErrWaveNotFound = wave.ErrWaveNotFound

type WavePostgres = wave.WavePostgres

type WaveCreateInput = wave.WaveCreateInput

type WaveSeriesInput = wave.WaveSeriesInput

type ReadyWindowInput = wave.ReadyWindowInput

type WaveRecord = wave.WaveRecord

func NewWavePostgres(tx *TxManager) *WavePostgres {
	return wave.NewWavePostgres(tx)
}
