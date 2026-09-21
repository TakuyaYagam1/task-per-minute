package v1

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func configurationService(c *tournamentController) inbound.TournamentConfigurationUseCase {
	if c == nil {
		return nil
	}
	return c.configuration
}

func (c *tournamentController) requireConfigurationService(
	w http.ResponseWriter,
	r *http.Request,
) (inbound.AdminOperatorIdentity, inbound.TournamentConfigurationUseCase, bool) {
	operator, ok := operatorFromRequest(w, r)
	if !ok {
		return inbound.AdminOperatorIdentity{}, nil, false
	}
	service := configurationService(c)
	if service == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return inbound.AdminOperatorIdentity{}, nil, false
	}
	return tournamentAdminIdentity(operator.ActorID), service, true
}

func (c *tournamentController) GetTournamentConfiguration(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
) {
	operator, service, ok := c.requireConfigurationService(w, r)
	if !ok {
		return
	}
	view, err := service.GetTournamentConfiguration(r.Context(), inbound.AdminTournamentConfigurationQuery{
		Operator: operator, TournamentID: tournamentID,
	})
	if err != nil {
		writeTournamentAdminError(w, r, err)
		return
	}
	payload, err := tournamentConfigurationResponse(view)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	response.WriteJSON(w, http.StatusOK, payload)
}

func (c *tournamentController) UpdateTournamentConfiguration(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
	params api.UpdateTournamentConfigurationParams,
) {
	operator, service, ok := c.requireConfigurationService(w, r)
	if !ok {
		return
	}
	var body api.UpdateTournamentConfigurationRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	swiss, err := tournamentConfigurationStageDefaultInput(body.SwissDefault)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	semifinal, err := tournamentConfigurationStageDefaultInput(body.SemifinalDefault)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	unlockIntents, err := tournamentConfigurationUnlockIntents(body.UnlockIntents)
	if err != nil || !domain.IsValidAssignmentReserveCount(int(body.ReserveCount)) ||
		!validConfigurationMutation(body.ExpectedProjectionRevision, body.ExpectedConfigurationRevision, bool(body.Confirmed), body.Reason) {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}
	evidence, err := service.UpdateTournamentConfiguration(r.Context(), inbound.AdminUpdateTournamentConfigurationCommand{
		Operator:                      operator,
		TournamentID:                  tournamentID,
		CommandID:                     params.IdempotencyKey,
		ExpectedProjectionRevision:    body.ExpectedProjectionRevision,
		ExpectedConfigurationRevision: body.ExpectedConfigurationRevision,
		ReserveCount:                  int(body.ReserveCount),
		Confirmed:                     bool(body.Confirmed),
		Reason:                        body.Reason,
		SwissDefault:                  swiss,
		SemifinalDefault:              semifinal,
		UnlockIntents:                 unlockIntents,
	})
	if err != nil {
		writeTournamentAdminError(w, r, err)
		return
	}
	payload, err := tournamentConfigurationMutationEvidenceResponse(evidence)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, payload)
}

func (c *tournamentController) UpdateTournamentSeriesConfiguration(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
	seriesID api.SeriesId,
	params api.UpdateTournamentSeriesConfigurationParams,
) {
	operator, service, ok := c.requireConfigurationService(w, r)
	if !ok {
		return
	}
	var body api.UpdateTournamentSeriesConfigurationRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	categories, err := tournamentConfigurationCategories(body.Categories)
	if err != nil || !validConfigurationMutation(body.ExpectedProjectionRevision, body.ExpectedSeriesRevision, bool(body.Confirmed), body.Reason) {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}
	unlockIntents, err := tournamentConfigurationUnlockIntents(body.UnlockIntents)
	if err != nil {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}
	evidence, err := service.UpdateUnstartedSeries(r.Context(), inbound.AdminUpdateUnstartedSeriesCommand{
		Operator:                   operator,
		TournamentID:               tournamentID,
		CommandID:                  params.IdempotencyKey,
		SeriesID:                   seriesID,
		ExpectedProjectionRevision: body.ExpectedProjectionRevision,
		ExpectedSeriesRevision:     body.ExpectedSeriesRevision,
		Confirmed:                  bool(body.Confirmed),
		Reason:                     body.Reason,
		CategoryMode:               domain.CategoryMode(body.Mode),
		Categories:                 categories,
		UnlockIntents:              unlockIntents,
	})
	if err != nil {
		writeTournamentAdminError(w, r, err)
		return
	}
	payload, err := tournamentConfigurationMutationEvidenceResponse(evidence)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, payload)
}

