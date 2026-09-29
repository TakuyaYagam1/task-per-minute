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
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	admissionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admission"
)

func (s *Server) GetTournamentAdmissionStatus(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
) {
	actor, ok := participantIdentityFromRequest(w, r)
	if !ok {
		return
	}
	if s == nil || s.tournamentAdmission == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	view, err := s.tournamentAdmission.GetStatus(r.Context(), inbound.TournamentAdmissionStatusQuery{
		Actor: actor, TournamentID: tournamentID,
	})
	if err != nil {
		writeTournamentAdmissionError(w, r, err)
		return
	}
	payload, err := tournamentAdmissionViewResponse(view)
	if err != nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	response.WriteJSON(w, http.StatusOK, payload)
}

func (s *Server) JoinTournamentAdmission(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
	params api.JoinTournamentAdmissionParams,
) {
	actor, ok := participantIdentityFromRequest(w, r)
	if !ok {
		return
	}
	if s == nil || s.tournamentAdmission == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	mutation, err := s.tournamentAdmission.Join(r.Context(), inbound.TournamentAdmissionJoinCommand{
		Actor: actor, TournamentID: tournamentID, CommandID: params.IdempotencyKey,
	})
	if err != nil {
		writeTournamentAdmissionError(w, r, err)
		return
	}
	payload, err := tournamentAdmissionMutationResponse(mutation)
	if err != nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	response.WriteJSON(w, http.StatusOK, payload)
}

func (s *Server) CheckInTournamentAdmission(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
	params api.CheckInTournamentAdmissionParams,
) {
	actor, ok := participantIdentityFromRequest(w, r)
	if !ok {
		return
	}
	if s == nil || s.tournamentAdmission == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	mutation, err := s.tournamentAdmission.CheckIn(r.Context(), inbound.TournamentAdmissionCheckInCommand{
		Actor: actor, TournamentID: tournamentID, CommandID: params.IdempotencyKey,
	})
	if err != nil {
		writeTournamentAdmissionError(w, r, err)
		return
	}
	payload, err := tournamentAdmissionMutationResponse(mutation)
	if err != nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	response.WriteJSON(w, http.StatusOK, payload)
}

func (s *Server) CancelTournamentAdmission(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
	params api.CancelTournamentAdmissionParams,
) {
	actor, ok := participantIdentityFromRequest(w, r)
	if !ok {
		return
	}
	if s == nil || s.tournamentAdmission == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	mutation, err := s.tournamentAdmission.Cancel(r.Context(), inbound.TournamentAdmissionCancelCommand{
		Actor: actor, TournamentID: tournamentID, CommandID: params.IdempotencyKey,
	})
	if err != nil {
		writeTournamentAdmissionError(w, r, err)
		return
	}
	payload, err := tournamentAdmissionMutationResponse(mutation)
	if err != nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	response.WriteJSON(w, http.StatusOK, payload)
}

func tournamentAdmissionViewResponse(view inbound.TournamentAdmissionView) (api.TournamentAdmissionView, error) {
	if err := validateTournamentAdmissionView(view); err != nil {
		return api.TournamentAdmissionView{}, domain.ErrInternal
	}
	status, state, attendance, err := tournamentAdmissionViewEnums(view)
	if err != nil {
		return api.TournamentAdmissionView{}, domain.ErrInternal
	}
	participantID, seed := tournamentAdmissionParticipantFields(view)
	return api.TournamentAdmissionView{
		TournamentId:      view.TournamentID,
		PlayerId:          view.PlayerID,
		ParticipantId:     participantID,
		Seed:              seed,
		Status:            status,
		Attendance:        attendance,
		TournamentState:   state,
		RosterLocked:      view.RosterLocked,
		RosterRevision:    view.RosterRevision,
		PlannedRosterSize: response.IntToInt32(view.PlannedRosterSize),
		RosterSize:        response.IntToInt32(view.RosterSize),
	}, nil
}

func validateTournamentAdmissionView(view inbound.TournamentAdmissionView) error {
	if !validTournamentAdmissionIdentity(view) || !validTournamentAdmissionRoster(view) {
		return domain.ErrInternal
	}
	if view.ParticipantID == uuid.Nil {
		return validTournamentAdmissionUnregistered(view)
	}
	return validTournamentAdmissionRegistered(view)
}

func validTournamentAdmissionIdentity(view inbound.TournamentAdmissionView) bool {
	return view.TournamentID != uuid.Nil && view.PlayerID != uuid.Nil && view.Status.IsValid() &&
		view.TournamentState.IsValid()
}

func validTournamentAdmissionRoster(view inbound.TournamentAdmissionView) bool {
	return view.RosterRevision >= 1 && view.PlannedRosterSize >= domain.TournamentMinParticipants &&
		view.PlannedRosterSize <= domain.TournamentMaxParticipants && view.RosterSize >= 0 &&
		view.RosterSize <= domain.TournamentMaxParticipants
}

