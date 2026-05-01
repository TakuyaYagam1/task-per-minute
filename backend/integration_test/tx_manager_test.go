//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/repo/persistent"
)

func playerExists(t *testing.T, pool *pgxpool.Pool, username string) bool {
	t.Helper()
	var n int
	require.NoError(t,
		pool.QueryRow(context.Background(),
			`SELECT COUNT(*) FROM players WHERE username = $1`, username).Scan(&n))
	return n > 0
}

func insertPlayer(ctx context.Context, mgr *persistent.TxManager, username string) error {
	_, err := mgr.Conn(ctx).Exec(ctx,
		"INSERT INTO players (username) VALUES ($1)", username)
	return err
}

func TestTxManager_Commit_PersistsRows(t *testing.T) {
	t.Parallel()
	mgr := persistent.NewTxManager(sharedPool)
	a, b := uniq("alice"), uniq("bob")

	err := mgr.Do(context.Background(), func(ctx context.Context) error {
		if err := insertPlayer(ctx, mgr, a); err != nil {
			return err
		}
		return insertPlayer(ctx, mgr, b)
	})
	require.NoError(t, err)
	require.True(t, playerExists(t, sharedPool, a), "%s missing after commit", a)
	require.True(t, playerExists(t, sharedPool, b), "%s missing after commit", b)
}

func TestTxManager_ErrorRollsBackBothInserts(t *testing.T) {
	t.Parallel()
	mgr := persistent.NewTxManager(sharedPool)
	a, b := uniq("alice"), uniq("bob")
	bust := errors.New("bust")

	err := mgr.Do(context.Background(), func(ctx context.Context) error {
		if err := insertPlayer(ctx, mgr, a); err != nil {
			return err
		}
		if err := insertPlayer(ctx, mgr, b); err != nil {
			return err
		}
		return bust
	})
	require.ErrorIs(t, err, bust)
	require.False(t, playerExists(t, sharedPool, a), "%s must be rolled back", a)
	require.False(t, playerExists(t, sharedPool, b), "%s must be rolled back", b)
}

func TestTxManager_PanicRollsBackAndRepanics(t *testing.T) {
	t.Parallel()
	mgr := persistent.NewTxManager(sharedPool)
	a := uniq("alice")

	func() {
		defer func() {
			r := recover()
			require.NotNil(t, r, "panic must propagate")
			require.Equal(t, "boom", r)
		}()
		_ = mgr.Do(context.Background(), func(ctx context.Context) error {
			if err := insertPlayer(ctx, mgr, a); err != nil {
				return err
			}
			panic("boom")
		})
	}()

	require.False(t, playerExists(t, sharedPool, a), "panic path must roll back %s", a)
}

func TestTxManager_NestedDoReusesOuterTx(t *testing.T) {
	t.Parallel()
	mgr := persistent.NewTxManager(sharedPool)
	a, b := uniq("alice"), uniq("bob")
	bust := errors.New("inner bust")

	err := mgr.Do(context.Background(), func(outerCtx context.Context) error {
		if err := insertPlayer(outerCtx, mgr, a); err != nil {
			return err
		}
		return mgr.Do(outerCtx, func(innerCtx context.Context) error {
			if err := insertPlayer(innerCtx, mgr, b); err != nil {
				return err
			}
			return bust
		})
	})
	require.ErrorIs(t, err, bust)
	require.False(t, playerExists(t, sharedPool, a), "outer tx must roll back %s", a)
	require.False(t, playerExists(t, sharedPool, b), "outer tx must roll back %s", b)
}

func TestTxManager_QuerierOutsideTx_UsesPool(t *testing.T) {
	t.Parallel()
	mgr := persistent.NewTxManager(sharedPool)

	got, err := mgr.Querier(context.Background()).CountTasksByDifficulty(context.Background(), "easy")
	require.NoError(t, err, "Querier(ctx) without tx must execute against the pool")
	require.GreaterOrEqual(t, got, int64(0),
		"smoke check: pool-bound Querier reaches the DB and returns a valid count")
}
