package v1

import (
	"encoding/hex"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

//nolint:gocyclo // The strict response mapper validates every nested configuration collection.
func tournamentConfigurationResponse(
	view inbound.AdminTournamentConfigurationView,
) (api.TournamentConfiguration, error) {
	if view.TournamentID == uuid.Nil || view.ProjectionRevisionID == uuid.Nil ||
		view.ProjectionRevision < 1 || view.ConfigurationRevision < 1 ||
		!domain.IsValidServerTime(view.UpdatedAt) {
		return api.TournamentConfiguration{}, domain.ErrInternal
	}
	pools := make([]api.TournamentConfigurationCategoryPool, len(view.CategoryPools))
	for index, pool := range view.CategoryPools {
		mapped, err := tournamentConfigurationCategoryPoolResponse(pool)
		if err != nil {
			return api.TournamentConfiguration{}, err
		}
		pools[index] = mapped
	}
	if len(pools) != 2 {
		return api.TournamentConfiguration{}, domain.ErrInternal
	}
	swiss, err := tournamentConfigurationStageDefaultResponse(view.SwissDefault)
	if err != nil {
		return api.TournamentConfiguration{}, err
	}
	golden, err := tournamentConfigurationStageDefaultResponse(view.GoldenDefault)
	if err != nil {
		return api.TournamentConfiguration{}, err
	}
	semifinal, err := tournamentConfigurationStageDefaultResponse(view.SemifinalDefault)
	if err != nil {
		return api.TournamentConfiguration{}, err
	}
	final, err := tournamentConfigurationFinalDefaultResponse(view.FinalDefault)
	if err != nil {
		return api.TournamentConfiguration{}, domain.ErrInternal
	}
	series := make([]api.TournamentConfigurationSeries, len(view.Series))
	for index, item := range view.Series {
		mapped, mapErr := tournamentConfigurationSeriesResponse(item)
		if mapErr != nil {
			return api.TournamentConfiguration{}, mapErr
		}
		series[index] = mapped
	}
	rounds := make([]api.TournamentConfigurationRound, len(view.Rounds))
	for index, item := range view.Rounds {
		mapped, mapErr := tournamentConfigurationRoundResponse(item)
		if mapErr != nil {
			return api.TournamentConfiguration{}, mapErr
		}
		rounds[index] = mapped
	}
	return api.TournamentConfiguration{
		TournamentId:          view.TournamentID,
		ProjectionRevisionId:  view.ProjectionRevisionID,
		ProjectionRevision:    view.ProjectionRevision,
		ConfigurationRevision: view.ConfigurationRevision,
		CategoryPools:         pools,
		SwissDefault:          swiss,
		GoldenDefault:         golden,
		SemifinalDefault:      semifinal,
		FinalDefault:          final,
		Series:                series,
		Rounds:                rounds,
		UpdatedAt:             view.UpdatedAt,
	}, nil
}

func tournamentConfigurationCategoryPoolResponse(
	view inbound.AdminConfigurationCategoryPoolView,
) (api.TournamentConfigurationCategoryPool, error) {
	if view.ID == uuid.Nil || view.Revision < 1 || !view.Format.IsValid() {
		return api.TournamentConfigurationCategoryPool{}, domain.ErrInternal
	}
	categories, err := tournamentConfigurationCategoryResponse(view.Categories)
	if err != nil {
		return api.TournamentConfigurationCategoryPool{}, err
	}
	expected := view.Format.WinsRequired()*2 + 1
	if len(categories) != expected {
		return api.TournamentConfigurationCategoryPool{}, domain.ErrInternal
	}
	return api.TournamentConfigurationCategoryPool{
		Id: view.ID, Revision: view.Revision, Format: api.SeriesFormat(view.Format), Categories: categories,
	}, nil
}

func tournamentConfigurationStageDefaultResponse(
	view inbound.AdminConfigurationStageDefault,
) (api.TournamentConfigurationStageDefault, error) {
	if !view.Mode.IsValid() {
		return api.TournamentConfigurationStageDefault{}, domain.ErrInternal
	}
	categories, err := tournamentConfigurationCategoryResponse(view.Categories)
	if err != nil {
		return api.TournamentConfigurationStageDefault{}, err
	}
	return api.TournamentConfigurationStageDefault{
		Mode: api.CategoryMode(view.Mode), Categories: categories,
	}, nil
}

func tournamentConfigurationFinalDefaultResponse(
	view inbound.AdminConfigurationStageDefault,
) (api.TournamentConfigurationFinalDefault, error) {
	if view.Mode != domain.CategoryModeDraft {
		return api.TournamentConfigurationFinalDefault{}, domain.ErrInternal
	}
	categories, err := tournamentConfigurationCategoryResponse(view.Categories)
	if err != nil || len(categories) != 5 {
		return api.TournamentConfigurationFinalDefault{}, domain.ErrInternal
	}
	return api.TournamentConfigurationFinalDefault{
		Mode: api.TournamentConfigurationFinalDefaultModeDraft, Categories: categories,
	}, nil
}

func tournamentConfigurationSeriesResponse(
	view inbound.AdminConfigurationSeriesView,
) (api.TournamentConfigurationSeries, error) {
	if view.ID == uuid.Nil || !view.Stage.IsValid() || view.RoundNumber < 0 || view.RoundNumber > 4 ||
		view.Revision < 1 || view.CategoryPoolRevisionID == uuid.Nil || view.CategoryPoolRevision < 1 {
		return api.TournamentConfigurationSeries{}, domain.ErrInternal
	}
	categories, err := tournamentConfigurationCategoryResponse(view.Categories)
	if err != nil || !view.Mode.IsValid() {
		return api.TournamentConfigurationSeries{}, domain.ErrInternal
	}
	unlockIntents, err := tournamentConfigurationUnlockIntentResponses(view.UnlockIntents)
	if err != nil {
		return api.TournamentConfigurationSeries{}, err
	}
	return api.TournamentConfigurationSeries{
		Id:                     view.ID,
		Stage:                  api.TournamentConfigurationSeriesStage(view.Stage),
		RoundNumber:            int32(view.RoundNumber),
		Revision:               view.Revision,
		Mode:                   api.CategoryMode(view.Mode),
		Categories:             categories,
		CategoryPoolRevisionId: view.CategoryPoolRevisionID,
		CategoryPoolRevision:   view.CategoryPoolRevision,
		Locked:                 view.Locked,
		Started:                view.Started,
		Consumed:               view.Consumed,
		Disclosed:              view.Disclosed,
		UnlockIntents:          unlockIntents,
	}, nil
}

func tournamentConfigurationRoundResponse(
	view inbound.AdminConfigurationRoundView,
) (api.TournamentConfigurationRound, error) {
	if view.ID == uuid.Nil || view.RoundNumber < 1 || view.RoundNumber > 4 || view.Revision < 1 || !view.Mode.IsValid() {
		return api.TournamentConfigurationRound{}, domain.ErrInternal
	}
	categories, err := tournamentConfigurationCategoryResponse(view.Categories)
	if err != nil {
		return api.TournamentConfigurationRound{}, err
	}
	pairings := make([]api.ConfigurationParticipantPair, len(view.Pairings))
	for index, pair := range view.Pairings {
		if pair.FirstParticipantID == uuid.Nil || pair.SecondParticipantID == uuid.Nil || pair.FirstParticipantID == pair.SecondParticipantID {
			return api.TournamentConfigurationRound{}, domain.ErrInternal
		}
		pairings[index] = api.ConfigurationParticipantPair{FirstParticipantId: pair.FirstParticipantID, SecondParticipantId: pair.SecondParticipantID}
	}
	unlockIntents, err := tournamentConfigurationUnlockIntentResponses(view.UnlockIntents)
	if err != nil {
		return api.TournamentConfigurationRound{}, err
	}
	return api.TournamentConfigurationRound{
		Id: view.ID, RoundNumber: int32(view.RoundNumber), Revision: view.Revision,
		Mode: api.CategoryMode(view.Mode), Categories: categories, Pairings: pairings,
		ByeParticipantId: cloneUUIDPointer(view.ByeParticipantID), Locked: view.Locked, Started: view.Started,
		Consumed: view.Consumed, Disclosed: view.Disclosed, UnlockIntents: unlockIntents,
	}, nil
}

func tournamentConfigurationUnlockIntentResponses(
	values []inbound.AdminConfigurationUnlockIntent,
) ([]api.ConfigurationUnlockIntent, error) {
	result := make([]api.ConfigurationUnlockIntent, len(values))
	for index, intent := range values {
		mapped, err := tournamentConfigurationUnlockIntentResponse(intent)
		if err != nil {
			return nil, err
		}
		result[index] = mapped
	}
	return result, nil
}

func tournamentConfigurationCategoryResponse(values []domain.Category) ([]api.Category, error) {
	if len(values) < 1 || len(values) > 5 {
		return nil, domain.ErrInternal
	}
	result := make([]api.Category, len(values))
	seen := make(map[domain.Category]struct{}, len(values))
	for index, value := range values {
		if !value.IsValid() {
			return nil, domain.ErrInternal
		}
		if _, exists := seen[value]; exists {
			return nil, domain.ErrInternal
		}
		seen[value] = struct{}{}
		result[index] = api.Category(value)
	}
	return result, nil
}

func tournamentConfigurationMutationEvidenceResponse(
	evidence inbound.AdminConfigurationMutationEvidence,
) (api.TournamentConfigurationMutationEvidence, error) {
	if evidence.CommandID == uuid.Nil || evidence.TournamentID == uuid.Nil || evidence.OperatorID == uuid.Nil ||
		evidence.Reason == "" || !domain.IsValidServerTime(evidence.RequestedAt) ||
		evidence.PreviousConfigurationRevision < 1 || evidence.NextConfigurationRevision < 1 {
		return api.TournamentConfigurationMutationEvidence{}, domain.ErrInternal
	}
	artifacts := make([]api.ConfigurationArtifact, len(evidence.AffectedArtifacts))
	for index, artifact := range evidence.AffectedArtifacts {
		mapped, err := tournamentConfigurationArtifactResponse(artifact)
		if err != nil {
			return api.TournamentConfigurationMutationEvidence{}, err
		}
		artifacts[index] = mapped
	}
	unlockIntents := make([]api.ConfigurationUnlockIntent, len(evidence.UnlockIntents))
	for index, intent := range evidence.UnlockIntents {
		mapped, err := tournamentConfigurationUnlockIntentResponse(intent)
		if err != nil {
			return api.TournamentConfigurationMutationEvidence{}, err
		}
		unlockIntents[index] = mapped
	}
	return api.TournamentConfigurationMutationEvidence{
		CommandId:                     evidence.CommandID,
		TournamentId:                  evidence.TournamentID,
		OperatorId:                    evidence.OperatorID,
		Reason:                        evidence.Reason,
		RequestedAt:                   evidence.RequestedAt,
		ValidationDigest:              hex.EncodeToString(evidence.ValidationDigest[:]),
		PreviousConfigurationRevision: evidence.PreviousConfigurationRevision,
		NextConfigurationRevision:     evidence.NextConfigurationRevision,
		AffectedArtifactIds:           append([]uuid.UUID{}, evidence.AffectedArtifactIDs...),
		SupersededArtifactIds:         append([]uuid.UUID{}, evidence.SupersededArtifactIDs...),
		RebuiltArtifactIds:            append([]uuid.UUID{}, evidence.RebuiltArtifactIDs...),
		AffectedArtifacts:             artifacts,
		UnlockIntents:                 unlockIntents,
	}, nil
}

func tournamentConfigurationArtifactResponse(
	artifact inbound.AdminConfigurationArtifactView,
) (api.ConfigurationArtifact, error) {
	if artifact.Kind == "" || artifact.ID == uuid.Nil || !artifact.Stage.IsValid() ||
		artifact.PreviousRevisionID == uuid.Nil || artifact.SuccessorRevisionID == uuid.Nil {
		return api.ConfigurationArtifact{}, domain.ErrInternal
	}
	return api.ConfigurationArtifact{
		Kind: artifact.Kind, Id: artifact.ID,
		Stage:              api.ConfigurationArtifactStage(artifact.Stage),
		PreviousRevisionId: artifact.PreviousRevisionID, SuccessorRevisionId: artifact.SuccessorRevisionID,
	}, nil
}

func tournamentConfigurationUnlockIntentResponse(
	intent inbound.AdminConfigurationUnlockIntent,
) (api.ConfigurationUnlockIntent, error) {
	if intent.ReservationID == uuid.Nil || intent.OwnerID == uuid.Nil || intent.SourceRevisionID == uuid.Nil ||
		intent.ExpectedRevision < 1 {
		return api.ConfigurationUnlockIntent{}, domain.ErrInternal
	}
	return api.ConfigurationUnlockIntent{
		ReservationId: intent.ReservationID, OwnerId: intent.OwnerID, SourceRevisionId: intent.SourceRevisionID,
		ExpectedRevision: intent.ExpectedRevision, ExpectedUsed: intent.ExpectedUsed, ExpectedDisclosed: intent.ExpectedDisclosed,
		EvidenceDigest: hex.EncodeToString(intent.EvidenceDigest[:]), BindingDigest: hex.EncodeToString(intent.BindingDigest[:]),
	}, nil
}

func tournamentConfigurationUnlockIntents(
	values []api.ConfigurationUnlockIntent,
) ([]inbound.AdminConfigurationUnlockIntent, error) {
	result := make([]inbound.AdminConfigurationUnlockIntent, len(values))
	for index, value := range values {
		evidenceDigest, err := parseSHA256Hex(value.EvidenceDigest)
		if err != nil {
			return nil, domain.ErrValidation
		}
		bindingDigest, err := parseSHA256Hex(value.BindingDigest)
		if err != nil {
			return nil, domain.ErrValidation
		}
		if value.ReservationId == uuid.Nil || value.OwnerId == uuid.Nil || value.SourceRevisionId == uuid.Nil ||
			value.ExpectedRevision < 1 {
			return nil, domain.ErrValidation
		}
		result[index] = inbound.AdminConfigurationUnlockIntent{
			ReservationID: value.ReservationId, OwnerID: value.OwnerId, SourceRevisionID: value.SourceRevisionId,
			ExpectedRevision: value.ExpectedRevision, ExpectedUsed: value.ExpectedUsed, ExpectedDisclosed: value.ExpectedDisclosed,
			EvidenceDigest: evidenceDigest, BindingDigest: bindingDigest,
		}
	}
	return result, nil
}
