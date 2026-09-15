//go:build integration

package integration_test

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
)

func playerExists(t *testing.T, pool *pgxpool.Pool, username string) bool {
	t.Helper()
	var n int
	require.NoError(t,
		pool.QueryRow(context.Background(),
			`SELECT COUNT(*) FROM players WHERE username = $1`, username).Scan(&n))
	return n > 0
}

func insertPlayer(ctx context.Context, mgr *postgres.TxManager, username string) error {
	_, err := mgr.Conn(ctx).Exec(ctx,
		"INSERT INTO players (username) VALUES ($1)", username)
	return err
}

func TestTxManager_Commit_PersistsRows(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	mgr := postgres.NewTxManager(pool)
	a, b := uniq("alice"), uniq("bob")

	err := mgr.Do(context.Background(), func(ctx context.Context) error {
		if err := insertPlayer(ctx, mgr, a); err != nil {
			return err
		}
		return insertPlayer(ctx, mgr, b)
	})
	require.NoError(t, err)
	require.True(t, playerExists(t, pool, a), "%s missing after commit", a)
	require.True(t, playerExists(t, pool, b), "%s missing after commit", b)
}

func TestTxManager_ErrorRollsBackBothInserts(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	mgr := postgres.NewTxManager(pool)
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
	require.False(t, playerExists(t, pool, a), "%s must be rolled back", a)
	require.False(t, playerExists(t, pool, b), "%s must be rolled back", b)
}

func TestTxManager_PanicRollsBackAndRepanics(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	mgr := postgres.NewTxManager(pool)
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

	require.False(t, playerExists(t, pool, a), "panic path must roll back %s", a)
}

func TestTxManager_NestedDoReusesOuterTx(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	mgr := postgres.NewTxManager(pool)
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
	require.False(t, playerExists(t, pool, a), "outer tx must roll back %s", a)
	require.False(t, playerExists(t, pool, b), "outer tx must roll back %s", b)
}

func TestTxManager_BeginFailureWrapsError(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	mgr := postgres.NewTxManager(pool)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	callbackCalled := false

	err := mgr.Do(ctx, func(context.Context) error {
		callbackCalled = true
		return nil
	})

	require.ErrorIs(t, err, context.Canceled)
	require.ErrorContains(t, err, "TxManager - Do - Pool.BeginTx:")
	require.False(t, callbackCalled, "callback must not run when BeginTx fails")
}

func TestTxManager_RollbackFailureJoinsCallbackError(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	mgr := postgres.NewTxManager(pool)
	callbackErr := errors.New("callback failed")
	var closeErr error

	err := mgr.Do(context.Background(), func(ctx context.Context) error {
		tx, ok := mgr.Conn(ctx).(pgx.Tx)
		if !ok {
			return errors.New("transaction context did not contain pgx transaction")
		}
		closeErr = tx.Conn().Close(context.Background())
		return callbackErr
	})

	require.NoError(t, closeErr)
	require.ErrorIs(t, err, callbackErr)
	require.ErrorContains(t, err, "TxManager - Do - Tx.Rollback:")
	require.NotErrorIs(t, err, pgx.ErrTxClosed)
}

func TestTxManager_CommitFailureWrapsError(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	mgr := postgres.NewTxManager(pool)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := mgr.Do(ctx, func(context.Context) error {
		cancel()
		return nil
	})

	require.ErrorIs(t, err, context.Canceled)
	require.ErrorContains(t, err, "TxManager - Do - Tx.Commit:")
}

func TestTxManager_QuerierOutsideTx_UsesPool(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	mgr := postgres.NewTxManager(pool)
	username := uniq("querier")
	require.NoError(t, insertPlayer(context.Background(), mgr, username))

	got, err := mgr.Querier(context.Background()).GetPlayerByUsername(context.Background(), username)
	require.NoError(t, err, "Querier(ctx) without tx must execute against the pool")
	require.Equal(t, username, got.Username)
}

