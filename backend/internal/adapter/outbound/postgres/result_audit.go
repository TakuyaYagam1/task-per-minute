package postgres

import auditpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/audit"

type AuditPostgres = auditpostgres.AuditPostgres
type AuditCursor = auditpostgres.AuditCursor
type AuditFilter = auditpostgres.AuditFilter
type AuditRecord = auditpostgres.AuditRecord
type AuditPage = auditpostgres.AuditPage

func NewAuditPostgres(tx *TxManager) *AuditPostgres {
	return auditpostgres.NewAuditPostgres(tx)
}
