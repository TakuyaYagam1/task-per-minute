package v1

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func (c *arenaAdminController) GetArenaOperatorRoster(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.ArenaTournamentId,
) {
	operator, ok := arenaOperatorFromRequest(w, r)
	if !ok {
		return
	}
	if c.service == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	result, err := c.service.GetRoster(r.Context(), ArenaGetRosterCommand{
		Operator: operator, TournamentID: tournamentID,
	})
	if err != nil {
		writeArenaAdminError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, result)
}

func (c *arenaAdminController) ReplaceArenaTournamentRoster(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.ArenaTournamentId,
	params api.ReplaceArenaTournamentRosterParams,
) {
	operator, ok := arenaOperatorFromRequest(w, r)
	if !ok {
		return
	}
	if c.service == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	var body api.ArenaReplaceRosterRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	participants := make([]ArenaRosterParticipantCommand, len(body.Participants))
	for index, input := range body.Participants {
		attendance, valid := arenaAttendance(input.Attendance)
		if !valid {
			errmap.HandleError(w, r, domain.ErrValidation)
			return
		}
		participants[index] = ArenaRosterParticipantCommand{
			PlayerID: input.PlayerId, Seed: input.Seed, Attendance: attendance,
		}
	}
	result, err := c.service.ReplaceRoster(r.Context(), ArenaReplaceRosterCommand{
		Operator: operator, TournamentID: tournamentID, CommandID: params.IdempotencyKey,
		ExpectedRevision: body.ExpectedProjectionRevision, Participants: participants,
	})
	if err != nil {
		writeArenaAdminError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, result)
}

func (c *arenaAdminController) RunArenaRosterPreflight(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.ArenaTournamentId,
	params api.RunArenaRosterPreflightParams,
) {
	operator, ok := arenaOperatorFromRequest(w, r)
	if !ok {
		return
	}
	if c.service == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	var body api.ArenaPreflightRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	result, err := c.service.RunRosterPreflight(r.Context(), ArenaRosterPreflightCommand{
		Operator: operator, TournamentID: tournamentID, CommandID: params.IdempotencyKey,
		ExpectedRevision: body.ExpectedProjectionRevision,
	})
	if err != nil {
		writeArenaAdminError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, result)
}

func (c *arenaAdminController) LockArenaTournamentRoster(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.ArenaTournamentId,
	params api.LockArenaTournamentRosterParams,
) {
	operator, ok := arenaOperatorFromRequest(w, r)
	if !ok {
		return
	}
	if c.service == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	var body api.ArenaLockRosterRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	result, err := c.service.LockRoster(r.Context(), ArenaLockRosterCommand{
		Operator: operator, TournamentID: tournamentID, CommandID: params.IdempotencyKey,
		ExpectedRevision:    body.ExpectedProjectionRevision,
		PreflightRevisionID: body.PreflightRevisionId,
		CheckedInPlayerIDs:  append([]uuid.UUID(nil), body.CheckedInPlayerIds...),
	})
	if err != nil {
		writeArenaAdminError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, result)
}

func (c *arenaAdminController) UnlockArenaTournamentRoster(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.ArenaTournamentId,
	params api.UnlockArenaTournamentRosterParams,
) {
	operator, ok := arenaOperatorFromRequest(w, r)
	if !ok {
		return
	}
	if c.service == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	var body api.ArenaUnlockRosterRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	reason := strings.TrimSpace(body.Reason)
	if reason == "" {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}
	result, err := c.service.UnlockRoster(r.Context(), ArenaUnlockRosterCommand{
		Operator: operator, TournamentID: tournamentID, CommandID: params.IdempotencyKey,
		ExpectedRevision: body.ExpectedProjectionRevision,
		Confirmed:        body.Confirmed, Reason: reason,
	})
	if err != nil {
		writeArenaAdminError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, result)
}

func arenaAttendance(value api.ArenaAttendanceState) (ArenaAttendance, bool) {
	attendance := ArenaAttendance(value)
	switch attendance {
	case ArenaAttendanceInvited, ArenaAttendanceRegistered, ArenaAttendanceCheckedIn, ArenaAttendanceWithdrawn:
		return attendance, true
	default:
		return "", false
	}
}
