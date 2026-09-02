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

const (
	arenaSubmittedFlagMaxRunes   = 4096
	arenaSurrenderReasonMaxRunes = 512
)

func (c *arenaParticipantController) SubmitArenaParticipantFlag(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.ArenaTournamentId,
	seriesID api.ArenaSeriesId,
	gameID api.ArenaGameId,
	params api.SubmitArenaParticipantFlagParams,
) {
	actor, ok := arenaParticipantFromRequest(w, r)
	if !ok {
		return
	}
	if c.service == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	if !c.allowArenaParticipantCommand(w, r, actor, "flag") {
		return
	}

	var body api.ArenaParticipantSubmissionRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	if tournamentID == uuid.Nil || seriesID == uuid.Nil || gameID == uuid.Nil ||
		params.IdempotencyKey == uuid.Nil || body.ExpectedProjectionRevision < 1 ||
		body.SubmittedFlag == nil || utf8.RuneCountInString(*body.SubmittedFlag) < 1 ||
		utf8.RuneCountInString(*body.SubmittedFlag) > arenaSubmittedFlagMaxRunes {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}

	result, err := c.service.SubmitFlag(r.Context(), ArenaParticipantSubmissionCommand{
		Actor: actor, TournamentID: tournamentID, SeriesID: seriesID, GameID: gameID,
		CommandID: params.IdempotencyKey, ExpectedProjectionRevision: body.ExpectedProjectionRevision,
		SubmittedFlag: *body.SubmittedFlag,
	})
	if err != nil {
		writeArenaParticipantError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, result)
}

func (c *arenaParticipantController) SurrenderArenaParticipantSeries(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.ArenaTournamentId,
	seriesID api.ArenaSeriesId,
	params api.SurrenderArenaParticipantSeriesParams,
) {
	actor, ok := arenaParticipantFromRequest(w, r)
	if !ok {
		return
	}
	if c.service == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	if !c.allowArenaParticipantCommand(w, r, actor, "surrender") {
		return
	}

	var body api.ArenaParticipantSurrenderRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	reason := ""
	if body.Reason != nil {
		reason = strings.TrimSpace(*body.Reason)
	}
	if tournamentID == uuid.Nil || seriesID == uuid.Nil || params.IdempotencyKey == uuid.Nil ||
		body.ExpectedProjectionRevision < 1 || !body.Confirmed ||
		utf8.RuneCountInString(reason) > arenaSurrenderReasonMaxRunes {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}

	result, err := c.service.Surrender(r.Context(), ArenaParticipantSurrenderCommand{
		Actor: actor, TournamentID: tournamentID, SeriesID: seriesID,
		CommandID: params.IdempotencyKey, ExpectedProjectionRevision: body.ExpectedProjectionRevision,
		Confirmed: body.Confirmed, Reason: reason,
	})
	if err != nil {
		writeArenaParticipantError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, result)
}

func (c *arenaParticipantController) allowArenaParticipantCommand(
	w http.ResponseWriter,
	r *http.Request,
	actor ArenaParticipantIdentity,
	operation string,
) bool {
	if c.submissionLimiter == nil {
		return true
	}
	if c.submissionLimiter.Allow(operation + ":" + actor.PlayerID.String()) {
		return true
	}
	w.Header().Set("Retry-After", c.submissionLimiter.RetryAfter())
	errmap.HandleError(w, r, domain.ErrRateLimited)
	return false
}