func (c *tournamentController) ReplaceTournamentSwissRoundConfiguration(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
	roundNumber int32,
	params api.ReplaceTournamentSwissRoundConfigurationParams,
) {
	operator, service, ok := c.requireConfigurationService(w, r)
	if !ok {
		return
	}
	var body api.ReplaceTournamentSwissRoundConfigurationRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	if roundNumber < 1 || roundNumber > 4 ||
		!validConfigurationMutation(body.ExpectedProjectionRevision, body.ExpectedRoundRevision, bool(body.Confirmed), body.Reason) {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}
	if body.ManualByeParticipantId != nil && *body.ManualByeParticipantId == uuid.Nil {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}
	categories, err := tournamentConfigurationCategories(body.Categories)
	if err != nil {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}
	manualPairings, err := tournamentConfigurationParticipantPairs(body.ManualPairings)
	if err != nil {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}
	unlockIntents, err := tournamentConfigurationUnlockIntents(body.UnlockIntents)
	if err != nil {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}
	evidence, err := service.ReviseSwissRound(r.Context(), inbound.AdminReviseSwissRoundCommand{
		Operator:                   operator,
		TournamentID:               tournamentID,
		CommandID:                  params.IdempotencyKey,
		RoundNumber:                int(roundNumber),
		ExpectedProjectionRevision: body.ExpectedProjectionRevision,
		ExpectedRoundRevision:      body.ExpectedRoundRevision,
		Confirmed:                  bool(body.Confirmed),
		Reason:                     body.Reason,
		CategoryMode:               domain.CategoryMode(body.Mode),
		Categories:                 categories,
		ManualPairings:             manualPairings,
		ManualByeParticipantID:     cloneUUIDPointer(body.ManualByeParticipantId),
		UnlockIntents:              unlockIntents,
	})
	if err != nil {
		writeTournamentAdminError(w, r, err)
		return
	}
	payload, err := tournamentConfigurationMutationEvidenceResponse(evidence)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, payload)
}

func validConfigurationMutation(expectedProjection, expectedEntity int64, confirmed bool, reason string) bool {
	return expectedProjection >= 1 && expectedEntity >= 1 && confirmed && strings.TrimSpace(reason) != ""
}

func tournamentConfigurationStageDefaultInput(
	value api.TournamentConfigurationStageDefaultInput,
) (inbound.AdminConfigurationStageDefault, error) {
	categories, err := tournamentConfigurationCategories(value.Categories)
	if err != nil {
		return inbound.AdminConfigurationStageDefault{}, err
	}
	mode := domain.CategoryMode(value.Mode)
	if !mode.IsValid() {
		return inbound.AdminConfigurationStageDefault{}, domain.ErrValidation
	}
	return inbound.AdminConfigurationStageDefault{Mode: mode, Categories: categories}, nil
}

func tournamentConfigurationCategories(values []api.Category) ([]domain.Category, error) {
	if len(values) < 1 || len(values) > 5 {
		return nil, domain.ErrValidation
	}
	result := make([]domain.Category, len(values))
	seen := make(map[domain.Category]struct{}, len(values))
	for index, value := range values {
		category := domain.Category(value)
		if !category.IsValid() {
			return nil, domain.ErrValidation
		}
		if _, exists := seen[category]; exists {
			return nil, domain.ErrValidation
		}
		seen[category] = struct{}{}
		result[index] = category
	}
	return result, nil
}

func tournamentConfigurationParticipantPairs(
	values *[]api.ConfigurationParticipantPair,
) ([]inbound.AdminConfigurationParticipantPair, error) {
	if values == nil {
		return nil, nil
	}
	if len(*values) < 2 || len(*values) > 8 {
		return nil, domain.ErrValidation
	}
	result := make([]inbound.AdminConfigurationParticipantPair, len(*values))
	for index, value := range *values {
		if value.FirstParticipantId == uuid.Nil || value.SecondParticipantId == uuid.Nil ||
			value.FirstParticipantId == value.SecondParticipantId {
			return nil, domain.ErrValidation
		}
		result[index] = inbound.AdminConfigurationParticipantPair{
			FirstParticipantID:  value.FirstParticipantId,
			SecondParticipantID: value.SecondParticipantId,
		}
	}
	return result, nil
}
