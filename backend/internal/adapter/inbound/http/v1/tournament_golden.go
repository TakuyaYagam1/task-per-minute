package v1

import (
	"net/http"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func (s *Server) GetGoldenOperatorState(w http.ResponseWriter, r *http.Request, tournamentID api.TournamentId) {
	operator, _, ok := s.requireAdminService(w, r)
	if !ok || !s.requireGolden(w, r) {
		return
	}
	view, err := s.golden.OperatorView(r.Context(), usecase.GoldenOperatorQuery{
		TournamentID: tournamentID, OperatorID: operator.ActorID,
	})
	s.writeGoldenOperator(w, r, view, err)
}

func (s *Server) OpenGoldenExecution(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
	params api.OpenGoldenExecutionParams,
) {
	operator, _, ok := s.requireAdminService(w, r)
	if !ok || !s.requireGolden(w, r) {
		return
	}
	var body api.GoldenOpenRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	view, err := s.golden.Open(r.Context(), usecase.GoldenOpenCommand{
		TournamentID: tournamentID, CommandID: params.IdempotencyKey,
		ExpectedProjectionRevision: body.ExpectedProjectionRevision,
		GoldenMutationScope: usecase.GoldenMutationScope{
			ActorID: operator.ActorID, ExpectedRuntimeRevision: body.ExpectedRuntimeRevision,
		},
	})
	s.writeGoldenOperator(w, r, view, err)
}

func (s *Server) StartGoldenAttempt(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
	attemptID api.GoldenAttemptId,
	params api.StartGoldenAttemptParams,
) {
	operator, _, ok := s.requireAdminService(w, r)
	if !ok || !s.requireGolden(w, r) {
		return
	}
	var body api.GoldenStartRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	view, err := s.golden.Start(r.Context(), usecase.GoldenStartCommand{
		TournamentID: tournamentID, AttemptID: attemptID, CommandID: params.IdempotencyKey,
		GoldenMutationScope: usecase.GoldenMutationScope{
			ActorID: operator.ActorID, ExpectedRuntimeRevision: body.ExpectedRuntimeRevision,
			ExpectedAttemptID: attemptID, ExpectedReadyWindowID: body.ReadyWindowId,
		},
	})
	s.writeGoldenOperator(w, r, view, err)
}

func (s *Server) GetGoldenParticipantState(w http.ResponseWriter, r *http.Request, tournamentID api.TournamentId) {
	actor, ok := participantIdentityFromRequest(w, r)
	if !ok || !s.requireGolden(w, r) {
		return
	}
	view, err := s.golden.ParticipantView(r.Context(), usecase.GoldenParticipantQuery{
		TournamentID: tournamentID, PlayerID: actor.PlayerID,
	})
	s.writeGoldenParticipant(w, r, actor, view, err)
}

func (s *Server) SetGoldenParticipantReady(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
	params api.SetGoldenParticipantReadyParams,
) {
	actor, ok := participantIdentityFromRequest(w, r)
	if !ok || !s.requireGolden(w, r) {
		return
	}
	var body api.GoldenReadyRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	if !bool(body.Ready) {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}
	view, err := s.golden.SetReady(r.Context(), usecase.GoldenReadyCommand{
		TournamentID: tournamentID, PlayerID: actor.PlayerID,
		CommandID: params.IdempotencyKey, Ready: true,
		GoldenMutationScope: usecase.GoldenMutationScope{
			ActorID: actor.PlayerID, ExpectedRuntimeRevision: body.ExpectedRuntimeRevision,
			ExpectedAttemptID: body.AttemptId, ExpectedReadyWindowID: body.ReadyWindowId,
		},
	})
	s.writeGoldenParticipant(w, r, actor, view, err)
}

func (s *Server) SubmitGoldenFlag(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
	params api.SubmitGoldenFlagParams,
) {
	actor, ok := participantIdentityFromRequest(w, r)
	if !ok || !s.requireGolden(w, r) {
		return
	}
	var body api.GoldenSubmissionRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	view, err := s.golden.Submit(r.Context(), usecase.GoldenSubmissionCommand{
		TournamentID: tournamentID, PlayerID: actor.PlayerID,
		CommandID: params.IdempotencyKey, SubmittedFlag: body.SubmittedFlag,
		GoldenMutationScope: usecase.GoldenMutationScope{
			ActorID: actor.PlayerID, ExpectedRuntimeRevision: body.ExpectedRuntimeRevision,
			ExpectedAttemptID: body.AttemptId, ExpectedReadyWindowID: body.ReadyWindowId,
		},
	})
	s.writeGoldenParticipant(w, r, actor, view, err)
}

func (s *Server) requireGolden(w http.ResponseWriter, r *http.Request) bool {
	if s == nil || s.golden == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return false
	}
	return true
}