func validTournamentAdmissionUnregistered(view inbound.TournamentAdmissionView) error {
	if view.Seed != 0 || view.Attendance != "" || view.Status != inbound.TournamentAdmissionStatusNotRegistered {
		return domain.ErrInternal
	}
	return nil
}

func validTournamentAdmissionRegistered(view inbound.TournamentAdmissionView) error {
	if view.Seed < 1 || view.Seed > domain.TournamentMaxParticipants || !view.Attendance.IsValid() ||
		view.Status == inbound.TournamentAdmissionStatusNotRegistered {
		return domain.ErrInternal
	}
	return nil
}

func tournamentAdmissionViewEnums(
	view inbound.TournamentAdmissionView,
) (api.TournamentAdmissionStatus, api.TournamentState, *api.TournamentAdmissionViewAttendance, error) {
	status := api.TournamentAdmissionStatus(view.Status)
	state := api.TournamentState(view.TournamentState)
	if !status.Valid() || !state.Valid() {
		return "", "", nil, domain.ErrInternal
	}
	var attendance *api.TournamentAdmissionViewAttendance
	if view.Attendance != "" {
		value := api.TournamentAdmissionViewAttendance(view.Attendance)
		if !value.Valid() {
			return "", "", nil, domain.ErrInternal
		}
		attendance = &value
	}
	return status, state, attendance, nil
}

func tournamentAdmissionParticipantFields(view inbound.TournamentAdmissionView) (*uuid.UUID, *int32) {
	var participantID *uuid.UUID
	if view.ParticipantID != uuid.Nil {
		participantID = response.UUIDPtr(&view.ParticipantID)
	}
	var seed *int32
	if view.ParticipantID != uuid.Nil {
		value := response.IntToInt32(view.Seed)
		seed = &value
	}
	return participantID, seed
}

func tournamentAdmissionMutationResponse(mutation inbound.TournamentAdmissionMutation) (api.TournamentAdmissionMutation, error) {
	view, err := tournamentAdmissionViewResponse(mutation.View)
	if err != nil {
		return api.TournamentAdmissionMutation{}, err
	}
	return api.TournamentAdmissionMutation{View: view, Changed: mutation.Changed}, nil
}

func writeTournamentAdmissionError(w http.ResponseWriter, r *http.Request, err error) {
	reason, detail, ok := tournamentAdmissionConflict(err)
	if !ok {
		errmap.HandleError(w, r, err)
		return
	}
	instance := r.URL.Path
	requestID := middleware.GetRequestIDFromCtx(r.Context())
	payload := api.TournamentAdmissionConflictProblem{
		Type: "about:blank", Title: http.StatusText(http.StatusConflict), Status: http.StatusConflict,
		Detail: &detail, Instance: &instance, RequestId: &requestID, Reason: reason,
	}
	response.WriteProblem(w, http.StatusConflict, payload)
}

func tournamentAdmissionConflict(err error) (api.TournamentAdmissionConflictReason, string, bool) {
	if reason, detail, ok := tournamentAdmissionClosedConflict(err); ok {
		return reason, detail, true
	}
	if reason, detail, ok := tournamentAdmissionFullConflict(err); ok {
		return reason, detail, true
	}
	if reason, detail, ok := tournamentAdmissionWithdrawnConflict(err); ok {
		return reason, detail, true
	}
	if reason, detail, ok := tournamentAdmissionReservationConflict(err); ok {
		return reason, detail, true
	}
	if errors.Is(err, domain.ErrConflict) {
		return api.TournamentAdmissionConflictReasonConflict, domain.ErrConflict.Message, true
	}
	return "", "", false
}

func tournamentAdmissionClosedConflict(err error) (api.TournamentAdmissionConflictReason, string, bool) {
	if !errors.Is(err, admissionusecase.ErrTournamentAdmissionClosed) {
		return "", "", false
	}
	return api.TournamentAdmissionConflictReasonClosed, "tournament admission is closed", true
}

func tournamentAdmissionFullConflict(err error) (api.TournamentAdmissionConflictReason, string, bool) {
	if !errors.Is(err, admissionusecase.ErrTournamentAdmissionFull) {
		return "", "", false
	}
	return api.TournamentAdmissionConflictReasonFull, "tournament admission is full", true
}

func tournamentAdmissionWithdrawnConflict(err error) (api.TournamentAdmissionConflictReason, string, bool) {
	if !errors.Is(err, admissionusecase.ErrTournamentAdmissionWithdrawn) {
		return "", "", false
	}
	return api.TournamentAdmissionConflictReasonWithdrawn, "tournament admission was withdrawn", true
}

func tournamentAdmissionReservationConflict(err error) (api.TournamentAdmissionConflictReason, string, bool) {
	if !errors.Is(err, admissionusecase.ErrTournamentAdmissionConflict) {
		return "", "", false
	}
	return api.TournamentAdmissionConflictReasonConflictingReservation, "player has a conflicting tournament reservation", true
}
