//go:build integration

package integration_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/repo/persistent"
)

func newPlayerRepo() (*persistent.PlayerPostgres, *persistent.TxManager) {
	mgr := persistent.NewTxManager(sharedPool)
	return persistent.NewPlayerPostgres(mgr), mgr
}

func TestPlayerRepo_Create_HappyPath(t *testing.T) {
	t.Parallel()
	repo, _ := newPlayerRepo()
	ctx := context.Background()
	name := uniq("alice")

	p, err := repo.Create(ctx, name)
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, p.ID)
	require.Equal(t, name, p.Username)
	require.Equal(t, domain.PlayerStatusIdle, p.Status, "default status must be idle")
	require.Nil(t, p.SessionToken, "session_token starts NULL")
	require.False(t, p.CreatedAt.IsZero())
}

func TestPlayerRepo_Create_DuplicateUsername_ReturnsErrUsernameTaken(t *testing.T) {
	t.Parallel()
	repo, _ := newPlayerRepo()
	ctx := context.Background()
	name := uniq("alice")

	_, err := repo.Create(ctx, name)
	require.NoError(t, err)

	_, err = repo.Create(ctx, name)
	require.Error(t, err)
	require.ErrorIs(t, err, apperr.ErrUsernameTaken,
		"second Create with same username must map unique violation to apperr.ErrUsernameTaken")
}

func TestPlayerRepo_JoinByUsername_QueuedPlayerResetsIdle(t *testing.T) {
	t.Parallel()
	repo, _ := newPlayerRepo()
	ctx := context.Background()
	name := uniq("alice")

	created, err := repo.JoinByUsername(ctx, name, uuid.New())
	require.NoError(t, err)

	_, err = repo.UpdateStatus(ctx, created.ID, domain.PlayerStatusQueued)
	require.NoError(t, err)

	token := uuid.New()
	joined, err := repo.JoinByUsername(ctx, name, token)
	require.NoError(t, err)
	require.Equal(t, created.ID, joined.ID)
	require.NotNil(t, joined.SessionToken)
	require.Equal(t, token, *joined.SessionToken)
	require.Equal(t, domain.PlayerStatusIdle, joined.Status,
		"session rotation must stale old queue entries by resetting queued players to idle")
}

func TestPlayerRepo_ResetQueuedToIdle_OnlyQueuedPlayers(t *testing.T) {
	pool, _ := SetupTestDB(t)
	mgr := persistent.NewTxManager(pool)
	repo := persistent.NewPlayerPostgres(mgr)
	ctx := context.Background()

	idle, err := repo.Create(ctx, uniq("idle"))
	require.NoError(t, err)
	queued, err := repo.Create(ctx, uniq("queued"))
	require.NoError(t, err)
	active, err := repo.Create(ctx, uniq("active"))
	require.NoError(t, err)
	_, err = repo.UpdateStatus(ctx, queued.ID, domain.PlayerStatusQueued)
	require.NoError(t, err)
	_, err = repo.UpdateStatus(ctx, active.ID, domain.PlayerStatusInDuel)
	require.NoError(t, err)

	reset, err := repo.ResetQueuedToIdle(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, reset)

	gotIdle, err := repo.GetByID(ctx, idle.ID)
	require.NoError(t, err)
	require.Equal(t, domain.PlayerStatusIdle, gotIdle.Status)
	gotQueued, err := repo.GetByID(ctx, queued.ID)
	require.NoError(t, err)
	require.Equal(t, domain.PlayerStatusIdle, gotQueued.Status)
	gotActive, err := repo.GetByID(ctx, active.ID)
	require.NoError(t, err)
	require.Equal(t, domain.PlayerStatusInDuel, gotActive.Status)
}

func TestPlayerRepo_GetByID(t *testing.T) {
	t.Parallel()
	repo, _ := newPlayerRepo()
	ctx := context.Background()

	created, err := repo.Create(ctx, uniq("alice"))
	require.NoError(t, err)

	got, err := repo.GetByID(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, created.ID, got.ID)
	require.Equal(t, created.Username, got.Username)
}

func TestPlayerRepo_GetByID_NotFound(t *testing.T) {
	t.Parallel()
	repo, _ := newPlayerRepo()
	_, err := repo.GetByID(context.Background(), uuid.New())
	require.ErrorIs(t, err, apperr.ErrPlayerNotFound)
}

