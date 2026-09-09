package postgres

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
)

func resolveMarkReadyConflict(
	ctx context.Context,
	querier *sqlc.Queries,
	waveID uuid.UUID,
	windowID uuid.UUID,
	participantID uuid.UUID,
) (bool, error) {
	head, err := querier.GetWaveReadinessHead(ctx, sqlc.GetWaveReadinessHeadParams{
		WaveID: waveID, ParticipantID: participantID,
	})
	if err != nil {
		return false, err
	}
	if head.ReadyWindowID.Valid && head.ReadyWindowID.UUID == windowID && head.Ready {
		return true, nil
	}
	return false, errWaveCAS
}
