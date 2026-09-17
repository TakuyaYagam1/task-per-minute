package admin

import incidentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/incident"

type IncidentQuery = incidentusecase.IncidentQuery
type AuditPort = incidentusecase.AuditPort
type IncidentSnapshotPort = incidentusecase.IncidentSnapshotPort
type IncidentAuthenticator = incidentusecase.IncidentAuthenticator

func validIncidentQuery(query IncidentQuery) bool {
	return incidentusecase.ValidIncidentQuery(query)
}
