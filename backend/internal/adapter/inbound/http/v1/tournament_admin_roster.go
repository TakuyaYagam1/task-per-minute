package v1

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func (c *tournamentController) GetTournamentRoster(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
) {
	operator, service, ok := c.requireAdminService(w, r)
	if !ok {
		return
	}
	view, err := service.GetRoster(r.Context(), inbound.AdminRosterQuery{
		Operator: operator, TournamentID: tournamentID,
	})
	if err != nil {
		writeTournamentAdminError(w, r, err)
		return
	}
	payload, err := tournamentRosterResponse(view)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, payload)
}

func (c *tournamentController) ReplaceTournamentRoster(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
	params api.ReplaceTournamentRosterParams,
) {
	operator, service, ok := c.requireAdminService(w, r)
	if !ok {
		return
	}
	var body api.ReplaceRosterRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	participants := make([]inbound.AdminRosterParticipantInput, len(body.Participants))
	for index, participant := range body.Participants {
		participants[index] = inbound.AdminRosterParticipantInput{
			PlayerID: participant.PlayerId, Seed: int(participant.Seed),
			Attendance: domain.AttendanceState(participant.Attendance),
		}
	}
	view, err := service.ReplaceRoster(r.Context(), inbound.AdminReplaceRosterCommand{AdminCommandScope: inbound.AdminCommandScope{Operator: operator, TournamentID: tournamentID, CommandID: params.IdempotencyKey}, ExpectedProjectionRevision: body.ExpectedProjectionRevision, Participants: participants})
	writeRosterMutationResponse(w, r, view, err)
}

func (c *tournamentController) RunTournamentRosterPreflight(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
	params api.RunTournamentRosterPreflightParams,
) {
	operator, service, ok := c.requireAdminService(w, r)
	if !ok {
		return
	}
	var body api.PreflightRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	report, err := service.RunPreflight(r.Context(), inbound.AdminPreflightCommand{
		AdminCommandScope: inbound.AdminCommandScope{
			Operator: operator, TournamentID: tournamentID, CommandID: params.IdempotencyKey,
		},
		ExpectedProjectionRevision: body.ExpectedProjectionRevision,
	})
	if err != nil {
		writeTournamentAdminError(w, r, err)
		return
	}
	payload, err := tournamentPreflightResponse(report)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, payload)
}

func (c *tournamentController) LockTournamentRoster(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
	params api.LockTournamentRosterParams,
) {
	operator, service, ok := c.requireAdminService(w, r)
	if !ok {
		return
	}
	var body api.LockRosterRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	view, err := service.LockRoster(r.Context(), inbound.AdminLockRosterCommand{
		AdminCommandScope: inbound.AdminCommandScope{
			Operator: operator, TournamentID: tournamentID, CommandID: params.IdempotencyKey,
		},
		ExpectedProjectionRevision: body.ExpectedProjectionRevision,
		PreflightRevisionID:        body.PreflightRevisionId,
		CheckedInPlayerIDs:         append([]uuid.UUID(nil), body.CheckedInPlayerIds...),
	})
	writeRosterMutationResponse(w, r, view, err)
}

func (c *tournamentController) UnlockTournamentRoster(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
	params api.UnlockTournamentRosterParams,
) {
	operator, service, ok := c.requireAdminService(w, r)
	if !ok {
		return
	}
	var body api.UnlockRosterRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	view, err := service.UnlockRoster(r.Context(), inbound.AdminUnlockRosterCommand{
		AdminCommandScope: inbound.AdminCommandScope{
			Operator: operator, TournamentID: tournamentID, CommandID: params.IdempotencyKey,
		},
		ExpectedProjectionRevision: body.ExpectedProjectionRevision,
		Confirmed:                  body.Confirmed,
		Reason:                     body.Reason,
	})
	writeRosterMutationResponse(w, r, view, err)
}

func writeRosterMutationResponse(
	w http.ResponseWriter,
	r *http.Request,
	view inbound.AdminRosterView,
	err error,
) {
	if err != nil {
		writeTournamentAdminError(w, r, err)
		return
	}
	payload, err := tournamentRosterResponse(view)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, payload)
}
