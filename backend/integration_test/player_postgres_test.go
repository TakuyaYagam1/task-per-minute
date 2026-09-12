//go:build integration

package integration_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func newPlayerRepo(pools ...*pgxpool.Pool) (*postgres.PlayerPostgres, *postgres.TxManager) {
	pool := sharedPool
	if len(pools) > 0 && pools[0] != nil {
		pool = pools[0]
	}
	mgr := postgres.NewTxManager(pool)
	return postgres.NewPlayerPostgres(mgr), mgr
}

func TestPlayerRepo_Create_HappyPath(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	repo, _ := newPlayerRepo(pool)
	ctx := context.Background()
	name := uniq("alice")

	p, err := repo.Create(ctx, name)
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, p.ID)
	require.Equal(t, name, p.Username)
	require.Nil(t, p.SessionToken, "session_token starts NULL")
	require.False(t, p.CreatedAt.IsZero())
}

func TestPlayerRepo_Create_DuplicateUsername_ReturnsErrUsernameTaken(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	repo, _ := newPlayerRepo(pool)
	ctx := context.Background()
	name := uniq("alice")

	_, err := repo.Create(ctx, name)
	require.NoError(t, err)

	_, err = repo.Create(ctx, name)
	require.Error(t, err)
	require.ErrorIs(t, err, domain.ErrUsernameTaken,
		"second Create with same username must map unique violation to domain.ErrUsernameTaken")
}

func TestPlayerRepo_GetByID(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	repo, _ := newPlayerRepo(pool)
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
	pool := newParallelTestDB(t)
	repo, _ := newPlayerRepo(pool)
	_, err := repo.GetByID(context.Background(), uuid.New())
	require.ErrorIs(t, err, domain.ErrPlayerNotFound)
}

func TestPlayerRepo_GetByUsername(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	repo, _ := newPlayerRepo(pool)
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
	pool := newParallelTestDB(t)
	repo, _ := newPlayerRepo(pool)
	_, err := repo.GetByUsername(context.Background(), uniq("ghost"))
	require.ErrorIs(t, err, domain.ErrPlayerNotFound)
}

func TestPlayerRepo_UpdateSessionToken_SetThenClear(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	repo, _ := newPlayerRepo(pool)
	ctx := context.Background()

	p, err := repo.Create(ctx, uniq("alice"))
	require.NoError(t, err)

	token := uuid.New()
	expiresAt := time.Now().Add(time.Hour).UTC()
	updated, err := repo.UpdateSessionToken(ctx, p.ID, &token, &expiresAt)
	require.NoError(t, err)
	require.NotNil(t, updated.SessionToken)
	require.Equal(t, token, *updated.SessionToken)
	require.NotNil(t, updated.SessionExpiresAt)
	require.WithinDuration(t, expiresAt, *updated.SessionExpiresAt, time.Millisecond)

	bySession, err := repo.GetBySessionToken(ctx, token)
	require.NoError(t, err)
	require.Equal(t, p.ID, bySession.ID)

	cleared, err := repo.UpdateSessionToken(ctx, p.ID, nil, nil)
	require.NoError(t, err)
	require.Nil(t, cleared.SessionToken)
	require.Nil(t, cleared.SessionExpiresAt)

	_, err = repo.GetBySessionToken(ctx, token)
	require.ErrorIs(t, err, domain.ErrPlayerNotFound,
		"after clearing the token nobody should match it")
}

func TestPlayerRepo_GetBySessionToken_ExpiredSessionClearsToken(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	repo, _ := newPlayerRepo(pool)
	ctx := context.Background()

	p, err := repo.Create(ctx, uniq("alice"))
	require.NoError(t, err)

	token := uuid.New()
	expiresAt := time.Now().Add(-time.Minute).UTC()
	_, err = repo.UpdateSessionToken(ctx, p.ID, &token, &expiresAt)
	require.NoError(t, err)

	_, err = repo.GetBySessionToken(ctx, token)
	require.ErrorIs(t, err, domain.ErrPlayerNotFound)

	cleared, err := repo.GetByID(ctx, p.ID)
	require.NoError(t, err)
	require.Nil(t, cleared.SessionToken)
	require.Nil(t, cleared.SessionExpiresAt)
}

func TestPlayerRepo_GetBySessionToken_NullExpiryIsExpired(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	repo, _ := newPlayerRepo(pool)
	ctx := context.Background()

	p, err := repo.Create(ctx, uniq("alice"))
	require.NoError(t, err)

	token := uuid.New()
	_, err = repo.UpdateSessionToken(ctx, p.ID, &token, nil)
	require.NoError(t, err)

	_, err = repo.GetBySessionToken(ctx, token)
	require.ErrorIs(t, err, domain.ErrPlayerNotFound)
}

func TestPlayerRepo_UpdateSessionToken_NotFound(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	repo, _ := newPlayerRepo(pool)
	token := uuid.New()
	expiresAt := time.Now().Add(time.Hour).UTC()
	_, err := repo.UpdateSessionToken(context.Background(), uuid.New(), &token, &expiresAt)
	require.ErrorIs(t, err, domain.ErrPlayerNotFound)
}

