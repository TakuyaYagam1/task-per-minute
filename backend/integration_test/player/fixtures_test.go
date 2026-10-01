//go:build integration

package player_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	testkit "github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	accountrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/account"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/player"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth/mocks"
)

type databaseFixture struct {
	mgr     *postgres.TxManager
	players *player.PlayerPostgres
}

func newDatabaseFixture(pools ...*pgxpool.Pool) *databaseFixture {
	pool := sharedPool
	if len(pools) > 0 && pools[0] != nil {
		pool = pools[0]
	}
	fixture := testkit.NewDatabaseFixture(pool)
	return &databaseFixture{mgr: fixture.Manager, players: fixture.Players}
}

func realIntegrationClock() *authmocks.MockClock {
	clock := &authmocks.MockClock{}
	clock.EXPECT().Now().RunAndReturn(func() time.Time { return time.Now().UTC() }).Maybe()
	return clock
}

func createVerifiedAccountSession(
	ctx context.Context,
	tb testing.TB,
	pool *pgxpool.Pool,
	mgr *postgres.TxManager,
	players *player.PlayerPostgres,
	username string,
	expiresAt time.Time,
) (*domain.Player, uuid.UUID) {
	tb.Helper()
	legacyPlayer, err := players.Create(ctx, username)
	require.NoError(tb, err)
	accountID := uuid.New()
	email := uniq("player") + "@example.test"
	_, err = pool.Exec(ctx, `
		INSERT INTO player_accounts (
			id, player_id, username, username_normalized, email, email_normalized,
			password_hash, email_verified_at
		)
		VALUES ($1, $2, $3::varchar(50), lower($3::varchar(50)), $4::varchar(254), lower($4::varchar(254)), 'integration-test-hash', now())`,
		accountID,
		legacyPlayer.ID,
		legacyPlayer.Username,
		email,
	)
	require.NoError(tb, err)
	result, err := pool.Exec(ctx, `
		UPDATE player_username_reservations
		SET legacy_count = 0, account_id = $2
		WHERE normalized_username = lower($1) AND account_id IS NULL`,
		username,
		accountID,
	)
	require.NoError(tb, err)
	require.EqualValues(tb, 1, result.RowsAffected())
	token := uuid.New()
	updated, err := accountrepo.NewAccountPostgres(mgr).UpdateAccountPlayerSession(
		ctx,
		legacyPlayer.ID,
		strings.ToLower(legacyPlayer.Username),
		"integration-test-hash",
		token,
		expiresAt,
	)
	require.NoError(tb, err)
	return updated, token
}

func resetMigrationTables(ctx context.Context, tb testing.TB) {
	tb.Helper()
	_, err := sharedPool.Exec(ctx, `TRUNCATE TABLE participant_reservations, tournaments CASCADE`)
	require.NoError(tb, err)
}

func createMigrationTournament(ctx context.Context, tb testing.TB) uuid.UUID {
	tb.Helper()
	var id uuid.UUID
	err := sharedPool.QueryRow(ctx, `
		INSERT INTO tournaments DEFAULT VALUES
		RETURNING id`).Scan(&id)
	require.NoError(tb, err)
	return id
}
