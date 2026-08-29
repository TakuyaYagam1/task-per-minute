package duel_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	duelusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/duel"
	duelmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/duel/mocks"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestMatchmakingReservationCAS_ArenaConflictBeforeQueueMutation(t *testing.T) {
	t.Parallel()

	f := newStrictFixture(t)
	player := &domain.Player{ID: uuid.New(), Username: "alice", Status: domain.PlayerStatusIdle}
	f.tx.EXPECT().Do(mock.Anything, mock.Anything).RunAndReturn(runTx).Once()
	f.players.EXPECT().GetByID(mock.Anything, player.ID).Return(player, nil).Once()
	f.players.EXPECT().AcquireParticipantReservation(
		mock.Anything,
		player.ID,
		domain.ParticipantReservationOwnerCasualQueue,
		player.ID,
		mock.Anything,
	).Return(nil, false, domain.ErrPlayerReserved).Once()

	result, err := f.uc.JoinQueue(t.Context(), player.ID)
	require.Nil(t, result)
	require.ErrorIs(t, err, domain.ErrPlayerReserved)
}

func TestMatchmakingReservationCAS_DuplicateQueueUsesSameClaim(t *testing.T) {
	t.Parallel()

	f := newStrictFixture(t)
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	player := &domain.Player{ID: uuid.New(), Username: "alice", Status: domain.PlayerStatusQueued}
	reservation := queueReservation(player.ID, now)
	f.tx.EXPECT().Do(mock.Anything, mock.Anything).RunAndReturn(runTx).Once()
	f.players.EXPECT().GetByID(mock.Anything, player.ID).Return(player, nil).Once()
	f.players.EXPECT().AcquireParticipantReservation(
		mock.Anything,
		player.ID,
		domain.ParticipantReservationOwnerCasualQueue,
		player.ID,
		now,
	).Return(reservation, false, nil).Once()
	f.queue.EXPECT().Enqueue(mock.Anything, player.ID).Return(nil).Once()
	f.queue.EXPECT().PopPair(mock.Anything).Return(uuid.Nil, uuid.Nil, false, nil).Once()

	result, err := f.uc.JoinQueue(t.Context(), player.ID)
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestMatchmakingReservationCAS_EnqueueFailureReleasesOwnClaim(t *testing.T) {
	t.Parallel()

	f := newStrictFixture(t)
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	player := &domain.Player{ID: uuid.New(), Username: "alice", Status: domain.PlayerStatusIdle}
	reservation := queueReservation(player.ID, now)
	queueErr := errors.New("redis unavailable")
	f.tx.EXPECT().Do(mock.Anything, mock.Anything).RunAndReturn(runTx).Twice()
	f.players.EXPECT().GetByID(mock.Anything, player.ID).Return(player, nil).Once()
	f.players.EXPECT().AcquireParticipantReservation(
		mock.Anything,
		player.ID,
		domain.ParticipantReservationOwnerCasualQueue,
		player.ID,
		now,
	).Return(reservation, true, nil).Once()
	f.players.EXPECT().UpdateStatusIfCurrent(
		mock.Anything,
		player.ID,
		domain.PlayerStatusIdle,
		domain.PlayerStatusQueued,
	).Return(withStatus(player, domain.PlayerStatusQueued), true, nil).Once()
	f.queue.EXPECT().Enqueue(mock.Anything, player.ID).Return(queueErr).Once()
	f.players.EXPECT().ReleaseParticipantReservation(mock.Anything, *reservation).Return(true, nil).Once()
	f.players.EXPECT().UpdateStatusIfCurrent(
		mock.Anything,
		player.ID,
		domain.PlayerStatusQueued,
		domain.PlayerStatusIdle,
	).Return(player, true, nil).Once()

	result, err := f.uc.JoinQueue(t.Context(), player.ID)
	require.Nil(t, result)
	require.ErrorIs(t, err, queueErr)
}

func TestMatchmakingUsecase_JoinQueue_NoPairEnqueuesAndMarksQueued(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	player := &domain.Player{ID: uuid.New(), Username: "alice", Status: domain.PlayerStatusIdle}
	f.players.EXPECT().GetByID(mock.Anything, player.ID).Return(player, nil)
	f.players.EXPECT().
		UpdateStatusIfCurrent(mock.Anything, player.ID, domain.PlayerStatusIdle, domain.PlayerStatusQueued).
		Return(withStatus(player, domain.PlayerStatusQueued), true, nil)
	f.queue.EXPECT().Enqueue(mock.Anything, player.ID).Return(nil)
	f.queue.EXPECT().PopPair(mock.Anything).Return(uuid.Nil, uuid.Nil, false, nil)

	result, err := f.uc.JoinQueue(t.Context(), player.ID)
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestMatchmakingUsecase_JoinQueue_RollsBackEnqueueWhenPopPairFails(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	player := &domain.Player{ID: uuid.New(), Username: "alice", Status: domain.PlayerStatusIdle}
	popErr := errors.New("redis unavailable")
	f.players.EXPECT().GetByID(mock.Anything, player.ID).Return(player, nil)
	f.players.EXPECT().
		UpdateStatusIfCurrent(mock.Anything, player.ID, domain.PlayerStatusIdle, domain.PlayerStatusQueued).
		Return(withStatus(player, domain.PlayerStatusQueued), true, nil)
	f.queue.EXPECT().Enqueue(mock.Anything, player.ID).Return(nil)
	f.queue.EXPECT().PopPair(mock.Anything).Return(uuid.Nil, uuid.Nil, false, popErr)
	f.queue.EXPECT().Remove(mock.Anything, player.ID).Return(nil)
	f.players.EXPECT().
		UpdateStatusIfCurrent(mock.Anything, player.ID, domain.PlayerStatusQueued, domain.PlayerStatusIdle).
		Return(withStatus(player, domain.PlayerStatusIdle), true, nil)

	result, err := f.uc.JoinQueue(t.Context(), player.ID)
	require.ErrorIs(t, err, popErr)
	require.Nil(t, result)
}

func TestMatchmakingUsecase_JoinQueue_RejectsPlayerInDuel(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	player := &domain.Player{ID: uuid.New(), Username: "alice", Status: domain.PlayerStatusInDuel}
	f.players.EXPECT().GetByID(mock.Anything, player.ID).Return(player, nil)

	_, err := f.uc.JoinQueue(t.Context(), player.ID)
	require.ErrorIs(t, err, domain.ErrPlayerInDuel)
}

func TestMatchmakingUsecase_JoinQueue_RollsBackFirstClaimWhenSecondClaimFails(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	player1 := &domain.Player{ID: uuid.New(), Username: "alice", Status: domain.PlayerStatusIdle}
	player2 := &domain.Player{ID: uuid.New(), Username: "bob", Status: domain.PlayerStatusQueued}

	f.players.EXPECT().GetByID(mock.Anything, player1.ID).Return(player1, nil).Once()
	f.players.EXPECT().
		UpdateStatusIfCurrent(mock.Anything, player1.ID, domain.PlayerStatusIdle, domain.PlayerStatusQueued).
		Return(withStatus(player1, domain.PlayerStatusQueued), true, nil).Once()
	f.queue.EXPECT().Enqueue(mock.Anything, player1.ID).Return(nil).Once()
	f.queue.EXPECT().PopPair(mock.Anything).Return(player1.ID, player2.ID, true, nil).Once()
	f.players.EXPECT().GetByID(mock.Anything, player1.ID).Return(withStatus(player1, domain.PlayerStatusQueued), nil).Once()
	f.players.EXPECT().GetByID(mock.Anything, player2.ID).Return(player2, nil).Once()
	f.players.EXPECT().
		UpdateStatusIfCurrent(mock.Anything, player1.ID, domain.PlayerStatusQueued, domain.PlayerStatusInDuel).
		Return(withStatus(player1, domain.PlayerStatusInDuel), true, nil).Once()
	f.players.EXPECT().
		UpdateStatusIfCurrent(mock.Anything, player2.ID, domain.PlayerStatusQueued, domain.PlayerStatusInDuel).
		Return(player2, false, nil).Once()
	f.players.EXPECT().
		UpdateStatusIfCurrent(mock.Anything, player1.ID, domain.PlayerStatusInDuel, domain.PlayerStatusQueued).
		Return(withStatus(player1, domain.PlayerStatusQueued), true, nil).Once()
	f.queue.EXPECT().Enqueue(mock.Anything, player1.ID).Return(nil).Once()
	f.queue.EXPECT().PopPair(mock.Anything).Return(uuid.Nil, uuid.Nil, false, nil).Once()

	result, err := f.uc.JoinQueue(t.Context(), player1.ID)
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestMatchmakingUsecase_LeaveQueue_RemovesAndMarksIdle(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	player := &domain.Player{ID: uuid.New(), Username: "alice", Status: domain.PlayerStatusQueued}
	f.queue.EXPECT().Remove(mock.Anything, player.ID).Return(nil)
	f.players.EXPECT().
		UpdateStatusIfCurrent(mock.Anything, player.ID, domain.PlayerStatusQueued, domain.PlayerStatusIdle).
		Return(withStatus(player, domain.PlayerStatusIdle), true, nil)

	require.NoError(t, f.uc.LeaveQueue(t.Context(), player.ID))
}

func TestMatchmakingUsecase_JoinQueue_PresignsSourceFileURL(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	player1 := &domain.Player{ID: uuid.New(), Username: "alice", Status: domain.PlayerStatusIdle}
	player2 := &domain.Player{ID: uuid.New(), Username: "bob", Status: domain.PlayerStatusIdle}
	task1ID := uuid.New()
	sourceKey := domain.TaskSourceFileUploadKey(task1ID, uuid.New())
	internalURL := "http://seaweed/internal/bucket/" + sourceKey
	presignedURL := "http://seaweed/public/bucket/" + sourceKey + "?X-Amz-Signature=test"
	task1 := &domain.Task{
		ID:            task1ID,
		Title:         "forensics",
		Category:      domain.CategoryForensics,
		Difficulty:    domain.DifficultyEasy,
		TimeLimit:     60,
		Flag:          "FLAG{alice}",
		Hints:         []string{"one", "two", "three"},
		SourceFileURL: &internalURL,
	}
	task2 := &domain.Task{
		ID:         uuid.New(),
		Title:      "web",
		Category:   domain.CategoryWeb,
		Difficulty: domain.DifficultyEasy,
		TimeLimit:  30,
		Flag:       "FLAG{bob}",
		Hints:      []string{"one", "two", "three"},
	}
	created := &domain.Duel{
		ID:        uuid.New(),
		Player1ID: player1.ID,
		Player2ID: player2.ID,
		Status:    domain.DuelStatusActive,
		StartedAt: now,
		Deadline:  now.Add(time.Minute),
	}

	f.players.EXPECT().GetByID(mock.Anything, player1.ID).Return(player1, nil).Once()
	f.players.EXPECT().
		UpdateStatusIfCurrent(mock.Anything, player1.ID, domain.PlayerStatusIdle, domain.PlayerStatusQueued).
		Return(withStatus(player1, domain.PlayerStatusQueued), true, nil).Once()
	f.queue.EXPECT().Enqueue(mock.Anything, player1.ID).Return(nil)
	f.queue.EXPECT().PopPair(mock.Anything).Return(player1.ID, player2.ID, true, nil)
	f.players.EXPECT().GetByID(mock.Anything, player1.ID).Return(withStatus(player1, domain.PlayerStatusQueued), nil).Once()
	f.players.EXPECT().GetByID(mock.Anything, player2.ID).Return(withStatus(player2, domain.PlayerStatusQueued), nil).Once()
	f.players.EXPECT().
		UpdateStatusIfCurrent(mock.Anything, player1.ID, domain.PlayerStatusQueued, domain.PlayerStatusInDuel).
		Return(withStatus(player1, domain.PlayerStatusInDuel), true, nil).Once()
	f.players.EXPECT().
		UpdateStatusIfCurrent(mock.Anything, player2.ID, domain.PlayerStatusQueued, domain.PlayerStatusInDuel).
		Return(withStatus(player2, domain.PlayerStatusInDuel), true, nil).Once()
	f.tasks.EXPECT().CountByDifficulty(mock.Anything, domain.DifficultyEasy).Return(int64(2), nil).Twice()
	f.tasks.EXPECT().CountSolvedByDifficulty(mock.Anything, player1.ID, domain.DifficultyEasy).Return(int64(1), nil).Once()
	f.tasks.EXPECT().CountSolvedByDifficulty(mock.Anything, player2.ID, domain.DifficultyEasy).Return(int64(0), nil).Once()
	f.tasks.EXPECT().ListByDifficulty(mock.Anything, domain.DifficultyEasy).Return([]*domain.Task{task1, task2}, nil).Times(3)
	f.history.EXPECT().ListSolvedTaskIDs(mock.Anything, player1.ID).Return([]uuid.UUID{task2.ID}, nil)
	f.history.EXPECT().ListSolvedTaskIDs(mock.Anything, player2.ID).Return(nil, nil)
	f.storage.EXPECT().PresignedGetURL(mock.Anything, sourceKey, time.Minute).Return(presignedURL, nil)
	f.duels.EXPECT().Create(mock.Anything, player1.ID, player2.ID, now.Add(time.Minute)).Return(created, nil)
	f.duels.EXPECT().CreateDuelPlayerTask(mock.Anything, created.ID, player1.ID, task1.ID).Return(nil)
	f.duels.EXPECT().CreateDuelPlayerTask(mock.Anything, created.ID, player2.ID, task1.ID).Return(nil)

	result, err := f.uc.JoinQueue(t.Context(), player1.ID)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, task1.ID, result.Player1Task.ID)
	require.Equal(t, task1.ID, result.Player2Task.ID)
	require.NotNil(t, result.Player1Task.SourceFileURL)
	require.Equal(t, presignedURL, *result.Player1Task.SourceFileURL)
	require.NotNil(t, result.Player2Task.SourceFileURL)
	require.Equal(t, presignedURL, *result.Player2Task.SourceFileURL)
	require.Equal(t, internalURL, *task1.SourceFileURL)
}

func TestMatchmakingUsecase_JoinQueue_AssignsSameSharedUnsolvedTaskWhenManyAvailable(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	player1 := &domain.Player{ID: uuid.New(), Username: "alice", Status: domain.PlayerStatusIdle}
	player2 := &domain.Player{ID: uuid.New(), Username: "bob", Status: domain.PlayerStatusIdle}
	short := &domain.Task{
		ID:         uuid.New(),
		Title:      "Привет поздравляю",
		Category:   domain.CategoryMisc,
		Difficulty: domain.DifficultyEasy,
		TimeLimit:  300,
		Flag:       "FLAG{short}",
		Hints:      []string{"one", "two", "three"},
	}
	long := &domain.Task{
		ID:         uuid.New(),
		Title:      "Помоги туристу",
		Category:   domain.CategoryOSINT,
		Difficulty: domain.DifficultyEasy,
		TimeLimit:  600,
		Flag:       "FLAG{long}",
		Hints:      []string{"one", "two", "three"},
	}
	var createdDeadline time.Time

	f.players.EXPECT().GetByID(mock.Anything, player1.ID).Return(player1, nil).Once()
	f.players.EXPECT().
		UpdateStatusIfCurrent(mock.Anything, player1.ID, domain.PlayerStatusIdle, domain.PlayerStatusQueued).
		Return(withStatus(player1, domain.PlayerStatusQueued), true, nil).Once()
	f.queue.EXPECT().Enqueue(mock.Anything, player1.ID).Return(nil)
	f.queue.EXPECT().PopPair(mock.Anything).Return(player1.ID, player2.ID, true, nil)
	f.players.EXPECT().GetByID(mock.Anything, player1.ID).Return(withStatus(player1, domain.PlayerStatusQueued), nil).Once()
	f.players.EXPECT().GetByID(mock.Anything, player2.ID).Return(withStatus(player2, domain.PlayerStatusQueued), nil).Once()
	f.players.EXPECT().
		UpdateStatusIfCurrent(mock.Anything, player1.ID, domain.PlayerStatusQueued, domain.PlayerStatusInDuel).
		Return(withStatus(player1, domain.PlayerStatusInDuel), true, nil).Once()
	f.players.EXPECT().
		UpdateStatusIfCurrent(mock.Anything, player2.ID, domain.PlayerStatusQueued, domain.PlayerStatusInDuel).
		Return(withStatus(player2, domain.PlayerStatusInDuel), true, nil).Once()
	f.tasks.EXPECT().CountByDifficulty(mock.Anything, domain.DifficultyEasy).Return(int64(2), nil).Twice()
	f.tasks.EXPECT().CountSolvedByDifficulty(mock.Anything, mock.Anything, domain.DifficultyEasy).Return(int64(0), nil).Twice()
	f.tasks.EXPECT().ListByDifficulty(mock.Anything, domain.DifficultyEasy).Return([]*domain.Task{short, long}, nil).Times(3)
	f.history.EXPECT().ListSolvedTaskIDs(mock.Anything, player1.ID).Return(nil, nil).Once()
	f.history.EXPECT().ListSolvedTaskIDs(mock.Anything, player2.ID).Return(nil, nil).Once()
	f.duels.EXPECT().
		Create(mock.Anything, player1.ID, player2.ID, mock.AnythingOfType("time.Time")).
		RunAndReturn(func(_ context.Context, _, _ uuid.UUID, deadline time.Time) (*domain.Duel, error) {
			createdDeadline = deadline
			return &domain.Duel{
				ID:        uuid.New(),
				Player1ID: player1.ID,
				Player2ID: player2.ID,
				Status:    domain.DuelStatusActive,
				StartedAt: now,
				Deadline:  deadline,
			}, nil
		})
	taskIDMatcher := mock.MatchedBy(func(id uuid.UUID) bool {
		return id == short.ID || id == long.ID
	})
	f.duels.EXPECT().CreateDuelPlayerTask(mock.Anything, mock.Anything, player1.ID, taskIDMatcher).Return(nil)
	f.duels.EXPECT().CreateDuelPlayerTask(mock.Anything, mock.Anything, player2.ID, taskIDMatcher).Return(nil)

	result, err := f.uc.JoinQueue(t.Context(), player1.ID)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, result.Player1Task.ID, result.Player2Task.ID)
	require.Equal(t, now.Add(time.Duration(result.Player1Task.TimeLimit)*time.Second), createdDeadline)
}

func TestMatchmakingUsecase_JoinQueue_AssignsOnlyCommonUnsolvedTaskToBothPlayers(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	player1 := &domain.Player{ID: uuid.New(), Username: "takuya", Status: domain.PlayerStatusIdle}
	player2 := &domain.Player{ID: uuid.New(), Username: "newbie", Status: domain.PlayerStatusIdle}
	caesar := &domain.Task{
		ID:         uuid.New(),
		Title:      "цезарь",
		Category:   domain.CategoryCrypto,
		Difficulty: domain.DifficultyEasy,
		TimeLimit:  60,
		Flag:       "FLAG{caesar}",
		Hints:      []string{"one", "two", "three"},
	}
	binary := &domain.Task{
		ID:         uuid.New(),
		Title:      "бинарь",
		Category:   domain.CategoryReverse,
		Difficulty: domain.DifficultyEasy,
		TimeLimit:  90,
		Flag:       "FLAG{binary}",
		Hints:      []string{"one", "two", "three"},
	}
	created := &domain.Duel{
		ID:        uuid.New(),
		Player1ID: player1.ID,
		Player2ID: player2.ID,
		Status:    domain.DuelStatusActive,
		StartedAt: now,
		Deadline:  now.Add(90 * time.Second),
	}

	f.players.EXPECT().GetByID(mock.Anything, player1.ID).Return(player1, nil).Once()
	f.players.EXPECT().
		UpdateStatusIfCurrent(mock.Anything, player1.ID, domain.PlayerStatusIdle, domain.PlayerStatusQueued).
		Return(withStatus(player1, domain.PlayerStatusQueued), true, nil).Once()
	f.queue.EXPECT().Enqueue(mock.Anything, player1.ID).Return(nil)
	f.queue.EXPECT().PopPair(mock.Anything).Return(player1.ID, player2.ID, true, nil)
	f.players.EXPECT().GetByID(mock.Anything, player1.ID).Return(withStatus(player1, domain.PlayerStatusQueued), nil).Once()
	f.players.EXPECT().GetByID(mock.Anything, player2.ID).Return(withStatus(player2, domain.PlayerStatusQueued), nil).Once()
	f.players.EXPECT().
		UpdateStatusIfCurrent(mock.Anything, player1.ID, domain.PlayerStatusQueued, domain.PlayerStatusInDuel).
		Return(withStatus(player1, domain.PlayerStatusInDuel), true, nil).Once()
	f.players.EXPECT().
		UpdateStatusIfCurrent(mock.Anything, player2.ID, domain.PlayerStatusQueued, domain.PlayerStatusInDuel).
		Return(withStatus(player2, domain.PlayerStatusInDuel), true, nil).Once()
	f.tasks.EXPECT().CountByDifficulty(mock.Anything, domain.DifficultyEasy).Return(int64(2), nil).Twice()
	f.tasks.EXPECT().CountSolvedByDifficulty(mock.Anything, player1.ID, domain.DifficultyEasy).Return(int64(1), nil).Once()
	f.tasks.EXPECT().CountSolvedByDifficulty(mock.Anything, player2.ID, domain.DifficultyEasy).Return(int64(0), nil).Once()
	f.tasks.EXPECT().ListByDifficulty(mock.Anything, domain.DifficultyEasy).Return([]*domain.Task{caesar, binary}, nil).Times(3)
	f.history.EXPECT().ListSolvedTaskIDs(mock.Anything, player1.ID).Return([]uuid.UUID{caesar.ID}, nil).Once()
	f.history.EXPECT().ListSolvedTaskIDs(mock.Anything, player2.ID).Return(nil, nil).Once()
	f.duels.EXPECT().Create(mock.Anything, player1.ID, player2.ID, now.Add(90*time.Second)).Return(created, nil)
	f.duels.EXPECT().CreateDuelPlayerTask(mock.Anything, created.ID, player1.ID, binary.ID).Return(nil)
	f.duels.EXPECT().CreateDuelPlayerTask(mock.Anything, created.ID, player2.ID, binary.ID).Return(nil)

	result, err := f.uc.JoinQueue(t.Context(), player1.ID)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, binary.ID, result.Player1Task.ID)
	require.Equal(t, binary.ID, result.Player2Task.ID)
}

