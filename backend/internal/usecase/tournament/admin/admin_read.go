package admin

import (
	"encoding/json"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/audit"
)

const maxAuditEvents = 200

type AuditQuery struct {
	Operator OperatorIdentity
	Filter   audit.AuditFilter
}

func validAuditQuery(query AuditQuery) bool {
	if !validOperator(query.Operator) {
		return false
	}
	_, err := audit.LookupAudit(nil, query.Filter)
	return err == nil
}

func validAuditPage(page audit.AuditPage, tournamentID uuid.UUID) bool {
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