func TestPlayerRepo_JoinByUsername_RejectsActiveSession(t *testing.T) {
	t.Parallel()

	pool := newParallelTestDB(t)
	repo, _ := newPlayerRepo(pool)
	ctx := context.Background()
	username := uniq("active_session")
	firstToken := uuid.New()
	secondToken := uuid.New()
	expiresAt := time.Now().UTC().Add(time.Hour)

	first, err := repo.JoinByUsername(ctx, username, firstToken, expiresAt)
	require.NoError(t, err)

	second, err := repo.JoinByUsername(ctx, username, secondToken, expiresAt)
	require.ErrorIs(t, err, domain.ErrUsernameTaken)
	require.Nil(t, second)

	retained, err := repo.GetByID(ctx, first.ID)
	require.NoError(t, err)
	require.NotNil(t, retained.SessionToken)
	require.Equal(t, firstToken, *retained.SessionToken)
}

func TestPlayerRepo_JoinByUsername_ReclaimsExpiredSession(t *testing.T) {
	t.Parallel()

	pool := newParallelTestDB(t)
	repo, _ := newPlayerRepo(pool)
	ctx := context.Background()
	username := uniq("expired_session")
	expiredToken := uuid.New()
	newToken := uuid.New()
	expiredAt := time.Now().UTC().Add(-time.Minute)

	created, err := repo.Create(ctx, username)
	require.NoError(t, err)
	_, err = repo.UpdateSessionToken(ctx, created.ID, &expiredToken, &expiredAt)
	require.NoError(t, err)

	reclaimed, err := repo.JoinByUsername(ctx, username, newToken, time.Now().UTC().Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, created.ID, reclaimed.ID)
	require.NotNil(t, reclaimed.SessionToken)
	require.Equal(t, newToken, *reclaimed.SessionToken)
}

func TestPlayerRepo_JoinByUsername_AllowsOnlyOneConcurrentClaim(t *testing.T) {
	t.Parallel()

	pool := newParallelTestDB(t)
	repo, _ := newPlayerRepo(pool)
	ctx := context.Background()
	username := uniq("concurrent_session")
	_, err := repo.Create(ctx, username)
	require.NoError(t, err)

	type claimResult struct {
		player *domain.Player
		token  uuid.UUID
		err    error
	}

	tokens := []uuid.UUID{uuid.New(), uuid.New()}
	start := make(chan struct{})
	results := make(chan claimResult, len(tokens))
	var claims sync.WaitGroup
	for _, token := range tokens {
		claims.Add(1)
		go func(token uuid.UUID) {
			defer claims.Done()
			<-start
			claimed, claimErr := repo.JoinByUsername(ctx, username, token, time.Now().UTC().Add(time.Hour))
			results <- claimResult{player: claimed, token: token, err: claimErr}
		}(token)
	}
	close(start)
	claims.Wait()
	close(results)

	var succeeded []claimResult
	var rejected []claimResult
	for result := range results {
		switch {
		case result.err == nil:
			succeeded = append(succeeded, result)
		case errors.Is(result.err, domain.ErrUsernameTaken):
			rejected = append(rejected, result)
		default:
			require.NoError(t, result.err)
		}
	}

	require.Len(t, succeeded, 1)
	require.Len(t, rejected, 1)
	require.NotNil(t, succeeded[0].player)
	require.NotNil(t, succeeded[0].player.SessionToken)
	require.Equal(t, succeeded[0].token, *succeeded[0].player.SessionToken)
	require.Nil(t, rejected[0].player)
}

func TestPlayerRepo_TournamentReservationAllowsInitialSessionClaimAndBlocksAdminDelete(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	repo, _ := newPlayerRepo()
	player, err := repo.Create(ctx, uniq("reserved_player"))
	require.NoError(t, err)
	tournamentID := createMigrationTournament(ctx, t)
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO participant_reservations (player_id, tournament_id)
		VALUES ($1, $2)`, player.ID, tournamentID)
	require.NoError(t, err)

	token := uuid.New()
	expiresAt := time.Now().UTC().Add(time.Hour)
	claimed, err := repo.JoinByUsername(ctx, player.Username, token, expiresAt)
	require.NoError(t, err)
	require.NotNil(t, claimed.SessionToken)
	require.Equal(t, token, *claimed.SessionToken)

	err = repo.SoftDeletePlayer(
		ctx,
		player.ID,
		"deleted_"+uuid.NewString(),
		time.Now().UTC(),
	)
	require.ErrorIs(t, err, domain.ErrConflict)

	retained, err := repo.GetByID(ctx, player.ID)
	require.NoError(t, err)
	require.Equal(t, player.Username, retained.Username)
}

func TestPlayerRepo_InsideTx_RollsBackOnError(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	repo, mgr := newPlayerRepo(pool)
	ctx := context.Background()
	a, b := uniq("alice"), uniq("bob")

	bust := domain.ErrInternal
	err := mgr.Do(ctx, func(txCtx context.Context) error {
		_, err := repo.Create(txCtx, a)
		require.NoError(t, err)
		_, err = repo.Create(txCtx, b)
		require.NoError(t, err)
		return bust
	})
	require.ErrorIs(t, err, bust)

	_, err = repo.GetByUsername(ctx, a)
	require.ErrorIs(t, err, domain.ErrPlayerNotFound, "tx rollback must wipe %s", a)
	_, err = repo.GetByUsername(ctx, b)
	require.ErrorIs(t, err, domain.ErrPlayerNotFound, "tx rollback must wipe %s", b)
}
