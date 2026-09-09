package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
)

type ctxKey struct{}

type transactionScope struct {
	tx       pgx.Tx
	readOnly bool
}

type TxManager struct {
	pool *pgxpool.Pool
}

var (
	ErrReadOnlySnapshotWrite     = errors.New("transaction manager: write requested inside read-only snapshot")
	ErrSnapshotInsideTransaction = errors.New("transaction manager: snapshot requested inside write transaction")

	_ playerusecase.ManagementTransactionManager = (*TxManager)(nil)
	_ playerusecase.SessionTransactionManager    = (*TxManager)(nil)
)

func NewTxManager(pool *pgxpool.Pool) *TxManager {
	return &TxManager{pool: pool}
}

// Do runs fn inside a single transaction. Nested Do calls reuse the outer tx.
// Every uncommitted transaction is rolled back, including panic and Goexit
// paths. Panics propagate after cleanup and nested calls retain outer ownership.
func (m *TxManager) Do(ctx context.Context, fn func(ctx context.Context) error) (err error) {
	if scope, ok := transactionScopeFromCtx(ctx); ok {
		if scope.readOnly {
			return ErrReadOnlySnapshotWrite
		}
		return fn(ctx)
	}

	return m.run(ctx, pgx.TxOptions{}, false, "Do", fn)
}

// ReadSnapshot runs fn in one repeatable-read, read-only PostgreSQL snapshot.
// Nested calls reuse an existing read snapshot. Entering a read snapshot from a
// write transaction fails because PostgreSQL cannot strengthen an active tx.
func (m *TxManager) ReadSnapshot(
	ctx context.Context,
	fn func(ctx context.Context) error,
) error {
	if scope, ok := transactionScopeFromCtx(ctx); ok {
		if !scope.readOnly {
			return ErrSnapshotInsideTransaction
		}
		return fn(ctx)
	}

	return m.run(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	}, true, "ReadSnapshot", fn)
}

func (m *TxManager) run(
	ctx context.Context,
	options pgx.TxOptions,
	readOnly bool,
	operation string,
	fn func(ctx context.Context) error,
) (err error) {
	tx, beginErr := m.pool.BeginTx(ctx, options)
	if beginErr != nil {
		return fmt.Errorf("TxManager - %s - Pool.BeginTx: %w", operation, beginErr)
	}

	//nolint:contextcheck // rollback must run on a fresh context: the caller's ctx may already be cancelled when we land here
	defer func() {
		if rbErr := tx.Rollback(context.Background()); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			err = errors.Join(err, fmt.Errorf("TxManager - %s - Tx.Rollback: %w", operation, rbErr))
		}
	}()

	txCtx := context.WithValue(ctx, ctxKey{}, transactionScope{tx: tx, readOnly: readOnly})
	if err = fn(txCtx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("TxManager - %s - Tx.Commit: %w", operation, err)
	}
	return nil
}

// Querier returns sqlc.Queries bound to the active tx if present, else to the pool.
func (m *TxManager) Querier(ctx context.Context) *sqlc.Queries {
	return sqlc.New(m.Conn(ctx))
}

// Conn returns the active transaction if present, otherwise the underlying pool.
// Use it for raw pgx access that cannot go through sqlc (e.g. ad-hoc DDL in tests).
func (m *TxManager) Conn(ctx context.Context) sqlc.DBTX {
	if tx, ok := txFromCtx(ctx); ok {
		return tx
	}
	return m.pool
}

func txFromCtx(ctx context.Context) (pgx.Tx, bool) {
	scope, ok := transactionScopeFromCtx(ctx)
	return scope.tx, ok
}

func transactionScopeFromCtx(ctx context.Context) (transactionScope, bool) {
	scope, ok := ctx.Value(ctxKey{}).(transactionScope)
	return scope, ok
}
