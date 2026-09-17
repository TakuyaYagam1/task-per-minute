package admin

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/audit"
	incidentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/incident"
)

type AuditQuery = incidentusecase.AuditQuery

func validAuditQuery(query AuditQuery) bool {
	return incidentusecase.ValidAuditQuery(query)
}

func validAuditPage(page audit.AuditPage, tournamentID uuid.UUID) bool {
	return incidentusecase.ValidAuditPage(page, tournamentID)
}
