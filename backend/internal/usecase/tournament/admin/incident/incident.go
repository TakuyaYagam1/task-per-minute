package incident

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/audit"
	operation "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/operation"
)

const maxAuditEvents = 200

// AuditQuery identifies the operator and filters for an audit page request.
type AuditQuery struct {
	Operator operation.OperatorIdentity
	Filter   audit.AuditFilter
}

// IncidentQuery identifies the operator and tournament for an incident export.
type IncidentQuery struct {
	Operator     operation.OperatorIdentity
	TournamentID uuid.UUID
}

// AuditPort provides validated audit pages to tournament administration.
type AuditPort interface {
	ListAudit(ctx context.Context, query AuditQuery) (audit.AuditPage, error)
}

// IncidentSnapshotPort provides the durable snapshot used to build an
// incident bundle.
type IncidentSnapshotPort interface {
	LoadIncidentSnapshot(ctx context.Context, query IncidentQuery) (audit.IncidentBundleSnapshot, error)
}

// IncidentAuthenticator signs and verifies incident bundles.
type IncidentAuthenticator interface {
	Sign(bundle audit.IncidentBundle) (audit.IncidentBundle, error)
	Verify(bundle audit.IncidentBundle) error
}

// ValidAuditQuery validates the operator and audit filter boundaries.
func ValidAuditQuery(query AuditQuery) bool {
	if query.Operator.ActorID == uuid.Nil {
		return false
	}
	_, err := audit.LookupAudit(nil, query.Filter)
	return err == nil
}

// ValidAuditPage validates an audit page for the requested tournament.
func ValidAuditPage(page audit.AuditPage, tournamentID uuid.UUID) bool {
	if page.Events == nil || len(page.Events) > maxAuditEvents {
		return false
	}
	for _, event := range page.Events {
		if event.TournamentID != tournamentID || !json.Valid(event.RedactedPayload) {
			return false
		}
		if _, err := audit.LookupAudit([]audit.AuditEvent{event}, audit.AuditFilter{
			TournamentID: tournamentID, PageSize: 1,
		}); err != nil {
			return false
		}
	}
	if page.NextCursor != nil {
		_, err := audit.LookupAudit(nil, audit.AuditFilter{
			TournamentID: tournamentID, Cursor: page.NextCursor,
		})
		return err == nil
	}
	return true
}

// ValidIncidentQuery validates the operator and tournament boundaries.
func ValidIncidentQuery(query IncidentQuery) bool {
	return query.Operator.ActorID != uuid.Nil && query.TournamentID != uuid.Nil
}
