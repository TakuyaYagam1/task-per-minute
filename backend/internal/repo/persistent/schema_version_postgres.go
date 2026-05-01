package persistent

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type SchemaVersionPostgres struct {
	pool *pgxpool.Pool
}

func NewSchemaVersionPostgres(pool *pgxpool.Pool) *SchemaVersionPostgres {
	return &SchemaVersionPostgres{pool: pool}
}

func (r *SchemaVersionPostgres) SchemaVersion(ctx context.Context) (int64, error) {
	if r == nil || r.pool == nil {
		return 0, errors.New("schema version postgres: nil pool")
	}

	var version int64
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(MAX(version_id), 0)
		FROM goose_db_version
		WHERE is_applied
	`).Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("SchemaVersionPostgres - SchemaVersion - QueryRow: %w", err)
	}
	return version, nil
}
