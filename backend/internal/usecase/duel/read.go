package duel

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type ReadUseCase struct {
	duels ReadDuelRepository
}

func NewReadUseCase(duels ReadDuelRepository) *ReadUseCase {
	return &ReadUseCase{duels: duels}
}

func (u *ReadUseCase) GetDuel(ctx context.Context, duelID, playerID uuid.UUID) (*Detail, error) {
	duel, err := u.duels.GetByID(ctx, duelID)
	if err != nil {
		return nil, fmt.Errorf("ReadUsecase - GetDuel - DuelRepo.GetByID: %w", err)
	}
	if duel.Player1ID != playerID && duel.Player2ID != playerID {
		return nil, apperr.ErrNotDuelParticipant
	}

	playerTasks := make([]*domain.DuelPlayerTask, 0, 2)
	for _, participantID := range []uuid.UUID{duel.Player1ID, duel.Player2ID} {
		task, err := u.duels.GetDuelPlayerTask(ctx, duelID, participantID)
		if err != nil {
			return nil, fmt.Errorf("ReadUsecase - GetDuel - DuelRepo.GetDuelPlayerTask: %w", err)
		}
		playerTasks = append(playerTasks, task)
	}

	return &Detail{
		Duel:        duel,
		PlayerTasks: playerTasks,
	}, nil
}
