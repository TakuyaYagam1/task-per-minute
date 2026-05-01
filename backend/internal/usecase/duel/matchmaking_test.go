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

func TestMatchmakingUsecase_JoinQueue_NoPairEnqueuesAndMarksQueued(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	player := &domain.Player{ID: uuid.New(), Username: "alice", Status: domain.PlayerStatusIdle}
	f.players.EXPECT().GetByID(mock.Anything, player.ID).Return(player, nil)
	f.queue.EXPECT().Enqueue(mock.Anything, player.ID).Return(nil)
	f.players.EXPECT().UpdateStatus(mock.Anything, player.ID, domain.PlayerStatusQueued).Return(withStatus(player, domain.PlayerStatusQueued), nil)
	f.queue.EXPECT().PopPair(mock.Anything).Return(uuid.Nil, uuid.Nil, false, nil)

	result, err := f.uc.JoinQueue(t.Context(), player.ID)
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestMatchmakingUsecase_JoinQueue_RejectsPlayerInDuel(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	player := &domain.Player{ID: uuid.New(), Username: "alice", Status: domain.PlayerStatusInDuel}
	f.players.EXPECT().GetByID(mock.Anything, player.ID).Return(player, nil)

	_, err := f.uc.JoinQueue(t.Context(), player.ID)
	require.ErrorIs(t, err, apperr.ErrPlayerInDuel)
}

func TestMatchmakingUsecase_LeaveQueue_RemovesAndMarksIdle(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	player := &domain.Player{ID: uuid.New(), Username: "alice", Status: domain.PlayerStatusQueued}
	f.queue.EXPECT().Remove(mock.Anything, player.ID).Return(nil)
	f.players.EXPECT().GetByID(mock.Anything, player.ID).Return(player, nil)
	f.players.EXPECT().UpdateStatus(mock.Anything, player.ID, domain.PlayerStatusIdle).Return(withStatus(player, domain.PlayerStatusIdle), nil)

	require.NoError(t, f.uc.LeaveQueue(t.Context(), player.ID))
}

type matchmakingFixture struct {
	uc      *duelusecase.MatchmakingUsecase
	queue   *usecasemocks.MockMatchmakingQueue
	players *usecasemocks.MockPlayerRepo
}

func newFixture(t *testing.T) *matchmakingFixture {
	t.Helper()

	tx := usecasemocks.NewMockTxManager(t)
	queue := usecasemocks.NewMockMatchmakingQueue(t)
	players := usecasemocks.NewMockPlayerRepo(t)
	tasks := usecasemocks.NewMockTaskRepo(t)
	history := usecasemocks.NewMockHistoryRepo(t)
	duels := usecasemocks.NewMockDuelRepo(t)
	uc := duelusecase.NewMatchmakingUsecase(
		tx,
		queue,
		players,
		tasks,
		history,
		duels,
		fixedClock{now: time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)},
	)
	return &matchmakingFixture{uc: uc, queue: queue, players: players}
}

func withStatus(player *domain.Player, status domain.PlayerStatus) *domain.Player {
	updated := *player
	updated.Status = status
	return &updated
}

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time {
	return c.now
}
