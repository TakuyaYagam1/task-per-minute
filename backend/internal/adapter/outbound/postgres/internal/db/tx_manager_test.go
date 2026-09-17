package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
)

func TestTxManagerHasPool(t *testing.T) {
	var nilManager *db.TxManager
	if nilManager.HasPool() {
		t.Fatal("nil manager must not report a pool")
	}
	if db.NewTxManager(nil).HasPool() {
		t.Fatal("manager with nil pool must not report a pool")
	}
	if !db.NewTxManager(&pgxpool.Pool{}).HasPool() {
		t.Fatal("manager with a pool must report it")
	}
}

func TestWithTransactionCreatesWriteScope(t *testing.T) {
	manager := db.NewTxManager(nil)
	ctx := db.WithTransaction(context.Background(), nil)

	called := false
	err := manager.Do(ctx, func(context.Context) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("reuse write scope: %v", err)
	}
	if !called {
		t.Fatal("write scope callback was not called")
	}

	err = manager.ReadSnapshot(ctx, func(context.Context) error { return nil })
	if !errors.Is(err, db.ErrSnapshotInsideTransaction) {
		t.Fatalf("read snapshot inside write scope error = %v", err)
	}
}