func TestPlayerRepo_GetByUsername(t *testing.T) {
	t.Parallel()
	repo, _ := newPlayerRepo()
	ctx := context.Background()
	name := uniq("alice")

	created, err := repo.Create(ctx, name)
	require.NoError(t, err)

	got, err := repo.GetByUsername(ctx, name)
	require.NoError(t, err)
	require.Equal(t, created.ID, got.ID)
}

func TestPlayerRepo_GetByUsername_NotFound(t *testing.T) {
	t.Parallel()
	repo, _ := newPlayerRepo()
	_, err := repo.GetByUsername(context.Background(), uniq("ghost"))
	require.ErrorIs(t, err, apperr.ErrPlayerNotFound)
}

func TestPlayerRepo_UpdateSessionToken_SetThenClear(t *testing.T) {
	t.Parallel()
	repo, _ := newPlayerRepo()
	ctx := context.Background()

	p, err := repo.Create(ctx, uniq("alice"))
	require.NoError(t, err)

	token := uuid.New()
	updated, err := repo.UpdateSessionToken(ctx, p.ID, &token)
	require.NoError(t, err)
	require.NotNil(t, updated.SessionToken)
	require.Equal(t, token, *updated.SessionToken)

	bySession, err := repo.GetBySessionToken(ctx, token)
	require.NoError(t, err)
	require.Equal(t, p.ID, bySession.ID)

	cleared, err := repo.UpdateSessionToken(ctx, p.ID, nil)
	require.NoError(t, err)
	require.Nil(t, cleared.SessionToken)

	_, err = repo.GetBySessionToken(ctx, token)
	require.ErrorIs(t, err, apperr.ErrPlayerNotFound,
		"after clearing the token nobody should match it")
}

func TestPlayerRepo_UpdateSessionToken_NotFound(t *testing.T) {
	t.Parallel()
	repo, _ := newPlayerRepo()
	token := uuid.New()
	_, err := repo.UpdateSessionToken(context.Background(), uuid.New(), &token)
	require.ErrorIs(t, err, apperr.ErrPlayerNotFound)
}

func TestPlayerRepo_UpdateStatus(t *testing.T) {
	t.Parallel()
	repo, _ := newPlayerRepo()
	ctx := context.Background()

	p, err := repo.Create(ctx, uniq("alice"))
	require.NoError(t, err)

	updated, err := repo.UpdateStatus(ctx, p.ID, domain.PlayerStatusQueued)
	require.NoError(t, err)
	require.Equal(t, domain.PlayerStatusQueued, updated.Status)
}

func TestPlayerRepo_UpdateStatus_InvalidStatus(t *testing.T) {
	t.Parallel()
	repo, _ := newPlayerRepo()
	_, err := repo.UpdateStatus(context.Background(), uuid.New(), domain.PlayerStatus("offline"))
	require.ErrorIs(t, err, apperr.ErrValidation,
		"invalid enum must be rejected before hitting the DB")
}

func TestPlayerRepo_UpdateStatus_NotFound(t *testing.T) {
	t.Parallel()
	repo, _ := newPlayerRepo()
	_, err := repo.UpdateStatus(context.Background(), uuid.New(), domain.PlayerStatusIdle)
	require.ErrorIs(t, err, apperr.ErrPlayerNotFound)
}

func TestPlayerRepo_InsideTx_RollsBackOnError(t *testing.T) {
	t.Parallel()
	repo, mgr := newPlayerRepo()
	ctx := context.Background()
	a, b := uniq("alice"), uniq("bob")

	bust := apperr.ErrInternal
	err := mgr.Do(ctx, func(txCtx context.Context) error {
		_, err := repo.Create(txCtx, a)
		require.NoError(t, err)
		_, err = repo.Create(txCtx, b)
		require.NoError(t, err)
		return bust
	})
	require.ErrorIs(t, err, bust)

	_, err = repo.GetByUsername(ctx, a)
	require.ErrorIs(t, err, apperr.ErrPlayerNotFound, "tx rollback must wipe %s", a)
	_, err = repo.GetByUsername(ctx, b)
	require.ErrorIs(t, err, apperr.ErrPlayerNotFound, "tx rollback must wipe %s", b)
}
