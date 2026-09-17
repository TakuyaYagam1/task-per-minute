package postgres

import (
	"context"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Config = db.Config

var ErrNilPool = db.ErrNilPool

func New(ctx context.Context, cfg Config) (*pgxpool.Pool, error) {
	return db.New(ctx, cfg)
}

func HealthCheck(ctx context.Context, pool *pgxpool.Pool) error {
	return db.HealthCheck(ctx, pool)
}
