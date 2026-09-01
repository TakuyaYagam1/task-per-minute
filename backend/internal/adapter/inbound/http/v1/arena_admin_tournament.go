package v1

import (
	"errors"
	"net/http"
	"strings"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arenausecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

type arenaAdminController struct {
	api.Unimplemented

	service ArenaAdminService
}

func newArenaAdminController(service ArenaAdminService) *arenaAdminController {
	return &arenaAdminController{service: service}
}

func (c *arenaAdminController) ListArenaOperatorTournaments(
	w http.ResponseWriter,
	r *http.Request,
	params api.ListArenaOperatorTournamentsParams,
) {
	operator, ok := arenaOperatorFromRequest(w, r)
	if !ok {
		return
	}
	if c.service == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	command := ArenaListTournamentsCommand{Operator: operator}
	if params.State != nil {
		command.State = domain.ArenaTournamentState(*params.State)
	}
	if params.Cursor != nil {
		command.Cursor = *params.Cursor
	}
	if params.PageSize != nil {
		command.PageSize = *params.PageSize
	}
	result, err := c.service.ListTournaments(r.Context(), command)
	if err != nil {
		writeArenaAdminError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, result)
}

func (c *arenaAdminController) CreateArenaTournament(
	w http.ResponseWriter,
	r *http.Request,
	params api.CreateArenaTournamentParams,
) {
	operator, ok := arenaOperatorFromRequest(w, r)
	if !ok {
		return
	}
	if c.service == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	var body api.ArenaCreateTournamentRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	result, err := c.service.CreateTournament(r.Context(), ArenaCreateTournamentCommand{
		Operator:         operator,
		CommandID:        params.IdempotencyKey,
		ExpectedRevision: body.ExpectedProjectionRevision,
		Preset:           domain.ArenaPreset(body.Preset),
		RosterSize:       int(body.RosterSize),
	})
	if err != nil {
		writeArenaAdminError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusCreated, result)
}

func (c *arenaAdminController) ApplyArenaTournamentAction(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.ArenaTournamentId,
	params api.ApplyArenaTournamentActionParams,
) {
	operator, ok := arenaOperatorFromRequest(w, r)
	if !ok {
		return
	}
	if c.service == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	var body api.ArenaTournamentActionRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	action, ok := arenaTournamentAction(body.Action)
	if !ok {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}
	reason := ""
	if body.Reason != nil {
		reason = strings.TrimSpace(*body.Reason)
	}
	if action == ArenaTournamentActionCancel && reason == "" {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}

	result, err := c.service.ApplyTournamentAction(r.Context(), ArenaTournamentActionCommand{
		Operator: operator, TournamentID: tournamentID, CommandID: params.IdempotencyKey,
		ExpectedRevision: body.ExpectedProjectionRevision, Action: action,
		Confirmed: body.Confirmed, Reason: reason,
	})
	if err != nil {
		writeArenaAdminError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, result)
}

func arenaTournamentAction(value api.ArenaTournamentActionRequestAction) (ArenaTournamentAction, bool) {
	action := ArenaTournamentAction(value)
	switch action {
	case ArenaTournamentActionOpenRegistration,
		ArenaTournamentActionStartSwiss,
		ArenaTournamentActionStartGolden,
		ArenaTournamentActionStartPlayoffs,
		ArenaTournamentActionPause,
		ArenaTournamentActionResume,
		ArenaTournamentActionComplete,
		ArenaTournamentActionCancel:
		return action, true
	default:
		return "", false
	}
}

func arenaOperatorFromRequest(w http.ResponseWriter, r *http.Request) (ArenaOperatorIdentity, bool) {
	actor, ok := adminActorFromRequest(r)
	if !ok {
		errmap.HandleError(w, r, domain.ErrInvalidCredentials)
		return ArenaOperatorIdentity{}, false
	}
	return ArenaOperatorIdentity{Subject: actor.Subject, SessionID: actor.JTI}, true
}

func writeArenaAdminError(w http.ResponseWriter, r *http.Request, err error) {
	var conflict *ArenaRevisionConflictError
	if errors.As(err, &conflict) {
		payload := api.ArenaRevisionConflict{
			ExpectedRevision: conflict.ExpectedRevision,
			CurrentRevision:  conflict.CurrentRevision,
		}
		if conflict.CurrentState != "" {
			state := api.ArenaTournamentState(conflict.CurrentState)
			payload.CurrentState = &state
		}
		response.WriteJSON(w, http.StatusConflict, payload)
		return
	}

	var pairing *ArenaPairingValidationError
	if errors.As(err, &pairing) {
		writeProblem(w, r, http.StatusBadRequest, pairing.Error())
		return
	}

	if errors.Is(err, arenausecase.ErrTournamentNotFound) ||
		errors.Is(err, arenausecase.ErrRosterNotFound) ||
		errors.Is(err, arenausecase.ErrSwissRoundNotFound) {
		writeProblem(w, r, http.StatusNotFound, "arena resource not found")
		return
	}
	errmap.HandleError(w, r, err)
}
