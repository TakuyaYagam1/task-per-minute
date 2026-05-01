package duel_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	duelusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/duel"
	usecasemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/mocks"
)

func TestReadUsecase_GetDuel_ReturnsDetailForParticipant(t *testing.T) {
	t.Parallel()

	repo := usecasemocks.NewMockDuelRepo(t)
	duel := &domain.Duel{
		ID:        uuid.New(),
		Player1ID: uuid.New(),
		Player2ID: uuid.New(),
		Status:    domain.DuelStatusActive,
		Deadline:  time.Now().Add(time.Minute).UTC(),
	}
	firstTask := &domain.DuelPlayerTask{DuelID: duel.ID, PlayerID: duel.Player1ID, TaskID: uuid.New()}
	secondTask := &domain.DuelPlayerTask{DuelID: duel.ID, PlayerID: duel.Player2ID, TaskID: uuid.New()}

	repo.EXPECT().GetByID(mock.Anything, duel.ID).Return(duel, nil)
	repo.EXPECT().GetDuelPlayerTask(mock.Anything, duel.ID, duel.Player1ID).Return(firstTask, nil)
	repo.EXPECT().GetDuelPlayerTask(mock.Anything, duel.ID, duel.Player2ID).Return(secondTask, nil)

	got, err := duelusecase.NewReadUsecase(repo).GetDuel(t.Context(), duel.ID, duel.Player1ID)

	require.NoError(t, err)
	require.Same(t, duel, got.Duel)
	require.Equal(t, []*domain.DuelPlayerTask{firstTask, secondTask}, got.PlayerTasks)
}

func TestReadUsecase_GetDuel_RejectsStranger(t *testing.T) {
	t.Parallel()

	repo := usecasemocks.NewMockDuelRepo(t)
	duel := &domain.Duel{
		ID:        uuid.New(),
		Player1ID: uuid.New(),
		Player2ID: uuid.New(),
		Status:    domain.DuelStatusActive,
	}

	repo.EXPECT().GetByID(mock.Anything, duel.ID).Return(duel, nil)

	_, err := duelusecase.NewReadUsecase(repo).GetDuel(t.Context(), duel.ID, uuid.New())

	require.ErrorIs(t, err, apperr.ErrNotDuelParticipant)
}
