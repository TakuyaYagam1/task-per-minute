package v1

import (
	"net/http"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func (c *tournamentController) ListTournamentAudit(
	w http.ResponseWriter,
	r *http.Request,
	params api.ListTournamentAuditParams,
) {
	operator, service, ok := c.requireAdminService(w, r)
	if !ok {
		return
	}
	filter := inbound.AdminAuditFilter{TournamentID: params.TournamentId}
	if params.EntityKind != nil {
		filter.EntityKind = string(*params.EntityKind)
	}
	filter.EntityID = cloneUUIDPointer(params.EntityId)
	if params.EventType != nil {
		filter.EventType = *params.EventType
	}
	if params.ActorKind != nil {
		filter.ActorKind = domain.ResultActorKind(*params.ActorKind)
	}
	filter.ActorID = cloneUUIDPointer(params.ActorId)
	if params.ResultReason != nil {
		filter.ResultReason = *params.ResultReason
	}
	filter.OccurredFrom = cloneTimePointer(params.OccurredFrom)
	filter.OccurredTo = cloneTimePointer(params.OccurredTo)
	if params.Cursor != nil {
		filter.Cursor = &inbound.AdminAuditCursor{
			OccurredAt:    params.Cursor.OccurredAt,
			AuditEventID:  params.Cursor.AuditEventId,
			RevisionID:    params.Cursor.RevisionId,
			SnapshotBound: params.Cursor.SnapshotBound,
		}
	}
	if params.PageSize != nil {
		filter.PageSize = int(*params.PageSize)
	}
	page, err := service.ListAudit(r.Context(), inbound.AdminAuditQuery{Operator: operator, Filter: filter})
	if err != nil {
		writeTournamentAdminError(w, r, err)
		return
	}
	payload, err := tournamentAuditPageResponse(page)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, payload)
}

func (c *tournamentController) ExportTournamentIncident(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
) {
	operator, service, ok := c.requireAdminService(w, r)
	if !ok {
		return
	}
	bundle, err := service.ExportIncident(r.Context(), inbound.AdminIncidentQuery{
		Operator: operator, TournamentID: tournamentID,
	})
	if err != nil {
		writeTournamentAdminError(w, r, err)
		return
	}
	payload := tournamentIncidentResponse(bundle)
	response.WriteJSON(w, http.StatusOK, payload)
}

func (c *tournamentController) GetOperatorSnapshot(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
	params api.GetOperatorSnapshotParams,
) {
	operator, service, ok := c.requireAdminService(w, r)
	if !ok {
		return
	}
	query := inbound.AdminSnapshotQuery{Operator: operator, TournamentID: tournamentID}
	if params.Cursor != nil {
		query.Cursor = &inbound.AdminOperatorCursor{
			ProjectionRevision: params.Cursor.ProjectionRevision,
			AuthorityRevision:  params.Cursor.AuthorityRevision,
			AuditSequence:      params.Cursor.AuditSequence,
		}
	}
	view, err := service.GetOperatorSnapshot(r.Context(), query)
	if err != nil {
		writeTournamentAdminError(w, r, err)
		return
	}
	payload, err := tournamentOperatorSnapshotResponse(view)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, payload)
}