func TestMatchmakingUsecase_JoinQueue_AssignsTaskWithoutHints(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	player1 := &domain.Player{ID: uuid.New(), Username: "alice", Status: domain.PlayerStatusIdle}
	player2 := &domain.Player{ID: uuid.New(), Username: "bob", Status: domain.PlayerStatusIdle}
	noHintTask := &domain.Task{
		ID:         uuid.New(),
		Title:      "без подсказок",
		Category:   domain.CategoryWeb,
		Difficulty: domain.DifficultyEasy,
		TimeLimit:  45,
		Flag:       "FLAG{no_hints}",
		Hints:      []string{"", "", ""},
	}
	created := &domain.Duel{
		ID:        uuid.New(),
		Player1ID: player1.ID,
		Player2ID: player2.ID,
		Status:    domain.DuelStatusActive,
		StartedAt: now,
		Deadline:  now.Add(45 * time.Second),
	}

	f.players.EXPECT().GetByID(mock.Anything, player1.ID).Return(player1, nil).Once()
	f.players.EXPECT().
		UpdateStatusIfCurrent(mock.Anything, player1.ID, domain.PlayerStatusIdle, domain.PlayerStatusQueued).
		Return(withStatus(player1, domain.PlayerStatusQueued), true, nil).Once()
	f.queue.EXPECT().Enqueue(mock.Anything, player1.ID).Return(nil)
	f.queue.EXPECT().PopPair(mock.Anything).Return(player1.ID, player2.ID, true, nil)
	f.players.EXPECT().GetByID(mock.Anything, player1.ID).Return(withStatus(player1, domain.PlayerStatusQueued), nil).Once()
	f.players.EXPECT().GetByID(mock.Anything, player2.ID).Return(withStatus(player2, domain.PlayerStatusQueued), nil).Once()
	f.players.EXPECT().
		UpdateStatusIfCurrent(mock.Anything, player1.ID, domain.PlayerStatusQueued, domain.PlayerStatusInDuel).
		Return(withStatus(player1, domain.PlayerStatusInDuel), true, nil).Once()
	f.players.EXPECT().
		UpdateStatusIfCurrent(mock.Anything, player2.ID, domain.PlayerStatusQueued, domain.PlayerStatusInDuel).
		Return(withStatus(player2, domain.PlayerStatusInDuel), true, nil).Once()
	f.tasks.EXPECT().CountByDifficulty(mock.Anything, domain.DifficultyEasy).Return(int64(1), nil).Twice()
	f.tasks.EXPECT().CountSolvedByDifficulty(mock.Anything, mock.Anything, domain.DifficultyEasy).Return(int64(0), nil).Twice()
	f.tasks.EXPECT().ListByDifficulty(mock.Anything, domain.DifficultyEasy).Return([]*domain.Task{noHintTask}, nil).Times(3)
	f.history.EXPECT().ListSolvedTaskIDs(mock.Anything, player1.ID).Return(nil, nil).Once()
	f.history.EXPECT().ListSolvedTaskIDs(mock.Anything, player2.ID).Return(nil, nil).Once()
	f.duels.EXPECT().Create(mock.Anything, player1.ID, player2.ID, now.Add(45*time.Second)).Return(created, nil)
	f.duels.EXPECT().CreateDuelPlayerTask(mock.Anything, created.ID, player1.ID, noHintTask.ID).Return(nil)
	f.duels.EXPECT().CreateDuelPlayerTask(mock.Anything, created.ID, player2.ID, noHintTask.ID).Return(nil)

	result, err := f.uc.JoinQueue(t.Context(), player1.ID)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, noHintTask.ID, result.Player1Task.ID)
	require.Equal(t, noHintTask.ID, result.Player2Task.ID)
	require.Equal(t, []string{"", "", ""}, result.Player1Task.Hints)
}

