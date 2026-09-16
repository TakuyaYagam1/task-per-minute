package postgres

import "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/realtime"

type RealtimeOutboxPostgres = realtime.RealtimeOutboxPostgres

func NewRealtimeOutboxPostgres(tx *TxManager) *RealtimeOutboxPostgres {
	return realtime.NewRealtimeOutboxPostgres(tx)
}
