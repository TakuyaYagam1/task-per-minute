package admin

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestPlayerUseCase_UpdatePlayerWritesAuditAndInvalidatesLeaderboard(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	playerID := uuid.New()
	repo := newAdminPlayerRepoFake(PlayerRecord{
		PlayerID:           playerID,
		Username:           "alice",
		Status:             domain.PlayerStatusIdle,
		CreatedAt:          now.Add(-time.Hour),
		Wins:               1,
		AverageSolveTimeMs: 1500,
	})
	leaderboard := &leaderboardInvalidatorSpy{}
	uc := NewPlayerUseCase(txPassthrough{}, repo, leaderboard, fixedAdminClock{now: now})

	updated, err := uc.UpdatePlayer(t.Context(), playerID, PlayerInput{
		Username:           "renamed",
		Wins:               3,
		AverageSolveTimeMs: 90000,
	}, Actor{Subject: "admin", JTI: "access-jti"})

	require.NoError(t, err)
	require.Equal(t, "renamed", updated.Username)
	require.Equal(t, 3, updated.Wins)
	require.Equal(t, int64(90000), updated.AverageSolveTimeMs)
	require.True(t, updated.StatsOverridden)
	require.Equal(t, 1, leaderboard.invalidations)
	require.Len(t, repo.audit, 1)
	audit := repo.audit[0]
	require.Equal(t, PlayerAuditActionUpdate, audit.Action)
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

func TestPlayerUseCase_DeletePlayerWritesAuditAndInvalidatesLeaderboard(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	playerID := uuid.New()
	repo := newAdminPlayerRepoFake(PlayerRecord{
		PlayerID:           playerID,
		Username:           "alice",
		Status:             domain.PlayerStatusIdle,
		CreatedAt:          now.Add(-time.Hour),
		Wins:               2,
		AverageSolveTimeMs: 45000,
		StatsOverridden:    true,
	})
	leaderboard := &leaderboardInvalidatorSpy{}
	uc := NewPlayerUseCase(txPassthrough{}, repo, leaderboard, fixedAdminClock{now: now})

	err := uc.DeletePlayer(t.Context(), playerID, Actor{Subject: "admin", JTI: "delete-jti"})

	require.NoError(t, err)
	require.True(t, repo.deleted)
	require.Equal(t, now, repo.deletedAt)
	require.True(t, strings.HasPrefix(repo.deletedUsername, "deleted_"))
	require.Equal(t, 1, leaderboard.invalidations)
	require.Len(t, repo.audit, 1)
	audit := repo.audit[0]
	require.Equal(t, PlayerAuditActionDelete, audit.Action)
	require.Equal(t, "alice", audit.BeforeState.Username)
	require.False(t, audit.BeforeState.Deleted)
	require.Equal(t, repo.deletedUsername, audit.AfterState.Username)
	require.True(t, audit.AfterState.Deleted)
	require.Equal(t, 2, audit.AfterState.Wins)
	require.Equal(t, int64(45000), audit.AfterState.AverageSolveTimeMs)
}

func TestPlayerUseCase_InvalidInputDoesNotWriteAudit(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	playerID := uuid.New()
	repo := newAdminPlayerRepoFake(PlayerRecord{
		PlayerID:  playerID,
		Username:  "alice",
		Status:    domain.PlayerStatusIdle,
		CreatedAt: now.Add(-time.Hour),
	})
	leaderboard := &leaderboardInvalidatorSpy{}
	uc := NewPlayerUseCase(txPassthrough{}, repo, leaderboard, fixedAdminClock{now: now})

	_, err := uc.UpdatePlayer(t.Context(), playerID, PlayerInput{
		Username:           "alice",
		Wins:               1,
		AverageSolveTimeMs: 0,
	}, Actor{Subject: "admin", JTI: "access-jti"})

	require.ErrorIs(t, err, domain.ErrValidation)
	require.Empty(t, repo.audit)
	require.Equal(t, 0, leaderboard.invalidations)

	err = uc.DeletePlayer(t.Context(), playerID, Actor{})

	require.ErrorIs(t, err, domain.ErrInvalidCredentials)
	require.Empty(t, repo.audit)
	require.Equal(t, 0, leaderboard.invalidations)
}

func TestPlayerUseCase_ListPlayerAuditFindsDeletedPlayer(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	playerID := uuid.New()
	deletedAt := now.Add(-time.Minute)
	repo := newAdminPlayerRepoFake(PlayerRecord{
		PlayerID:           playerID,
		Username:           "deleted_player",
		Status:             domain.PlayerStatusIdle,
		CreatedAt:          now.Add(-time.Hour),
		DeletedAt:          &deletedAt,
		Wins:               2,
		AverageSolveTimeMs: 45000,
	})
	repo.auditEvents = []PlayerAuditEvent{{
		ID:       uuid.New(),
		Action:   PlayerAuditActionDelete,
		PlayerID: playerID,
		Actor:    Actor{Subject: "admin", JTI: "delete-jti"},
		BeforeState: PlayerAuditState{
			Username: "deleted_player",
			Wins:     2,
		},
		AfterState: PlayerAuditState{
			Username: "deleted_" + strings.Repeat("a", 32),
			Wins:     2,
			Deleted:  true,
		},
		CreatedAt: now,
	}}
	uc := NewPlayerUseCase(txPassthrough{}, repo, nil, fixedAdminClock{now: now})

	events, err := uc.ListPlayerAudit(t.Context(), playerID, 500)

	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, int32(200), repo.auditLimit)
	require.Equal(t, PlayerAuditActionDelete, events[0].Action)
}

