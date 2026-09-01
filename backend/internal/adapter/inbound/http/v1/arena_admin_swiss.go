package v1

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func (c *arenaAdminController) ConfigureArenaTournamentPairings(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.ArenaTournamentId,
	params api.ConfigureArenaTournamentPairingsParams,
) {
	operator, ok := arenaOperatorFromRequest(w, r)
	if !ok {
		return
	}
	if c.service == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	var body api.ArenaPairingConfigurationRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	pairingMode, ok := arenaPairingMode(body.PairingMode)
	if !ok {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}
	manualPairings := arenaManualPairings(body.ManualPairings)
	categories := make([]domain.Category, len(body.Categories))
	for index := range body.Categories {
		categories[index] = domain.Category(body.Categories[index])
	}

	var repeatOverride *ArenaPairingRepeatOverrideCommand
	if body.RepeatOverride != nil {
		repeatOverride = &ArenaPairingRepeatOverrideCommand{
			Confirmed: body.RepeatOverride.Confirmed,
			Reason:    strings.TrimSpace(body.RepeatOverride.Reason),
		}
	}
	result, err := c.service.ConfigurePairings(r.Context(), ArenaConfigurePairingsCommand{
		Operator: operator, TournamentID: tournamentID, CommandID: params.IdempotencyKey,
		ExpectedRevision: body.ExpectedProjectionRevision, RoundNumber: body.RoundNumber,
		PairingMode: pairingMode, CategoryMode: domain.ArenaCategoryMode(body.CategoryMode), Categories: categories,
		ManualPairings: manualPairings, ManualByeParticipantID: cloneArenaUUID(body.ManualByeParticipantId),
		RepeatOverride: repeatOverride,
	})
	if err != nil {
		writeArenaAdminError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, result)
}

func (c *arenaAdminController) GetArenaPublicScoreboard(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.ArenaTournamentId,
) {
	if c.service == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	result, err := c.service.GetStandings(r.Context(), ArenaTournamentReadCommand{TournamentID: tournamentID})
	if err != nil {
		writeArenaAdminError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, result)
}

func (c *arenaAdminController) GetArenaPublicBracket(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.ArenaTournamentId,
) {
	if c.service == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	result, err := c.service.GetBracket(r.Context(), ArenaTournamentReadCommand{TournamentID: tournamentID})
	if err != nil {
		writeArenaAdminError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, result)
}

func arenaPairingMode(value api.ArenaPairingConfigurationRequestPairingMode) (ArenaPairingMode, bool) {
	mode := ArenaPairingMode(value)
	switch mode {
	case ArenaPairingModeAutomatic, ArenaPairingModeManual:
		return mode, true
	default:
		return "", false
	}
}

func arenaManualPairings(input *[]api.ArenaManualPairInput) []ArenaManualPairCommand {
	if input == nil {
		return nil
	}
	pairings := make([]ArenaManualPairCommand, len(*input))
	for index, pair := range *input {
		pairings[index] = ArenaManualPairCommand{
			FirstParticipantID: pair.FirstParticipantId, SecondParticipantID: pair.SecondParticipantId,
		}
	}
	return pairings
}

func cloneArenaUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
