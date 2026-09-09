package v1

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func (c *tournamentController) adminService() inbound.TournamentAdminUseCase {
	if c == nil {
		return nil
	}
	return c.admin
}

func tournamentAdminIdentity(operatorID uuid.UUID) inbound.AdminOperatorIdentity {
	return inbound.AdminOperatorIdentity{ActorID: operatorID}
}

func (c *tournamentController) requireAdminService(
	w http.ResponseWriter,
	r *http.Request,
) (inbound.AdminOperatorIdentity, inbound.TournamentAdminUseCase, bool) {
	operator, ok := operatorFromRequest(w, r)
	if !ok {
		return inbound.AdminOperatorIdentity{}, nil, false
	}
	service := c.adminService()
	if service == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return inbound.AdminOperatorIdentity{}, nil, false
	}
	return tournamentAdminIdentity(operator.ActorID), service, true
}

func writeTournamentAdminError(w http.ResponseWriter, r *http.Request, err error) {
	var conflict *inbound.AdminRevisionConflictError
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
	writeTournamentError(w, r, err)
}