type fixedAdminClock struct {
	now time.Time
}

func (c fixedAdminClock) Now() time.Time {
	return c.now
}

type txPassthrough struct{}

func (txPassthrough) Do(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

type leaderboardInvalidatorSpy struct {
	invalidations int
}

func (s *leaderboardInvalidatorSpy) Invalidate() {
	s.invalidations++
}

type adminPlayerRepoFake struct {
	player          PlayerRecord
	audit           []PlayerAuditInput
	auditEvents     []PlayerAuditEvent
	auditLimit      int32
	deleted         bool
	deletedUsername string
	deletedAt       time.Time
}

func newAdminPlayerRepoFake(player PlayerRecord) *adminPlayerRepoFake {
	return &adminPlayerRepoFake{player: player}
}

func (r *adminPlayerRepoFake) ListAdminPlayers(context.Context, bool) ([]PlayerRecord, error) {
	return []PlayerRecord{r.player}, nil
}

func (r *adminPlayerRepoFake) GetAdminPlayer(context.Context, uuid.UUID) (*PlayerRecord, error) {
	if r.deleted {
		return nil, domain.ErrPlayerNotFound
	}
	out := r.player
	return &out, nil
}

func (r *adminPlayerRepoFake) GetAdminPlayerIncludingDeleted(context.Context, uuid.UUID) (*PlayerRecord, error) {
	out := r.player
	return &out, nil
}

func (r *adminPlayerRepoFake) UpdateAdminPlayerUsername(_ context.Context, _ uuid.UUID, username string) error {
	r.player.Username = username
	return nil
}

func (r *adminPlayerRepoFake) UpsertAdminPlayerStats(
	_ context.Context,
	_ uuid.UUID,
	in PlayerStatsInput,
	_ time.Time,
) error {
	r.player.Wins = in.Wins
	r.player.AverageSolveTimeMs = in.AverageSolveTimeMs
	r.player.StatsOverridden = true
	return nil
}

func (r *adminPlayerRepoFake) SoftDeleteAdminPlayer(
	_ context.Context,
	_ uuid.UUID,
	deletedUsername string,
	deletedAt time.Time,
) error {
	r.deleted = true
	r.deletedUsername = deletedUsername
	r.deletedAt = deletedAt
	return nil
}

func (r *adminPlayerRepoFake) CreateAdminPlayerAudit(_ context.Context, in PlayerAuditInput) error {
	r.audit = append(r.audit, in)
	return nil
}

func (r *adminPlayerRepoFake) ListAdminPlayerAudit(
	_ context.Context,
	_ uuid.UUID,
	limit int32,
) ([]PlayerAuditEvent, error) {
	r.auditLimit = limit
	return r.auditEvents, nil
}
