package v1

import (
	"net/http"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func (c *tournamentController) ConfigureTournamentPairings(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
	params api.ConfigureTournamentPairingsParams,
) {
	operator, service, ok := c.requireAdminService(w, r)
	if !ok {
		return
	}
	var body api.PairingConfigurationRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	command := inbound.AdminPairingCommand{
		AdminCommandScope: inbound.AdminCommandScope{
			Operator: operator, TournamentID: tournamentID, CommandID: params.IdempotencyKey,
		},
		ExpectedProjectionRevision: body.ExpectedProjectionRevision,
		RoundNumber:                int(body.RoundNumber),
		PairingMode:                inbound.AdminPairingMode(body.PairingMode),
		CategoryMode:               domain.CategoryMode(body.CategoryMode),
		Categories:                 make([]domain.Category, len(body.Categories)),
		ManualPairingsProvided:     body.ManualPairings != nil,
		ManualByeParticipantID:     cloneUUIDPointer(body.ManualByeParticipantId),
	}
	for index, category := range body.Categories {
		command.Categories[index] = domain.Category(category)
	}
	if body.ManualPairings != nil {
		command.ManualPairings = make([]inbound.AdminParticipantPair, len(*body.ManualPairings))
		for index, pairing := range *body.ManualPairings {
			command.ManualPairings[index] = inbound.AdminParticipantPair{
				FirstParticipantID:  pairing.FirstParticipantId,
				SecondParticipantID: pairing.SecondParticipantId,
			}
		}
	}
	view, err := service.ConfigurePairings(r.Context(), command)
	if err != nil {
		writeTournamentAdminError(w, r, err)
		return
	}
	payload, err := tournamentSwissRoundResponse(view)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, payload)
}

func (c *tournamentController) ApplyTournamentAction(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
	params api.ApplyTournamentActionParams,
) {
	operator, service, ok := c.requireAdminService(w, r)
	if !ok {
		return
	}
	var body api.TournamentActionRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	action, err := tournamentAction(body.Action)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	view, err := service.ApplyTournamentAction(r.Context(), inbound.AdminTournamentActionCommand{
		AdminCommandScope: inbound.AdminCommandScope{
			Operator: operator, TournamentID: tournamentID, CommandID: params.IdempotencyKey,
		},
		ExpectedProjectionRevision: body.ExpectedProjectionRevision,
		Action:                     action,
		Confirmed:                  body.Confirmed,
		Reason:                     optionalString(body.Reason),
	})
	if err != nil {
		writeTournamentAdminError(w, r, err)
		return
	}
	payload, err := tournamentResponse(view)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, payload)
}

// tournamentAction deliberately excludes complete. Final publication is the
// only production path that completes a tournament after exact terminal proof.
func tournamentAction(action api.TournamentActionRequestAction) (inbound.AdminTournamentAction, error) {
	value := inbound.AdminTournamentAction(action)
	//nolint:exhaustive // This switch intentionally handles only the valid states for this boundary.
	switch value {
	case inbound.AdminTournamentActionOpenRegistration,
		inbound.AdminTournamentActionStartSwiss,
		inbound.AdminTournamentActionStartGolden,
		inbound.AdminTournamentActionStartPlayoffs,
		inbound.AdminTournamentActionPause,
		inbound.AdminTournamentActionResume,
		inbound.AdminTournamentActionCancel:
		return value, nil
	default:
		return "", domain.ErrValidation
	}
}

func (c *tournamentController) ControlTournamentWave(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
	waveID api.WaveId,
	params api.ControlTournamentWaveParams,
) {
	operator, service, ok := c.requireAdminService(w, r)
	if !ok {
		return
	}
	var body api.WaveControlRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	view, err := service.ControlWave(r.Context(), inbound.AdminWaveCommand{
		AdminCommandScope: inbound.AdminCommandScope{
			Operator: operator, TournamentID: tournamentID, CommandID: params.IdempotencyKey,
		},
		WaveID:                     waveID,
		ExpectedProjectionRevision: body.ExpectedProjectionRevision,
		Action:                     inbound.AdminWaveAction(body.Action),
		Confirmed:                  body.Confirmed,
		Reason:                     optionalString(body.Reason),
	})
	if err != nil {
		writeTournamentAdminError(w, r, err)
		return
	}
	payload, err := tournamentWaveResponse(view)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, payload)
}

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
