package persistent

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/TakuyaYagam1/task-per-minute/internal/repo/persistent/sqlc"
)

type ctxKey struct{}

type TxManager struct {
	pool *pgxpool.Pool
}

func NewTxManager(pool *pgxpool.Pool) *TxManager {
	return &TxManager{pool: pool}
}

// Do runs fn inside a single transaction. Nested Do calls reuse the outer tx.
// fn returning a non-nil error triggers Rollback; a panic also triggers Rollback
// and the panic is re-raised after the transaction is closed.
func (m *TxManager) Do(ctx context.Context, fn func(ctx context.Context) error) (err error) {
	if _, ok := txFromCtx(ctx); ok {
		return fn(ctx)
	}

	tx, beginErr := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if beginErr != nil {
		return fmt.Errorf("TxManager - Do - Pool.BeginTx: %w", beginErr)
	}

	//nolint:contextcheck // rollback must run on a fresh context: the caller's ctx may already be cancelled when we land here
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(context.Background())
			panic(p)
		}
		if err == nil {
			return
		}
		if rbErr := tx.Rollback(context.Background()); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			err = errors.Join(err, fmt.Errorf("TxManager - Do - Tx.Rollback: %w", rbErr))
		}
	}()

	txCtx := context.WithValue(ctx, ctxKey{}, tx)
	if err = fn(txCtx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("TxManager - Do - Tx.Commit: %w", err)
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
	tx, ok := ctx.Value(ctxKey{}).(pgx.Tx)
	return tx, ok
}
