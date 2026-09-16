package postgres

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
)

type SchemaVersionPostgres = db.SchemaVersionPostgres

func NewSchemaVersionPostgres(pool *pgxpool.Pool) *SchemaVersionPostgres {
	return db.NewSchemaVersionPostgres(pool)
}
