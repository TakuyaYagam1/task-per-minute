package v1

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type arenaParticipantController struct {
	api.Unimplemented

	service           ArenaParticipantService
	submissionLimiter ArenaSubmissionRateLimiter
}

func newArenaParticipantController(
	service ArenaParticipantService,
	submissionLimiter ArenaSubmissionRateLimiter,
) *arenaParticipantController {
	return &arenaParticipantController{service: service, submissionLimiter: submissionLimiter}
}

func (c *arenaParticipantController) GetArenaParticipantLobby(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.ArenaTournamentId,
) {
	actor, ok := arenaParticipantFromRequest(w, r)
	if !ok {
		return
	}
	if c.service == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	if tournamentID == uuid.Nil {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}

	result, err := c.service.GetLobby(r.Context(), ArenaParticipantLobbyCommand{
		Actor: actor, TournamentID: tournamentID,
	})
	if err != nil {
		writeArenaParticipantError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, result)
}

func (c *arenaParticipantController) GetArenaParticipantAssignment(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.ArenaTournamentId,
	assignmentID api.ArenaAssignmentId,
) {
	actor, ok := arenaParticipantFromRequest(w, r)
	if !ok {
		return
	}
	if c.service == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	if tournamentID == uuid.Nil || assignmentID == uuid.Nil {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}

	result, err := c.service.GetAssignment(r.Context(), ArenaParticipantAssignmentCommand{
		Actor: actor, TournamentID: tournamentID, AssignmentID: assignmentID,
	})
	if err != nil {
		writeArenaParticipantError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, result)
}

func (c *arenaParticipantController) SetArenaParticipantReady(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.ArenaTournamentId,
	waveID api.ArenaWaveId,
	params api.SetArenaParticipantReadyParams,
) {
	actor, ok := arenaParticipantFromRequest(w, r)
	if !ok {
		return
	}
	if c.service == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	var body api.ArenaParticipantReadyRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	if tournamentID == uuid.Nil || waveID == uuid.Nil || params.IdempotencyKey == uuid.Nil ||
		body.ExpectedProjectionRevision < 1 {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}

	result, err := c.service.SetReady(r.Context(), ArenaParticipantReadyCommand{
		Actor: actor, TournamentID: tournamentID, WaveID: waveID,
		CommandID: params.IdempotencyKey, ExpectedProjectionRevision: body.ExpectedProjectionRevision,
		Ready: body.Ready,
	})
	if err != nil {
		writeArenaParticipantError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, result)
}

func (c *arenaParticipantController) GetArenaParticipantSnapshot(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.ArenaTournamentId,
	params api.GetArenaParticipantSnapshotParams,
) {
	actor, ok := arenaParticipantFromRequest(w, r)
	if !ok {
		return
	}
	if c.service == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	if tournamentID == uuid.Nil || !validArenaParticipantCursor(params.Cursor) {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}

	result, err := c.service.GetSnapshot(r.Context(), ArenaParticipantSnapshotCommand{
		Actor: actor, TournamentID: tournamentID, Cursor: cloneArenaParticipantCursor(params.Cursor),
	})
	if err != nil {
		writeArenaParticipantError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, result)
}

func (c *arenaParticipantController) ApplyArenaParticipantPostSeriesAction(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.ArenaTournamentId,
	seriesID api.ArenaSeriesId,
	params api.ApplyArenaParticipantPostSeriesActionParams,
) {
	actor, ok := arenaParticipantFromRequest(w, r)
	if !ok {
		return
	}
	if c.service == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	var body api.ArenaParticipantPostSeriesRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	action := ArenaParticipantPostSeriesAction(body.Action)
	if tournamentID == uuid.Nil || seriesID == uuid.Nil || params.IdempotencyKey == uuid.Nil ||
		body.ExpectedProjectionRevision < 1 || !action.IsValid() {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}

	result, err := c.service.ApplyPostSeriesAction(r.Context(), ArenaParticipantPostSeriesCommand{
		Actor: actor, TournamentID: tournamentID, SeriesID: seriesID,
		CommandID: params.IdempotencyKey, ExpectedProjectionRevision: body.ExpectedProjectionRevision,
		Action: action,
	})
	if err != nil {
		writeArenaParticipantError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, result)
}

func validArenaParticipantCursor(cursor *api.ArenaParticipantRecoveryCursor) bool {
	return cursor == nil || (cursor.ProjectionRevision >= 1 &&
		cursor.ParticipantViewRevision >= 1 && cursor.EventSequence >= 0)
}

func cloneArenaParticipantCursor(
	cursor *api.ArenaParticipantRecoveryCursor,
) *api.ArenaParticipantRecoveryCursor {
	if cursor == nil {
		return nil
	}
	cloned := *cursor
	return &cloned
}

func arenaParticipantFromRequest(
	w http.ResponseWriter,
	r *http.Request,
) (ArenaParticipantIdentity, bool) {
	player, ok := middleware.GetPlayerFromCtx(r.Context())
	if !ok || player.ID == uuid.Nil {
		errmap.HandleError(w, r, domain.ErrInvalidSession)
		return ArenaParticipantIdentity{}, false
	}
	return ArenaParticipantIdentity{PlayerID: player.ID}, true
}

func writeArenaParticipantError(w http.ResponseWriter, r *http.Request, err error) {
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
	errmap.HandleError(w, r, err)
}
