package snapshot

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	draftpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

func participantDraftExecution(
	aggregate *draftpostgres.DraftAggregate,
	targetRevision int64,
) (*draftusecase.Execution, error) {
	if aggregate == nil || targetRevision < 1 || len(aggregate.Revisions) == 0 {
		return nil, domain.ErrInternal
	}
	revision, firstDecision := participantDraftRevisionAt(aggregate.Revisions, targetRevision)
	if revision == nil || firstDecision == nil {
		return nil, domain.ErrInternal
	}

	actions := participantDraftActionsAt(aggregate, targetRevision)
	recovery, transition, err := participantDraftEvidence(revision.RecoveryEvidence)
	if err != nil {
		return nil, err
	}
	execution := participantDraftExecutionFromState(
		aggregate,
		revision,
		firstDecision,
		actions,
		recovery,
		transition,
	)
	if err := execution.Validate(); err != nil {
		return nil, fmt.Errorf("ParticipantDraftRepository - invalid execution: %w", err)
	}
	cloned := draftusecase.CloneExecution(execution)
	return &cloned, nil
}

func participantDraftExecutionFromState(
	aggregate *draftpostgres.DraftAggregate,
	revision *draftpostgres.DraftRevisionRecord,
	firstDecision *domain.DecisionEvidence,
	actions []draftusecase.ActionRecord,
	recovery *draftusecase.RecoveryEvidence,
	transition *draftusecase.TransitionEvidence,
) draftusecase.Execution {
	state := participantDraftExecutionState(revision.State)
	execution := draftusecase.Execution{
		ID: aggregate.Draft.ID, SeriesID: aggregate.Draft.SeriesID, Format: aggregate.Draft.Format,
		FirstParticipantID:  aggregate.Draft.FirstParticipantID,
		SecondParticipantID: aggregate.Draft.SecondParticipantID,
		Pool:                append([]domain.Category(nil), aggregate.Pool...),
		State:               state,
		RevisionID:          revision.ID,
		Revision:            revision.Revision,
		CommandID:           revision.CommandID,
		ServiceEpoch:        revision.ServiceEpoch,
		Turn:                revision.TurnNumber,
		CurrentActorID:      cloneParticipantUUIDPointer(revision.CurrentActorID),
		CurrentAction:       cloneDraftActionPointer(revision.CurrentAction),
		AbsoluteDeadline:    cloneParticipantTimePointer(revision.AbsoluteDeadline),
		Recovery:            recovery,
		Transition:          transition,
		Actions:             actions,
		SelectedCategories:  append([]domain.Category(nil), revision.SelectedCategories...),
		FirstActorDecision:  *cloneDecisionEvidence(firstDecision),
	}
	if revision.PreviousRevisionID != nil {
		execution.PreviousRevisionID = *revision.PreviousRevisionID
	}
	if revision.PausedRemainingMS != nil {
		execution.PausedRemaining = time.Duration(*revision.PausedRemainingMS) * time.Millisecond
	}
	switch state {
	case draftusecase.ExecutionStateActive:
		if execution.AbsoluteDeadline != nil {
			execution.TurnDeadline = execution.AbsoluteDeadline.UTC()
		}
	case draftusecase.ExecutionStatePaused, draftusecase.ExecutionStateRecoveryRequired:
		if recovery != nil {
			execution.TurnDeadline = recovery.PreviousDeadline.UTC()
		}
	case draftusecase.ExecutionStateCompleted, draftusecase.ExecutionStateSuperseded:
	}
	if state == draftusecase.ExecutionStateActive || state == draftusecase.ExecutionStatePaused ||
		state == draftusecase.ExecutionStateRecoveryRequired {
		execution.LegalCategories = participantDraftLegalCategories(aggregate.Pool, actions)
	}
	return execution
}

func participantDraftRevisionAt(
	revisions []draftpostgres.DraftRevisionRecord,
	targetRevision int64,
) (*draftpostgres.DraftRevisionRecord, *domain.DecisionEvidence) {
	var target *draftpostgres.DraftRevisionRecord
	var firstDecision *domain.DecisionEvidence
	for index := range revisions {
		candidate := &revisions[index]
		if candidate.Revision == 1 {
			firstDecision = candidate.DecisionEvidence
		}
		if candidate.Revision == targetRevision {
			target = candidate
			break
		}
	}
	return target, firstDecision
}

func participantDraftActionsAt(
	aggregate *draftpostgres.DraftAggregate,
	targetRevision int64,
) []draftusecase.ActionRecord {
	actions := make([]draftusecase.ActionRecord, 0, len(aggregate.Actions))
	for _, action := range aggregate.Actions {
		resultRevision, found := participantDraftRevisionByID(aggregate.Revisions, action.ResultRevisionID)
		if !found || resultRevision.Revision > targetRevision {
			continue
		}
		actions = append(actions, draftusecase.ActionRecord{
			ID: action.ID, ResultRevisionID: action.ResultRevisionID, CommandID: action.CommandID,
			Turn: action.TurnNumber, ActorID: action.ActorID, Action: action.Action, Category: action.Category,
			ScheduledDeadline: action.ScheduledDeadline.UTC(), OccurredAt: action.OccurredAt.UTC(),
			Automatic: action.Automatic, DecisionEvidence: cloneDecisionEvidence(resultRevision.DecisionEvidence),
		})
	}
	return actions
}

type participantDraftEvidenceDocument struct {
	Recovery   *draftusecase.RecoveryEvidence   `json:"recovery,omitempty"`
	Transition *draftusecase.TransitionEvidence `json:"transition,omitempty"`
}

func participantDraftEvidence(
	document map[string]any,
) (*draftusecase.RecoveryEvidence, *draftusecase.TransitionEvidence, error) {
	if len(document) == 0 {
		return nil, nil, nil
	}
	payload, err := json.Marshal(document)
	if err != nil {
		return nil, nil, domain.ErrInternal
	}
	var evidence participantDraftEvidenceDocument
	if err := json.Unmarshal(payload, &evidence); err != nil { //nolint:musttag // Versioned draft evidence has explicit JSON tags on every persisted field.
		return nil, nil, domain.ErrInternal
	}
	return evidence.Recovery, evidence.Transition, nil
}

func participantDraftRevisionByID(
	revisions []draftpostgres.DraftRevisionRecord,
	revisionID uuid.UUID,
) (draftpostgres.DraftRevisionRecord, bool) {
	for _, revision := range revisions {
		if revision.ID == revisionID {
			return revision, true
		}
	}
	return draftpostgres.DraftRevisionRecord{}, false
}

func participantDraftLegalCategories(
	pool []domain.Category,
	actions []draftusecase.ActionRecord,
) []domain.Category {
	used := make(map[domain.Category]struct{}, len(actions))
	for _, action := range actions {
		used[action.Category] = struct{}{}
	}
	result := make([]domain.Category, 0, len(pool)-len(used))
	for _, category := range pool {
		if _, exists := used[category]; !exists {
			result = append(result, category)
		}
	}
	return result
}

func participantDraftExecutionState(state draftpostgres.DraftPersistenceState) draftusecase.ExecutionState {
	return draftusecase.ExecutionState(state)
}

func cloneDecisionEvidence(value *domain.DecisionEvidence) *domain.DecisionEvidence {
	if value == nil {
		return nil
	}
	cloned := *value
	cloned.NormalizedInputs = append([]string(nil), value.NormalizedInputs...)
	cloned.Result = append([]string(nil), value.Result...)
	return &cloned
}

func cloneDraftActionPointer(value *domain.DraftActionType) *domain.DraftActionType {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneParticipantUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneParticipantTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC()
	return &cloned
}