func (s *Server) writeGoldenOperator(w http.ResponseWriter, r *http.Request, view usecase.GoldenOperatorView, err error) {
	if err != nil {
		writeTournamentAdminError(w, r, err)
		return
	}
	payload, err := goldenOperatorResponse(view)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, payload)
}

func (s *Server) writeGoldenParticipant(
	w http.ResponseWriter,
	r *http.Request,
	actor usecase.Identity,
	view usecase.GoldenParticipantView,
	err error,
) {
	if err != nil {
		writeParticipantError(w, r, err)
		return
	}
	sourceFileAvailable := false
	if view.Task != nil {
		if s == nil || s.participantArchive == nil {
			errmap.HandleError(w, r, domain.ErrInternal)
			return
		}
		sourceFileAvailable, err = s.participantArchive.SourceFileAvailable(
			r.Context(),
			usecase.ParticipantArchiveQuery{
				Actor: actor, TournamentID: view.TournamentID, AssignmentID: view.Task.AssignmentID,
			},
		)
		if err != nil {
			writeParticipantError(w, r, err)
			return
		}
	}
	payload, err := goldenParticipantResponse(view, sourceFileAvailable)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, payload)
}

func goldenOperatorResponse(view usecase.GoldenOperatorView) (api.GoldenOperatorResponse, error) {
	payload := api.GoldenOperatorResponse{TournamentId: view.TournamentID, ObservedAt: view.ObservedAt, Groups: make([]api.GoldenOperatorGroup, len(view.Groups))}
	for index, group := range view.Groups {
		state := api.GoldenRuntimeState(group.State)
		if !state.Valid() || group.PositionFrom < 1 || group.PositionTo < group.PositionFrom ||
			group.PositionTo > domain.TournamentMaxParticipants {
			return api.GoldenOperatorResponse{}, domain.ErrInternal
		}
		members := make([]api.GoldenRuntimeMember, len(group.Members))
		for memberIndex, member := range group.Members {
			members[memberIndex] = api.GoldenRuntimeMember{ParticipantId: member.ParticipantID, Ready: member.Ready, Submitted: member.Submitted, Position: int32Pointer(member.Position)}
		}
		payload.Groups[index] = api.GoldenOperatorGroup{
			GroupId: group.GroupID, GroupRevisionId: group.GroupRevisionID, AttemptId: group.AttemptID,
			State:           state,
			RuntimeRevision: group.RuntimeRevision, ReadyWindowId: group.ReadyWindowID,
			//nolint:gosec // Positions were validated against the tournament domain bound above.
			PositionFrom: int32(group.PositionFrom), PositionTo: int32(group.PositionTo),
			StartedAt: group.StartedAt, Deadline: group.Deadline, Members: members,
		}
	}
	return payload, nil
}

func goldenParticipantResponse(
	view usecase.GoldenParticipantView,
	sourceFileAvailable bool,
) (api.GoldenParticipantResponse, error) {
	state := api.GoldenRuntimeState(view.State)
	if !state.Valid() {
		return api.GoldenParticipantResponse{}, domain.ErrInternal
	}
	payload := api.GoldenParticipantResponse{
		TournamentId: view.TournamentID, ParticipantId: view.ParticipantID, GroupId: view.GroupID,
		GroupRevisionId: view.GroupRevisionID, AttemptId: view.AttemptID, State: state,
		RuntimeRevision: view.RuntimeRevision, ReadyWindowId: view.ReadyWindowID,
		Ready: view.Ready, Submitted: view.Submitted, Position: int32Pointer(view.Position),
		StartedAt: view.StartedAt, Deadline: view.Deadline,
	}
	if view.Task != nil {
		if view.Task.TimeLimitSeconds != 180 {
			return api.GoldenParticipantResponse{}, domain.ErrInternal
		}
		payload.Task = &api.GoldenRuntimeTask{
			AssignmentId: view.Task.AssignmentID, SnapshotId: view.Task.SnapshotID, TaskId: view.Task.TaskID,
			Title: view.Task.Title, Category: view.Task.Category, Difficulty: view.Task.Difficulty,
			TimeLimitSeconds:    api.GoldenRuntimeTaskTimeLimitSeconds(view.Task.TimeLimitSeconds),
			SourceFileAvailable: sourceFileAvailable,
		}
	}
	return payload, nil
}

func int32Pointer(value *int) *int32 {
	if value == nil {
		return nil
	}
	//nolint:gosec // Runtime positions are validated against the 4-16 player tournament bound.
	converted := int32(*value)
	return &converted
}