type matchmakingFixture struct {
	uc      *duelusecase.MatchmakingUseCase
	tx      *duelmocks.MockTransactionManager
	queue   *duelmocks.MockMatchmakingQueue
	players *duelmocks.MockMatchmakingPlayerRepository
	tasks   *duelmocks.MockMatchmakingTaskRepository
	history *duelmocks.MockMatchmakingHistoryRepository
	duels   *duelmocks.MockMatchmakingDuelRepository
	storage *duelmocks.MockSourceFileURLSigner
}

func newFixture(t *testing.T) *matchmakingFixture {
	t.Helper()

	f := newStrictFixture(t)
	f.tx.EXPECT().Do(mock.Anything, mock.Anything).RunAndReturn(runTx).Maybe()
	f.players.EXPECT().AcquireParticipantReservation(
		mock.Anything,
		mock.Anything,
		domain.ParticipantReservationOwnerCasualQueue,
		mock.Anything,
		mock.Anything,
	).RunAndReturn(func(
		_ context.Context,
		playerID uuid.UUID,
		_ domain.ParticipantReservationOwner,
		_ uuid.UUID,
		at time.Time,
	) (*domain.ParticipantReservation, bool, error) {
		return queueReservation(playerID, at), true, nil
	}).Maybe()
	f.players.EXPECT().GetParticipantReservation(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, playerID uuid.UUID) (*domain.ParticipantReservation, error) {
			return queueReservation(playerID, time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)), nil
		}).Maybe()
	f.players.EXPECT().PromoteParticipantReservation(
		mock.Anything,
		mock.Anything,
		domain.ParticipantReservationOwnerCasualDuel,
		mock.Anything,
		mock.Anything,
	).RunAndReturn(func(
		_ context.Context,
		expected domain.ParticipantReservation,
		_ domain.ParticipantReservationOwner,
		duelID uuid.UUID,
		at time.Time,
	) (*domain.ParticipantReservation, bool, error) {
		expected.OwnerKind = domain.ParticipantReservationOwnerCasualDuel
		expected.OwnerID = duelID
		expected.Revision++
		expected.UpdatedAt = at
		return &expected, true, nil
	}).Maybe()
	f.players.EXPECT().ReleaseParticipantReservation(mock.Anything, mock.Anything).Return(true, nil).Maybe()
	return f
}

