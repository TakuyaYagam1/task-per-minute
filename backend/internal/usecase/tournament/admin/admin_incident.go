package admin

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/audit"
)

type IncidentQuery struct {
	Operator     OperatorIdentity
	TournamentID uuid.UUID
}

type AuditPort interface {
	ListAudit(ctx context.Context, query AuditQuery) (audit.AuditPage, error)
}

type IncidentSnapshotPort interface {
	LoadIncidentSnapshot(ctx context.Context, query IncidentQuery) (audit.IncidentBundleSnapshot, error)
}

type IncidentAuthenticator interface {
	Sign(bundle audit.IncidentBundle) (audit.IncidentBundle, error)
	Verify(bundle audit.IncidentBundle) error
}

func validIncidentQuery(query IncidentQuery) bool {
	return validOperator(query.Operator) && query.TournamentID != uuid.Nil
}
