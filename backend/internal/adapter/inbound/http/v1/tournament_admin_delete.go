package v1

import (
	"net/http"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func (c *tournamentController) DeleteTournament(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
	params api.DeleteTournamentParams,
) {
	operator, service, ok := c.requireAdminService(w, r)
	if !ok {
		return
	}
	var body api.DeleteTournamentRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	_, err := service.DeleteTournament(r.Context(), inbound.AdminTournamentDeletionCommand{
		AdminCommandScope: inbound.AdminCommandScope{
			Operator: operator, TournamentID: tournamentID, CommandID: params.IdempotencyKey,
		},
		ExpectedRevision: body.ExpectedRevision,
		Confirmed:        bool(body.Confirmed),
	})
	writeTournamentNoContent(w, r, err)
}
