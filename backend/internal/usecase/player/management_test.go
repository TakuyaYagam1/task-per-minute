package player_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
	playermocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player/mocks"
)

func TestManagement_UpdatePlayerWritesAuditAndInvalidatesLeaderboard(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	playerID := uuid.New()
	before := playerusecase.PlayerRecord{
		PlayerID: playerID, Username: "alice", CreatedAt: now.Add(-time.Hour),
		Wins: 1, AverageSolveTimeMs: 1500,
	}
	after := before
	after.Username = "renamed"
	after.Wins = 3
	after.AverageSolveTimeMs = 90000
	after.StatsOverridden = true

	repo := playermocks.NewMockPlayerRepository(t)
	tx := playermocks.NewMockManagementTransactionManager(t)
	clock := playermocks.NewMockManagementClock(t)
	leaderboard := playermocks.NewMockLeaderboardInvalidator(t)
	clock.EXPECT().Now().Return(now).Once()
	tx.EXPECT().Do(mock.Anything, mock.Anything).RunAndReturn(runManagementTransaction).Once()
	repo.EXPECT().GetPlayer(mock.Anything, playerID).Return(&before, nil).Once()
	repo.EXPECT().UpdateUsername(mock.Anything, playerID, "renamed").Return(nil).Once()
	repo.EXPECT().UpsertStats(mock.Anything, playerID, playerusecase.StatsInput{
		Wins: 3, AverageSolveTimeMs: 90000,
	}, now).Return(nil).Once()
	repo.EXPECT().GetPlayer(mock.Anything, playerID).Return(&after, nil).Once()
	var audit playerusecase.AuditInput
	repo.EXPECT().CreatePlayerAudit(mock.Anything, mock.Anything).
		Run(func(_ context.Context, input playerusecase.AuditInput) { audit = input }).
		Return(nil).Once()
	leaderboard.EXPECT().Invalidate().Return().Once()

	updated, err := playerusecase.ManagementNewUseCase(tx, repo, leaderboard, clock).UpdatePlayer(
		t.Context(),
		playerID,
		playerusecase.PlayerInput{Username: "renamed", Wins: 3, AverageSolveTimeMs: 90000},
		playerusecase.Actor{Subject: "admin", JTI: "access-jti"},
	)

	require.NoError(t, err)
	require.Equal(t, "renamed", updated.Username)
	require.Equal(t, 3, updated.Wins)
	require.Equal(t, int64(90000), updated.AverageSolveTimeMs)
	require.True(t, updated.StatsOverridden)
	require.Equal(t, playerusecase.AuditActionUpdate, audit.Action)
	require.Equal(t, "admin", audit.Actor.Subject)
	require.Equal(t, "access-jti", audit.Actor.JTI)
	require.Equal(t, now, audit.CreatedAt)
	require.Equal(t, "alice", audit.BeforeState.Username)
	require.Equal(t, 1, audit.BeforeState.Wins)
	require.False(t, audit.BeforeState.StatsOverridden)
	require.False(t, audit.BeforeState.Deleted)
	require.Equal(t, "renamed", audit.AfterState.Username)
	require.Equal(t, 3, audit.AfterState.Wins)
	require.True(t, audit.AfterState.StatsOverridden)
	require.False(t, audit.AfterState.Deleted)
}

