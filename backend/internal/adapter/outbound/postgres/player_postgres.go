package postgres

import "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/player"

type PlayerPostgres = player.PlayerPostgres

func NewPlayerPostgres(tx *TxManager) *PlayerPostgres {
	return player.NewPlayerPostgres(tx)
}