func TestTxManager_ReadSnapshotIsRepeatableAndReadOnly(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	mgr := postgres.NewTxManager(pool)
	username := uniq("snapshot")

	var observedAt time.Time
	err := mgr.ReadSnapshot(context.Background(), func(ctx context.Context) error {
		var isolation string
		var readOnly string
		if err := mgr.Conn(ctx).QueryRow(ctx, `
			SELECT current_setting('transaction_isolation'),
				current_setting('transaction_read_only'),
				transaction_timestamp()
		`).Scan(&isolation, &readOnly, &observedAt); err != nil {
			return err
		}
		require.Equal(t, "repeatable read", isolation)
		require.Equal(t, "on", readOnly)

		var before int
		if err := mgr.Conn(ctx).QueryRow(ctx,
			"SELECT COUNT(*) FROM players WHERE username = $1", username,
		).Scan(&before); err != nil {
			return err
		}
		require.Zero(t, before)

		if _, err := pool.Exec(ctx,
			"INSERT INTO players (username) VALUES ($1)", username,
		); err != nil {
			return err
		}

		var during int
		if err := mgr.Conn(ctx).QueryRow(ctx,
			"SELECT COUNT(*) FROM players WHERE username = $1", username,
		).Scan(&during); err != nil {
			return err
		}
		require.Zero(t, during, "repeatable read must not observe a later commit")

		return mgr.ReadSnapshot(ctx, func(nestedCtx context.Context) error {
			var nestedObservedAt time.Time
			if err := mgr.Conn(nestedCtx).QueryRow(nestedCtx,
				"SELECT transaction_timestamp()",
			).Scan(&nestedObservedAt); err != nil {
				return err
			}
			require.Equal(t, observedAt, nestedObservedAt)
			require.ErrorIs(t, mgr.Do(nestedCtx, func(context.Context) error { return nil }),
				postgres.ErrReadOnlySnapshotWrite)
			return nil
		})
	})
	require.NoError(t, err)
	require.True(t, playerExists(t, pool, username))
}

func TestTxManager_ReadSnapshotRejectsWriteTransactionNesting(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	mgr := postgres.NewTxManager(pool)

	err := mgr.Do(context.Background(), func(ctx context.Context) error {
		return mgr.ReadSnapshot(ctx, func(context.Context) error { return nil })
	})
	require.ErrorIs(t, err, postgres.ErrSnapshotInsideTransaction)
}

func TestTxManagerGoexitReleasesTransactionAndConnection(t *testing.T) {
	ctx := context.Background()
	config := sharedPool.Config().Copy()
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, config)
	require.NoError(t, err)
	defer pool.Close()
	mgr := postgres.NewTxManager(pool)
	started := make(chan pgx.Tx, 1)
	done := make(chan struct{})
	const lockID int64 = 7839421
	go func() {
		defer close(done)
		_ = mgr.Do(ctx, func(txCtx context.Context) error {
			tx := mgr.Conn(txCtx).(pgx.Tx)
			if _, err := tx.Exec(txCtx, "SELECT pg_advisory_xact_lock($1)", lockID); err != nil {
				return err
			}
			started <- tx
			runtime.Goexit()
			return nil
		})
	}()
	<-done
	var tx pgx.Tx
	select {
	case tx = <-started:
	default:
		t.Fatal("callback did not acquire its transaction lock")
	}
	// Clean up even on the RED implementation, without leaving a borrowed
	// connection to hang the pool's Close operation.
	defer func() { _ = tx.Rollback(context.Background()) }()
	var unlocked bool
	require.NoError(t, sharedPool.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock($1)", lockID).Scan(&unlocked))
	require.True(t, unlocked, "Goexit must release the transaction lock")
	probe, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	var one int
	require.NoError(t, pool.QueryRow(probe, "SELECT 1").Scan(&one))
	require.Equal(t, 1, one)
}
