package admin

import (
	"github.com/google/uuid"

	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/audit"
	tournamentpreflight "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/preflight"
)

// mapPreflightReport copies the leaf workflow report at the inbound boundary.
// The report contains slices owned by the workflow, so callers cannot mutate
// retained workflow state through an inbound result.
func mapPreflightReport(report tournamentpreflight.ReportRevision) inbound.AdminPreflightReport {
	mapped := inbound.AdminPreflightReport{
		ID: report.ID, TournamentID: report.TournamentID, AlgorithmVersion: report.AlgorithmVersion,
		EvaluatedAt: report.EvaluatedAt, NormalizedInputs: append([]string(nil), report.NormalizedInputs...),
		ProofHash: report.ProofHash,
	}
	if report.Revisions != nil {
		mapped.Revisions = make([]inbound.AdminPreflightSourceRevision, len(report.Revisions))
	}
	if report.Checks != nil {
		mapped.Checks = make([]inbound.AdminPreflightCheck, len(report.Checks))
	}
	for index, revision := range report.Revisions {
		mapped.Revisions[index] = inbound.AdminPreflightSourceRevision{Source: revision.Source, Value: revision.Value}
	}
	for index, check := range report.Checks {
		mapped.Checks[index] = inbound.AdminPreflightCheck{
			Code: string(check.Code), Passed: check.Passed, Explanation: check.Explanation,
			Evidence: append([]string(nil), check.Evidence...),
		}
	}
	return mapped
}

func mapAuditPage(page audit.AuditPage) inbound.AdminAuditPage {
	mapped := inbound.AdminAuditPage{}
	if page.Events != nil {
		mapped.Events = make([]inbound.AdminAuditEvent, len(page.Events))
	}
	for index, event := range page.Events {
		mapped.Events[index] = inbound.AdminAuditEvent{
			AuditEventID: event.AuditEventID, TournamentID: event.TournamentID, RosterID: event.RosterID,
			SeriesID: event.SeriesID, ResultEventID: event.ResultEventID, ActorKind: event.ActorKind,
			ActorID: copyInboundUUID(event.ActorID), EventType: event.EventType,
			RedactedPayload: append([]byte(nil), event.RedactedPayload...), OccurredAt: event.OccurredAt,
			CreatedAt: event.CreatedAt, ResultState: event.ResultState, ResultReason: event.ResultReason,
			WinnerID: copyInboundUUID(event.WinnerID), OfficialResultRevisionID: event.OfficialResultRevisionID,
			EntityKind: string(event.EntityKind), EntityID: event.EntityID, RevisionNumber: event.RevisionNumber,
			IsCurrent: event.IsCurrent, IsSuperseded: event.IsSuperseded,
		}
	}
	if page.NextCursor != nil {
		mapped.NextCursor = &inbound.AdminAuditCursor{
			OccurredAt: page.NextCursor.OccurredAt, AuditEventID: page.NextCursor.AuditEventID,
			RevisionID: page.NextCursor.RevisionID, SnapshotBound: page.NextCursor.SnapshotBound,
		}
	}
	return mapped
}

func copyInboundUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func mapIncidentBundle(bundle audit.IncidentBundle) inbound.AdminIncidentBundle {
	return inbound.AdminIncidentBundle{
		TournamentID: bundle.TournamentID, ProjectionRevision: bundle.ProjectionRevision,
		GeneratedAt: bundle.GeneratedAt, CanonicalContent: append([]byte(nil), bundle.CanonicalContent...),
		SHA256: bundle.SHA256, Algorithm: bundle.Algorithm, KeyID: bundle.KeyID, MAC: bundle.MAC,
	}
}
