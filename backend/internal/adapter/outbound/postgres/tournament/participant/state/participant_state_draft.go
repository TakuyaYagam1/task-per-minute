package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

// participantStateDraftReader keeps the participant state reader independent from
// the root postgres package while the participant draft writer remains there.
type participantStateDraftReader struct {
	drafts *draft.DraftPostgres
}

func newParticipantStateDraftReader(tx *db.TxManager) *participantStateDraftReader {
	return &participantStateDraftReader{drafts: draft.NewDraftPostgres(tx)}
}

func (r *participantStateDraftReader) LoadDraft(
	ctx context.Context,
	draftID uuid.UUID,
) (*draftusecase.Execution, error) {
	aggregate, err := r.loadAggregate(ctx, draftID)
	if err != nil {
		return nil, err
	}
	if len(aggregate.Revisions) == 0 {
		return nil, domain.ErrInternal
	}
	return participantStateDraftExecution(aggregate, aggregate.Revisions[len(aggregate.Revisions)-1].Revision)
}

func (r *participantStateDraftReader) loadAggregate(
	ctx context.Context,
	draftID uuid.UUID,
) (*draft.DraftAggregate, error) {
	if ctx == nil || r == nil || r.drafts == nil || draftID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	aggregate, err := r.drafts.Get(ctx, draftID)
	if errors.Is(err, draft.ErrDraftNotFound) {
		return nil, draftusecase.ErrNotFound
	}
	return aggregate, err
}

func participantStateDraftExecution(
	aggregate *draft.DraftAggregate,
	targetRevision int64,
) (*draftusecase.Execution, error) {
	if aggregate == nil || targetRevision < 1 || len(aggregate.Revisions) == 0 {
		return nil, domain.ErrInternal
	}
	revision, firstDecision := participantStateDraftRevisionAt(aggregate.Revisions, targetRevision)
	if revision == nil || firstDecision == nil {
		return nil, domain.ErrInternal
	}

	actions := participantStateDraftActionsAt(aggregate, targetRevision)
	recovery, transition, err := participantStateDraftEvidence(revision.RecoveryEvidence)
	if err != nil {
		return nil, err
	}
	execution := participantStateDraftExecutionFromState(
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

func participantStateDraftExecutionFromState(
	aggregate *draft.DraftAggregate,
	revision *draft.DraftRevisionRecord,
	firstDecision *domain.DecisionEvidence,
	actions []draftusecase.ActionRecord,
	recovery *draftusecase.RecoveryEvidence,
	transition *draftusecase.TransitionEvidence,
) draftusecase.Execution {
	state := participantStateDraftExecutionState(revision.State)
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
		CurrentActorID:      participantStateCloneUUIDPointer(revision.CurrentActorID),
		CurrentAction:       participantStateCloneDraftActionPointer(revision.CurrentAction),
		AbsoluteDeadline:    participantStateCloneTimePointer(revision.AbsoluteDeadline),
		Recovery:            recovery,
		Transition:          transition,
		Actions:             actions,
		SelectedCategories:  append([]domain.Category(nil), revision.SelectedCategories...),
		FirstActorDecision:  *participantStateCloneDecisionEvidence(firstDecision),
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
		execution.LegalCategories = participantStateDraftLegalCategories(aggregate.Pool, actions)
	}
	return execution
}

func participantStateDraftRevisionAt(
	revisions []draft.DraftRevisionRecord,
	targetRevision int64,
) (*draft.DraftRevisionRecord, *domain.DecisionEvidence) {
	var target *draft.DraftRevisionRecord
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

func participantStateDraftActionsAt(
	aggregate *draft.DraftAggregate,
	targetRevision int64,
) []draftusecase.ActionRecord {
	actions := make([]draftusecase.ActionRecord, 0, len(aggregate.Actions))
	for _, action := range aggregate.Actions {
		resultRevision, found := participantStateDraftRevisionByID(aggregate.Revisions, action.ResultRevisionID)
		if !found || resultRevision.Revision > targetRevision {
			continue
		}
		actions = append(actions, draftusecase.ActionRecord{
			ID: action.ID, ResultRevisionID: action.ResultRevisionID, CommandID: action.CommandID,
			Turn: action.TurnNumber, ActorID: action.ActorID, Action: action.Action, Category: action.Category,
			ScheduledDeadline: action.ScheduledDeadline.UTC(), OccurredAt: action.OccurredAt.UTC(),
			Automatic: action.Automatic, DecisionEvidence: participantStateCloneDecisionEvidence(resultRevision.DecisionEvidence),
		})
	}
	return actions
}

type participantStateDraftEvidenceDocument struct {
	Recovery   *draftusecase.RecoveryEvidence   `json:"recovery,omitempty"`
	Transition *draftusecase.TransitionEvidence `json:"transition,omitempty"`
}

func participantStateDraftEvidence(
	document map[string]any,
) (*draftusecase.RecoveryEvidence, *draftusecase.TransitionEvidence, error) {
	if len(document) == 0 {
		return nil, nil, nil
	}
	payload, err := json.Marshal(document)
	if err != nil {
		return nil, nil, domain.ErrInternal
	}
	var evidence participantStateDraftEvidenceDocument
	if err := json.Unmarshal(payload, &evidence); err != nil { //nolint:musttag // See participantStateDraftEvidenceDocument.
		return nil, nil, domain.ErrInternal
	}
	return evidence.Recovery, evidence.Transition, nil
}

func participantStateDraftRevisionByID(
	revisions []draft.DraftRevisionRecord,
	revisionID uuid.UUID,
) (draft.DraftRevisionRecord, bool) {
	for _, revision := range revisions {
		if revision.ID == revisionID {
			return revision, true
		}
	}
	return draft.DraftRevisionRecord{}, false
}

func participantStateDraftLegalCategories(
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

func participantStateDraftExecutionState(state draft.DraftPersistenceState) draftusecase.ExecutionState {
	return draftusecase.ExecutionState(state)
}

func participantStateCloneDecisionEvidence(value *domain.DecisionEvidence) *domain.DecisionEvidence {
	if value == nil {
		return nil
	}
	cloned := *value
	cloned.NormalizedInputs = append([]string(nil), value.NormalizedInputs...)
	cloned.Result = append([]string(nil), value.Result...)
	return &cloned
}

func participantStateCloneDraftActionPointer(value *domain.DraftActionType) *domain.DraftActionType {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func participantStateCloneUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func participantStateCloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC()
	return &cloned
}
