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
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func (s *Server) GetParticipantLobby(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
) {
	actor, ok := participantIdentityFromRequest(w, r)
	if !ok {
		return
	}
	if s == nil || s.tournamentParticipant == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	view, err := s.tournamentParticipant.GetLobby(r.Context(), usecase.LobbyQuery{
		Actor: actor, TournamentID: tournamentID,
	})
	if err != nil {
		writeParticipantError(w, r, err)
		return
	}
	payload, err := participantLobbyResponse(view)
	if err != nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	response.WriteJSON(w, http.StatusOK, payload)
}

func (s *Server) GetParticipantAssignment(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
	assignmentID api.AssignmentId,
) {
	actor, ok := participantIdentityFromRequest(w, r)
	if !ok {
		return
	}
	if s == nil || s.tournamentParticipant == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	result, err := s.tournamentParticipant.GetAssignment(r.Context(), usecase.AssignmentQuery{
		Actor: actor, TournamentID: tournamentID, AssignmentID: assignmentID,
	})
	if err != nil {
		writeParticipantError(w, r, err)
		return
	}
	payload, err := participantAssignmentResponse(result)
	if err != nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	response.WriteJSON(w, http.StatusOK, payload)
}

func (s *Server) GetParticipantAssignmentSourceFile(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
	assignmentID api.AssignmentId,
) {
	actor, ok := participantIdentityFromRequest(w, r)
	if !ok {
		return
	}
	if s == nil || s.participantArchive == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	download, err := s.participantArchive.GetSourceFile(r.Context(), usecase.ParticipantArchiveQuery{
		Actor: actor, TournamentID: tournamentID, AssignmentID: assignmentID,
	})
	if err != nil {
		writeParticipantError(w, r, err)
		return
	}
	if download.URL == "" || download.ExpiresAt.IsZero() {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	response.WriteJSON(w, http.StatusOK, api.ParticipantSourceFileResponse{
		SourceFileUrl: download.URL,
		ExpiresAt:     download.ExpiresAt.UTC(),
	})
}

func (s *Server) GetParticipantSnapshot(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
	params api.GetParticipantSnapshotParams,
) {
	actor, ok := participantIdentityFromRequest(w, r)
	if !ok {
		return
	}
	if s == nil || s.tournamentParticipant == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	query := usecase.SnapshotQuery{Actor: actor, TournamentID: tournamentID}
	if params.Cursor != nil {
		query.Cursor = &usecase.RecoveryCursor{
			ProjectionRevision:      params.Cursor.ProjectionRevision,
			ParticipantViewRevision: params.Cursor.ParticipantViewRevision,
			EventSequence:           params.Cursor.EventSequence,
		}
	}
	view, err := s.tournamentParticipant.GetSnapshot(r.Context(), query)
	if err != nil {
		writeParticipantError(w, r, err)
		return
	}
	payload, err := participantRecoveryResponse(view)
	if err != nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	response.WriteJSON(w, http.StatusOK, payload)
}

func (s *Server) SetParticipantReady(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
	waveID api.WaveId,
	params api.SetParticipantReadyParams,
) {
	actor, ok := participantIdentityFromRequest(w, r)
	if !ok {
		return
	}
	if s.tournamentParticipant == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	var body api.ParticipantReadyRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	event, err := s.tournamentParticipant.SetReady(r.Context(), usecase.ReadyCommand{
		Actor:                      actor,
		TournamentID:               tournamentID,
		WaveID:                     waveID,
		CommandID:                  params.IdempotencyKey,
		ExpectedProjectionRevision: body.ExpectedProjectionRevision,
		Ready:                      body.Ready,
	})
	if err != nil {
		writeParticipantError(w, r, err)
		return
	}
	payload, err := participantReadinessResponse(event)
	if err != nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	response.WriteJSON(w, http.StatusOK, payload)
}

func (s *Server) SubmitParticipantDraftAction(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
	seriesID api.SeriesId,
	params api.SubmitParticipantDraftActionParams,
) {
	actor, ok := participantIdentityFromRequest(w, r)
	if !ok {
		return
	}
	if s.tournamentParticipant == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	var body api.ParticipantDraftActionRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	execution, err := s.tournamentParticipant.SubmitDraftAction(
		r.Context(),
		usecase.DraftActionCommand{
			Actor:                      actor,
			TournamentID:               tournamentID,
			SeriesID:                   seriesID,
			CommandID:                  params.IdempotencyKey,
			ExpectedProjectionRevision: body.ExpectedProjectionRevision,
			ExpectedDraftRevision:      body.ExpectedDraftRevision,
			ExpectedTurn:               int(body.ExpectedTurn),
			Action:                     domain.DraftActionType(body.Action),
			Category:                   domain.Category(body.Category),
		},
	)
	if err != nil {
		writeParticipantError(w, r, err)
		return
	}
	payload, err := participantDraftResponse(execution)
	if err != nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	response.WriteJSON(w, http.StatusOK, payload)
}

func (s *Server) SubmitParticipantFlag(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
	seriesID api.SeriesId,
	gameID api.GameId,
	params api.SubmitParticipantFlagParams,
) {
	actor, ok := participantIdentityFromRequest(w, r)
	if !ok {
		return
	}
	if s.tournamentParticipant == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	var body api.ParticipantSubmissionRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	if body.SubmittedFlag == nil {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}
	result, err := s.tournamentParticipant.SubmitFlag(r.Context(), usecase.SubmissionCommand{
		Actor:                      actor,
		TournamentID:               tournamentID,
		SeriesID:                   seriesID,
		GameID:                     gameID,
		CommandID:                  params.IdempotencyKey,
		ExpectedProjectionRevision: body.ExpectedProjectionRevision,
		SubmittedFlag:              *body.SubmittedFlag,
	})
	if err != nil {
		writeParticipantError(w, r, err)
		return
	}
	payload, err := participantSubmissionResponse(result)
	if err != nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	response.WriteJSON(w, http.StatusOK, payload)
}

func (s *Server) SurrenderParticipantSeries(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
	seriesID api.SeriesId,
	params api.SurrenderParticipantSeriesParams,
) {
	actor, ok := participantIdentityFromRequest(w, r)
	if !ok {
		return
	}
	if s.tournamentParticipant == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	var body api.ParticipantSurrenderRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	reason := ""
	if body.Reason != nil {
		reason = *body.Reason
	}
	revision, err := s.tournamentParticipant.Surrender(r.Context(), usecase.SurrenderCommand{
		Actor:                      actor,
		TournamentID:               tournamentID,
		SeriesID:                   seriesID,
		CommandID:                  params.IdempotencyKey,
		ExpectedProjectionRevision: body.ExpectedProjectionRevision,
		Confirmed:                  body.Confirmed,
		Reason:                     reason,
	})
	if err != nil {
		writeParticipantError(w, r, err)
		return
	}
	payload, err := participantOfficialResultResponse(revision)
	if err != nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	response.WriteJSON(w, http.StatusOK, payload)
}

func (s *Server) ApplyParticipantPostSeriesAction(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
	seriesID api.SeriesId,
	params api.ApplyParticipantPostSeriesActionParams,
) {
	actor, ok := participantIdentityFromRequest(w, r)
	if !ok {
		return
	}
	if s.tournamentParticipant == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	var body api.ParticipantPostSeriesRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	result, err := s.tournamentParticipant.ApplyPostSeriesAction(
		r.Context(),
		usecase.PostSeriesCommand{
			Actor:                      actor,
			TournamentID:               tournamentID,
			SeriesID:                   seriesID,
			CommandID:                  params.IdempotencyKey,
			ExpectedProjectionRevision: body.ExpectedProjectionRevision,
			Action:                     usecase.PostSeriesAction(body.Action),
		},
	)
	if err != nil {
		writeParticipantError(w, r, err)
		return
	}
	payload, err := participantPostSeriesResponse(result)
	if err != nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	response.WriteJSON(w, http.StatusOK, payload)
}

func participantIdentityFromRequest(
	w http.ResponseWriter,
	r *http.Request,
) (usecase.Identity, bool) {
	if r == nil {
		return usecase.Identity{}, false
	}
	player, ok := middleware.GetPlayerFromCtx(r.Context())
	if !ok || player.ID == uuid.Nil {
		errmap.HandleError(w, r, domain.ErrInvalidSession)
		return usecase.Identity{}, false
	}
	return usecase.Identity{PlayerID: player.ID}, true
}

func writeParticipantError(w http.ResponseWriter, r *http.Request, err error) {
	var conflict *usecase.RevisionConflictError
	if errors.As(err, &conflict) {
		if conflict.ExpectedRevision < 1 || conflict.CurrentRevision < 1 {
			errmap.HandleError(w, r, domain.ErrInternal)
			return
		}
		detail, instance, requestID := tournamentProblemContext(r, "projection revision conflict")
		payload := api.ProjectionRevisionProblem{
			Type: "about:blank", Title: http.StatusText(http.StatusConflict),
			Status: int32(http.StatusConflict), Detail: &detail, Instance: &instance, RequestId: &requestID,
			ExpectedRevision: conflict.ExpectedRevision,
			CurrentRevision:  conflict.CurrentRevision,
		}
		if conflict.CurrentState != "" {
			state := api.TournamentState(conflict.CurrentState)
			if !state.Valid() {
				errmap.HandleError(w, r, domain.ErrInternal)
				return
			}
			payload.CurrentState = &state
		}
		response.WriteProblem(w, http.StatusConflict, payload)
		return
	}
	errmap.HandleError(w, r, err)
}
