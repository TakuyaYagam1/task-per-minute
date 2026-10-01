//go:build integration

package player_test

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	playerrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/player"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
)

func newPlayerRepo(pools ...*pgxpool.Pool) (*playerrepo.PlayerPostgres, *postgres.TxManager) {
	pool := sharedPool
	if len(pools) > 0 && pools[0] != nil {
		pool = pools[0]
	}
	mgr := postgres.NewTxManager(pool)
	return playerrepo.NewPlayerPostgres(mgr), mgr
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

func TestPlayerRepo_GetPlayer_NotFound(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	repo, _ := newPlayerRepo(pool)

	_, err := repo.GetPlayer(context.Background(), uuid.New())
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

func TestPlayerRepo_UpdateUsername_SuccessNotFoundAndDuplicate(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	repo, _ := newPlayerRepo(pool)
	ctx := context.Background()

	alice, err := repo.Create(ctx, uniq("alice"))
	require.NoError(t, err)
	bob, err := repo.Create(ctx, uniq("bob"))
	require.NoError(t, err)

	renamed := uniq("renamed")
	require.NoError(t, repo.UpdateUsername(ctx, alice.ID, renamed))
	updated, err := repo.GetPlayer(ctx, alice.ID)
	require.NoError(t, err)
	require.Equal(t, renamed, updated.Username)

	err = repo.UpdateUsername(ctx, uuid.New(), uniq("missing"))
	require.ErrorIs(t, err, domain.ErrPlayerNotFound)

	err = repo.UpdateUsername(ctx, bob.ID, renamed)
	require.ErrorIs(t, err, domain.ErrUsernameTaken)
	retained, err := repo.GetByID(ctx, bob.ID)
	require.NoError(t, err)
	require.Equal(t, bob.Username, retained.Username)
}

func TestPlayerRepo_UpdateUsername_AccountBackedAllowsNoopAndRejectsRename(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	repo, mgr := newPlayerRepo(pool)
	ctx := context.Background()
	player, _ := createVerifiedAccountSession(ctx, t, pool, mgr, repo, uniq("account_player"), time.Now().UTC().Add(time.Hour))

	require.NoError(t, repo.UpdateUsername(ctx, player.ID, player.Username))
	err := repo.UpdateUsername(ctx, player.ID, uniq("renamed_account_player"))
	require.ErrorIs(t, err, domain.ErrConflict)
	retained, err := repo.GetByID(ctx, player.ID)
	require.NoError(t, err)
	require.Equal(t, player.Username, retained.Username)
}

func TestPlayerRepo_UpdateSessionToken_SetThenClear(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	repo, mgr := newPlayerRepo(pool)
	ctx := context.Background()

	expiresAt := time.Now().Add(time.Hour).UTC()
	p, currentToken := createVerifiedAccountSession(ctx, t, pool, mgr, repo, uniq("alice"), expiresAt)
	token := uuid.New()
	updated, err := repo.UpdateSessionToken(ctx, p.ID, currentToken, &token, &expiresAt)
	require.NoError(t, err)
	require.NotNil(t, updated.SessionToken)
	require.Equal(t, token, *updated.SessionToken)
	require.NotNil(t, updated.SessionExpiresAt)
	require.WithinDuration(t, expiresAt, *updated.SessionExpiresAt, time.Millisecond)

	bySession, err := repo.GetBySessionToken(ctx, token)
	require.NoError(t, err)
	require.Equal(t, p.ID, bySession.ID)

	cleared, err := repo.UpdateSessionToken(ctx, p.ID, token, nil, nil)
	require.NoError(t, err)
	require.Nil(t, cleared.SessionToken)
	require.Nil(t, cleared.SessionExpiresAt)

	_, err = repo.GetBySessionToken(ctx, token)
	require.ErrorIs(t, err, domain.ErrPlayerNotFound,
		"after clearing the token nobody should match it")
}

func TestPlayerRepo_StaleSessionClearCannotRevokeReplacement(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	repo, mgr := newPlayerRepo(pool)
	ctx := context.Background()
	expiresAt := time.Now().UTC().Add(time.Hour)
	player, oldToken := createVerifiedAccountSession(ctx, t, pool, mgr, repo, uniq("alice"), expiresAt)
	replacementToken := uuid.New()
	replacementExpiry := expiresAt.Add(time.Hour)
	_, err := repo.UpdateSessionToken(ctx, player.ID, oldToken, &replacementToken, &replacementExpiry)
	require.NoError(t, err)

	_, err = repo.UpdateSessionToken(ctx, player.ID, oldToken, nil, nil)
	require.ErrorIs(t, err, domain.ErrPlayerNotFound)
	current, err := repo.GetBySessionToken(ctx, replacementToken)
	require.NoError(t, err)
	require.Equal(t, player.ID, current.ID)
}

func TestPlayerRepo_RejectsLegacySessionsAndCaseInsensitiveNameReuse(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	repo, _ := newPlayerRepo(pool)
	ctx := context.Background()
	username := uniq("legacyNick")
	legacy, err := repo.Create(ctx, username)
	require.NoError(t, err)
	token := uuid.New()
	_, err = pool.Exec(ctx, `
		UPDATE players SET session_token = $2, session_expires_at = $3 WHERE id = $1`,
		legacy.ID, token, time.Now().UTC().Add(time.Hour))
	require.NoError(t, err)
	_, err = repo.GetBySessionToken(ctx, token)
	require.ErrorIs(t, err, domain.ErrPlayerNotFound)

	_, err = repo.Create(ctx, strings.ToUpper(username))
	require.ErrorIs(t, err, domain.ErrUsernameTaken)
}

func TestPlayerRepo_GetBySessionToken_ExpiredSessionClearsToken(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	repo, mgr := newPlayerRepo(pool)
	ctx := context.Background()

	expiresAt := time.Now().Add(-time.Minute).UTC()
	p, token := createVerifiedAccountSession(ctx, t, pool, mgr, repo, uniq("alice"), expiresAt)

	_, err := repo.GetBySessionToken(ctx, token)
	require.ErrorIs(t, err, domain.ErrPlayerNotFound)

	cleared, err := repo.GetByID(ctx, p.ID)
	require.NoError(t, err)
	require.Nil(t, cleared.SessionToken)
	require.Nil(t, cleared.SessionExpiresAt)
}

func TestPlayerRepo_GetBySessionToken_NullExpiryIsExpired(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	repo, mgr := newPlayerRepo(pool)
	ctx := context.Background()

	expiresAt := time.Now().UTC().Add(time.Hour)
	p, token := createVerifiedAccountSession(ctx, t, pool, mgr, repo, uniq("alice"), expiresAt)
	_, err := pool.Exec(ctx, `UPDATE players SET session_expires_at = NULL WHERE id = $1`, p.ID)
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
	_, err := repo.UpdateSessionToken(context.Background(), uuid.New(), uuid.New(), &token, &expiresAt)
	require.ErrorIs(t, err, domain.ErrPlayerNotFound)
}

func TestPlayerRepo_UpsertStats_SuccessInvalidWinsAndForeignKey(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	repo, _ := newPlayerRepo(pool)
	ctx := context.Background()

	player, err := repo.Create(ctx, uniq("stats_player"))
	require.NoError(t, err)

	err = repo.UpsertStats(ctx, player.ID, playerusecase.StatsInput{
		Wins:               3,
		AverageSolveTimeMs: 90000,
	}, time.Now().UTC())
	require.NoError(t, err)
	stats, err := repo.GetPlayer(ctx, player.ID)
	require.NoError(t, err)
	require.Equal(t, 3, stats.Wins)
	require.Equal(t, int64(90000), stats.AverageSolveTimeMs)
	require.True(t, stats.StatsOverridden)

	err = repo.UpsertStats(ctx, player.ID, playerusecase.StatsInput{Wins: -1}, time.Now().UTC())
	require.ErrorIs(t, err, domain.ErrValidation)
	err = repo.UpsertStats(ctx, player.ID, playerusecase.StatsInput{Wins: int(math.MaxInt32) + 1}, time.Now().UTC())
	require.ErrorIs(t, err, domain.ErrValidation)

	err = repo.UpsertStats(ctx, uuid.New(), playerusecase.StatsInput{
		Wins:               1,
		AverageSolveTimeMs: 1,
	}, time.Now().UTC())
	require.ErrorIs(t, err, domain.ErrPlayerNotFound)
}

func TestPlayerRepo_TournamentReservationBlocksAdminDelete(t *testing.T) {
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

func TestPlayerRepo_SoftDelete_SuccessNotFoundAndDuplicate(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	repo, _ := newPlayerRepo(pool)
	ctx := context.Background()

	deleted, err := repo.Create(ctx, uniq("soft_delete"))
	require.NoError(t, err)
	formerUsername := deleted.Username
	deletedUsername := uniq("deleted")
	deletedAt := time.Now().UTC()
	require.NoError(t, repo.SoftDeletePlayer(ctx, deleted.ID, deletedUsername, deletedAt))

	_, err = repo.GetPlayer(ctx, deleted.ID)
	require.ErrorIs(t, err, domain.ErrPlayerNotFound)
	deletedRecord, err := repo.GetPlayerIncludingDeleted(ctx, deleted.ID)
	require.NoError(t, err)
	require.Equal(t, deletedUsername, deletedRecord.Username)
	require.NotNil(t, deletedRecord.DeletedAt)
	recreated, err := repo.Create(ctx, strings.ToUpper(formerUsername))
	require.NoError(t, err)
	require.NotEqual(t, deleted.ID, recreated.ID)

	err = repo.SoftDeletePlayer(ctx, uuid.New(), uniq("deleted_missing"), time.Now().UTC())
	require.ErrorIs(t, err, domain.ErrPlayerNotFound)

	collision, err := repo.Create(ctx, uniq("collision"))
	require.NoError(t, err)
	target, err := repo.Create(ctx, uniq("target"))
	require.NoError(t, err)
	err = repo.SoftDeletePlayer(ctx, target.ID, collision.Username, time.Now().UTC())
	require.ErrorIs(t, err, domain.ErrUsernameTaken)
}

func TestPlayerRepo_PlayerAuditRoundTripAndInvalidStoredJSON(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	repo, _ := newPlayerRepo(pool)
	ctx := context.Background()

	player, err := repo.Create(ctx, uniq("audit_player"))
	require.NoError(t, err)
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	input := playerusecase.AuditInput{
		Actor:    playerusecase.Actor{Subject: "admin", JTI: uuid.NewString()},
		Action:   playerusecase.AuditActionUpdate,
		PlayerID: player.ID,
		BeforeState: playerusecase.AuditState{
			Username:           player.Username,
			Wins:               0,
			AverageSolveTimeMs: 0,
			StatsOverridden:    false,
			Deleted:            false,
		},
		AfterState: playerusecase.AuditState{
			Username:           "renamed_audit_player",
			Wins:               2,
			AverageSolveTimeMs: 45000,
			StatsOverridden:    true,
			Deleted:            false,
		},
		CreatedAt: createdAt,
	}
	require.NoError(t, repo.CreatePlayerAudit(ctx, input))

	events, err := repo.ListPlayerAudit(ctx, player.ID, 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, input.Actor, events[0].Actor)
	require.Equal(t, input.Action, events[0].Action)
	require.Equal(t, input.PlayerID, events[0].PlayerID)
	require.Equal(t, input.BeforeState, events[0].BeforeState)
	require.Equal(t, input.AfterState, events[0].AfterState)
	require.WithinDuration(t, input.CreatedAt, events[0].CreatedAt, time.Microsecond)

	insertAudit := func(beforeState, afterState string, at time.Time) {
		_, err := pool.Exec(ctx, `
			INSERT INTO admin_player_audit_events (
				actor_subject, actor_jti, action, player_id, before_state, after_state, created_at
			)
			VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb, $7)`,
			"tester", uuid.NewString(), "update", player.ID, beforeState, afterState, at,
		)
		require.NoError(t, err)
	}

	insertAudit(`"invalid"`, `{}`, createdAt.Add(time.Minute))
	_, err = repo.ListPlayerAudit(ctx, player.ID, 10)
	require.ErrorContains(t, err, "before_state")

	insertAudit(`{}`, `"invalid"`, createdAt.Add(2*time.Minute))
	_, err = repo.ListPlayerAudit(ctx, player.ID, 10)
	require.ErrorContains(t, err, "after_state")
}

func TestPlayerRepo_CreatePlayerAudit_NotFoundAndCanceledContext(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	repo, _ := newPlayerRepo(pool)
	ctx := context.Background()

	auditInput := func(playerID uuid.UUID) playerusecase.AuditInput {
		return playerusecase.AuditInput{
			Actor:       playerusecase.Actor{Subject: "admin", JTI: uuid.NewString()},
			Action:      playerusecase.AuditActionUpdate,
			PlayerID:    playerID,
			BeforeState: playerusecase.AuditState{},
			AfterState:  playerusecase.AuditState{Username: "after"},
			CreatedAt:   time.Now().UTC(),
		}
	}

	err := repo.CreatePlayerAudit(ctx, auditInput(uuid.New()))
	require.ErrorIs(t, err, domain.ErrPlayerNotFound)

	player, err := repo.Create(ctx, uniq("audit_context"))
	require.NoError(t, err)
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()

	err = repo.CreatePlayerAudit(canceledCtx, auditInput(player.ID))
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorContains(t, err, "PlayerPostgres - CreatePlayerAudit - Querier.CreateAdminPlayerAuditEvent")
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