func TestManagement_DeletePlayerWritesAuditAndInvalidatesLeaderboard(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	playerID := uuid.New()
	player := playerusecase.PlayerRecord{
		PlayerID: playerID, Username: "alice", CreatedAt: now.Add(-time.Hour),
		Wins: 2, AverageSolveTimeMs: 45000, StatsOverridden: true,
	}

	repo := playermocks.NewMockPlayerRepository(t)
	tx := playermocks.NewMockManagementTransactionManager(t)
	clock := playermocks.NewMockManagementClock(t)
	leaderboard := playermocks.NewMockLeaderboardInvalidator(t)
	clock.EXPECT().Now().Return(now).Once()
	tx.EXPECT().Do(mock.Anything, mock.Anything).RunAndReturn(runManagementTransaction).Once()
	repo.EXPECT().GetPlayer(mock.Anything, playerID).Return(&player, nil).Once()
	var deletedUsername string
	repo.EXPECT().SoftDeletePlayer(mock.Anything, playerID, mock.Anything, now).
		Run(func(_ context.Context, _ uuid.UUID, username string, _ time.Time) {
			deletedUsername = username
		}).
		Return(nil).Once()
	var audit playerusecase.AuditInput
	repo.EXPECT().CreatePlayerAudit(mock.Anything, mock.Anything).
		Run(func(_ context.Context, input playerusecase.AuditInput) { audit = input }).
		Return(nil).Once()
	leaderboard.EXPECT().Invalidate().Return().Once()

	err := playerusecase.ManagementNewUseCase(tx, repo, leaderboard, clock).DeletePlayer(
		t.Context(), playerID, playerusecase.Actor{Subject: "admin", JTI: "delete-jti"},
	)

	require.NoError(t, err)
	require.True(t, strings.HasPrefix(deletedUsername, "deleted_"))
	require.Equal(t, playerusecase.AuditActionDelete, audit.Action)
	require.Equal(t, "alice", audit.BeforeState.Username)
	require.False(t, audit.BeforeState.Deleted)
	require.Equal(t, deletedUsername, audit.AfterState.Username)
	require.True(t, audit.AfterState.Deleted)
	require.Equal(t, 2, audit.AfterState.Wins)
	require.Equal(t, int64(45000), audit.AfterState.AverageSolveTimeMs)
}

func TestManagement_InvalidInputDoesNotWriteAudit(t *testing.T) {
	playerID := uuid.New()
	repo := playermocks.NewMockPlayerRepository(t)
	tx := playermocks.NewMockManagementTransactionManager(t)
	clock := playermocks.NewMockManagementClock(t)
	leaderboard := playermocks.NewMockLeaderboardInvalidator(t)
	uc := playerusecase.ManagementNewUseCase(tx, repo, leaderboard, clock)

	_, err := uc.UpdatePlayer(t.Context(), playerID, playerusecase.PlayerInput{
		Username: "alice", Wins: 1, AverageSolveTimeMs: 0,
	}, playerusecase.Actor{Subject: "admin", JTI: "access-jti"})
	require.ErrorIs(t, err, domain.ErrValidation)

	err = uc.DeletePlayer(t.Context(), playerID, playerusecase.Actor{})
	require.ErrorIs(t, err, domain.ErrInvalidCredentials)
}

func TestManagement_ListPlayerAuditFindsDeletedPlayer(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	playerID := uuid.New()
	deletedAt := now.Add(-time.Minute)
	player := playerusecase.PlayerRecord{
		PlayerID: playerID, Username: "deleted_player", CreatedAt: now.Add(-time.Hour),
		DeletedAt: &deletedAt, Wins: 2, AverageSolveTimeMs: 45000,
	}
	events := []playerusecase.AuditEvent{{
		ID: uuid.New(), Action: playerusecase.AuditActionDelete, PlayerID: playerID,
		Actor:       playerusecase.Actor{Subject: "admin", JTI: "delete-jti"},
		BeforeState: playerusecase.AuditState{Username: "deleted_player", Wins: 2},
		AfterState: playerusecase.AuditState{
			Username: "deleted_" + strings.Repeat("a", 32), Wins: 2, Deleted: true,
		},
		CreatedAt: now,
	}}
	repo := playermocks.NewMockPlayerRepository(t)
	repo.EXPECT().GetPlayerIncludingDeleted(mock.Anything, playerID).Return(&player, nil).Once()
	repo.EXPECT().ListPlayerAudit(mock.Anything, playerID, int32(200)).Return(events, nil).Once()

	got, err := playerusecase.ManagementNewUseCase(nil, repo, nil, nil).ListPlayerAudit(t.Context(), playerID, 500)

	require.NoError(t, err)
	require.Equal(t, events, got)
}

func runManagementTransaction(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}
