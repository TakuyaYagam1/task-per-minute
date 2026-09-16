package postgres

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/player"
)

type AdminPlayerEventsPostgres = player.AdminPlayerEventsPostgres

func NewAdminPlayerEventsPostgres(pool *pgxpool.Pool) *AdminPlayerEventsPostgres {
	return player.NewAdminPlayerEventsPostgres(pool)
}
