package pause

import (
	"encoding/json"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

type normalPauseDraftEvidenceDocument struct {
	Recovery   *draftusecase.RecoveryEvidence   `json:"recovery,omitempty"`
	Transition *draftusecase.TransitionEvidence `json:"transition,omitempty"`
}

func participantDraftEvidenceMap(
	recovery *draftusecase.RecoveryEvidence,
	transition *draftusecase.TransitionEvidence,
) (map[string]any, error) {
	if recovery == nil && transition == nil {
		return nil, nil
	}
	payload, err := json.Marshal(normalPauseDraftEvidenceDocument{
		Recovery: recovery, Transition: transition,
	})
	if err != nil {
		return nil, domain.ErrValidation
	}
	var document map[string]any
	if err := json.Unmarshal(payload, &document); err != nil {
		return nil, domain.ErrValidation
	}
	return document, nil
}
