package v1

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func (c *arenaAdminController) ControlArenaTournamentWave(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.ArenaTournamentId,
	waveID api.ArenaWaveId,
	params api.ControlArenaTournamentWaveParams,
) {
	operator, ok := arenaOperatorFromRequest(w, r)
	if !ok {
		return
	}
	if c.service == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	var body api.ArenaWaveControlRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	action := ArenaWaveControlAction(body.Action)
	reason := ""
	if body.Reason != nil {
		reason = strings.TrimSpace(*body.Reason)
	}
	if tournamentID == uuid.Nil || waveID == uuid.Nil || params.IdempotencyKey == uuid.Nil ||
		body.ExpectedProjectionRevision < 1 || !action.IsValid() || utf8.RuneCountInString(reason) > 512 {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}

	result, err := c.service.ControlWave(r.Context(), ArenaWaveControlCommand{
		Operator: operator, TournamentID: tournamentID, WaveID: waveID,
		CommandID: params.IdempotencyKey, ExpectedRevision: body.ExpectedProjectionRevision,
		Action: action, Confirmed: body.Confirmed, Reason: reason,
	})
	if err != nil {
		writeArenaAdminError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, result)
}

func (c *arenaParticipantController) SubmitArenaParticipantDraftAction(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.ArenaTournamentId,
	seriesID api.ArenaSeriesId,
	params api.SubmitArenaParticipantDraftActionParams,
) {
	actor, ok := arenaParticipantFromRequest(w, r)
	if !ok {
		return
	}
	if c.service == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	var body api.ArenaParticipantDraftActionRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	action := domain.ArenaDraftActionType(body.Action)
	category := domain.Category(body.Category)
	if tournamentID == uuid.Nil || seriesID == uuid.Nil || params.IdempotencyKey == uuid.Nil ||
		body.ExpectedProjectionRevision < 1 || body.ExpectedDraftRevision < 1 || body.ExpectedTurn < 1 ||
		!action.IsValid() || !category.IsValid() {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}

	result, err := c.service.SubmitDraftAction(r.Context(), ArenaParticipantDraftActionCommand{
		Actor: actor, TournamentID: tournamentID, SeriesID: seriesID,
		CommandID: params.IdempotencyKey, ExpectedProjectionRevision: body.ExpectedProjectionRevision,
		ExpectedDraftRevision: body.ExpectedDraftRevision, ExpectedTurn: body.ExpectedTurn,
		Action: action, Category: category,
	})
	if err != nil {
		writeArenaParticipantError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, result)
}
