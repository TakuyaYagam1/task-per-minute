package postgres

import auditpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/audit"

type TournamentAdminAuditPostgres = auditpostgres.TournamentAdminAuditPostgres

func NewTournamentAdminAuditPostgres(tx *TxManager) *TournamentAdminAuditPostgres {
	return auditpostgres.NewTournamentAdminAuditPostgres(tx)
}