func newStrictFixture(t *testing.T) *matchmakingFixture {
	t.Helper()

	tx := duelmocks.NewMockTransactionManager(t)
	queue := duelmocks.NewMockMatchmakingQueue(t)
	players := duelmocks.NewMockMatchmakingPlayerRepository(t)
	tasks := duelmocks.NewMockMatchmakingTaskRepository(t)
	history := duelmocks.NewMockMatchmakingHistoryRepository(t)
	duels := duelmocks.NewMockMatchmakingDuelRepository(t)
	storage := duelmocks.NewMockSourceFileURLSigner(t)
	uc := duelusecase.NewMatchmakingUseCase(
		tx,
		queue,
		players,
		tasks,
		history,
		duels,
		storage,
		fixedClock{now: time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)},
	)
	return &matchmakingFixture{uc: uc, tx: tx, queue: queue, players: players, tasks: tasks, history: history, duels: duels, storage: storage}
}

func withStatus(player *domain.Player, status domain.PlayerStatus) *domain.Player {
	updated := *player
	updated.Status = status
	return &updated
}

func queueReservation(playerID uuid.UUID, at time.Time) *domain.ParticipantReservation {
	return &domain.ParticipantReservation{
		PlayerID:      playerID,
		ReservationID: uuid.New(),
		OwnerKind:     domain.ParticipantReservationOwnerCasualQueue,
		OwnerID:       playerID,
		Revision:      1,
		AcquiredAt:    at,
		UpdatedAt:     at,
	}
}

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time {
	return c.now
}
