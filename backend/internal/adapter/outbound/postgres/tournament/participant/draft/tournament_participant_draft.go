package draft

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	draftrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

type DraftPersistenceState = draftrepo.DraftPersistenceState
type DraftRevisionExpectation = draftrepo.DraftRevisionExpectation
type DraftRevisionInput = draftrepo.DraftRevisionInput
type DraftActionInput = draftrepo.DraftActionInput
type DraftAggregate = draftrepo.DraftAggregate
type DraftRevisionRecord = draftrepo.DraftRevisionRecord

var ErrDraftNotFound = draftrepo.ErrDraftNotFound

// ContentLoader supplies the published configuration required to validate
// Swiss draft completion without coupling this child to the root adapter.
type ContentLoader func(context.Context, *sqlc.Queries, uuid.UUID) (domain.ContentConfiguration, error)

// DraftRepository is the participant adapter's narrow persistence boundary.
// The participant workflow only needs aggregate reads and revision appends;
// the assignment/draft adapter remains free to expose its broader API.
type DraftRepository interface {
	AppendRevision(
		ctx context.Context,
		draftID uuid.UUID,
		expectation DraftRevisionExpectation,
		input DraftRevisionInput,
	) (*DraftRevisionRecord, bool, error)
	Get(ctx context.Context, draftID uuid.UUID) (*DraftAggregate, error)
}

var _ DraftRepository = (*draftrepo.DraftPostgres)(nil)

type ParticipantDraftRepository struct {
	tx            *db.TxManager
	drafts        DraftRepository
	contentLoader ContentLoader
}

func NewParticipantDraftRepository(tx *db.TxManager, drafts *draftrepo.DraftPostgres) *ParticipantDraftRepository {
	var repository DraftRepository
	if drafts != nil {
		repository = drafts
	}
	return NewParticipantDraftRepositoryWithRepository(tx, repository, nil)
}

func NewParticipantDraftRepositoryWithDependencies(
	tx *db.TxManager,
	drafts *draftrepo.DraftPostgres,
	contentLoader ContentLoader,
) *ParticipantDraftRepository {
	var repository DraftRepository
	if drafts != nil {
		repository = drafts
	}
	return NewParticipantDraftRepositoryWithRepository(tx, repository, contentLoader)
}

func NewParticipantDraftRepositoryWithRepository(
	tx *db.TxManager,
	drafts DraftRepository,
	contentLoader ContentLoader,
) *ParticipantDraftRepository {
	return &ParticipantDraftRepository{tx: tx, drafts: drafts, contentLoader: contentLoader}
}

func (r *ParticipantDraftRepository) LoadDraft(
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
	return participantDraftExecution(aggregate, aggregate.Revisions[len(aggregate.Revisions)-1].Revision)
}

func (r *ParticipantDraftRepository) FindDraftCommand(
	ctx context.Context,
	draftID uuid.UUID,
	commandID uuid.UUID,
) (*draftusecase.Execution, error) {
	if commandID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	aggregate, err := r.loadAggregate(ctx, draftID)
	if err != nil {
		return nil, err
	}
	for _, revision := range aggregate.Revisions {
		if revision.CommandID == commandID {
			return participantDraftExecution(aggregate, revision.Revision)
		}
	}
	return nil, nil
}

func (r *ParticipantDraftRepository) CommitDraftRevisions(
	ctx context.Context,
	expected draftusecase.RevisionExpectation,
	revisions []draftusecase.Execution,
) (*draftusecase.Execution, bool, error) {
	if !validParticipantDraftCommit(ctx, r, expected, revisions) {
		return nil, false, domain.ErrValidation
	}

	var committed *draftusecase.Execution
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		committed, err = r.appendParticipantDraftRevisions(txCtx, expected, revisions)
		return err
	})
	if err != nil {
		return nil, false, err
	}
	return committed, true, nil
}

func validParticipantDraftCommit(
	ctx context.Context,
	repository *ParticipantDraftRepository,
	expected draftusecase.RevisionExpectation,
	revisions []draftusecase.Execution,
) bool {
	return ctx != nil && repository != nil && repository.tx != nil && repository.drafts != nil &&
		expected.RevisionID != uuid.Nil && expected.Revision >= 1 &&
		expected.ServiceEpoch != uuid.Nil && len(revisions) > 0
}

func (r *ParticipantDraftRepository) appendParticipantDraftRevisions(
	ctx context.Context,
	expected draftusecase.RevisionExpectation,
	revisions []draftusecase.Execution,
) (*draftusecase.Execution, error) {
	current := DraftRevisionExpectation{
		ID: expected.RevisionID, Revision: expected.Revision, ServiceEpoch: expected.ServiceEpoch,
	}
	for _, revision := range revisions {
		input, err := participantDraftRevisionInput(revision)
		if err != nil {
			return nil, err
		}
		row, changed, err := r.drafts.AppendRevision(ctx, revision.ID, current, input)
		if err != nil {
			return nil, err
		}
		if !changed || row == nil {
			return nil, domain.ErrConflict
		}
		current = DraftRevisionExpectation{
			ID: row.ID, Revision: row.Revision, ServiceEpoch: row.ServiceEpoch,
		}
	}
	execution, err := r.LoadDraft(ctx, revisions[len(revisions)-1].ID)
	if err != nil {
		return nil, err
	}
	if err := r.completeSwissDraft(ctx, *execution); err != nil {
		return nil, err
	}
	return execution, nil
}

func (r *ParticipantDraftRepository) loadAggregate(
	ctx context.Context,
	draftID uuid.UUID,
) (*DraftAggregate, error) {
	if ctx == nil || r == nil || r.drafts == nil || draftID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	aggregate, err := r.drafts.Get(ctx, draftID)
	if errors.Is(err, ErrDraftNotFound) {
		return nil, draftusecase.ErrNotFound
	}
	return aggregate, err
}

func participantDraftExecution(
	aggregate *DraftAggregate,
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
	aggregate *DraftAggregate,
	revision *DraftRevisionRecord,
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
	revisions []DraftRevisionRecord,
	targetRevision int64,
) (*DraftRevisionRecord, *domain.DecisionEvidence) {
	var target *DraftRevisionRecord
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
	aggregate *DraftAggregate,
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

func participantDraftRevisionInput(execution draftusecase.Execution) (DraftRevisionInput, error) {
	if execution.Validate() != nil || len(execution.Actions) == 0 {
		return DraftRevisionInput{}, domain.ErrValidation
	}
	lastAction := execution.Actions[len(execution.Actions)-1]
	if lastAction.ResultRevisionID != execution.RevisionID || lastAction.CommandID != execution.CommandID {
		return DraftRevisionInput{}, domain.ErrValidation
	}
	evidence, err := participantDraftEvidenceMap(execution.Recovery, execution.Transition)
	if err != nil {
		return DraftRevisionInput{}, err
	}
	input := DraftRevisionInput{
		ID: execution.RevisionID, CommandID: execution.CommandID, ServiceEpoch: execution.ServiceEpoch,
		State: participantDraftPersistenceState(execution.State), TurnNumber: execution.Turn,
		CurrentActorID:     cloneParticipantUUIDPointer(execution.CurrentActorID),
		CurrentAction:      cloneDraftActionPointer(execution.CurrentAction),
		AbsoluteDeadline:   cloneParticipantTimePointer(execution.AbsoluteDeadline),
		RecoveryEvidence:   evidence,
		SelectedCategories: append([]domain.Category(nil), execution.SelectedCategories...),
		Action: &DraftActionInput{
			ID: lastAction.ID, TurnNumber: lastAction.Turn, ActorID: lastAction.ActorID,
			Action: lastAction.Action, Category: lastAction.Category,
			ScheduledDeadline: lastAction.ScheduledDeadline.UTC(), OccurredAt: lastAction.OccurredAt.UTC(),
			Automatic: lastAction.Automatic,
		},
		CreatedAt: lastAction.OccurredAt.UTC(),
	}
	if execution.PausedRemaining > 0 {
		milliseconds := int(execution.PausedRemaining / time.Millisecond)
		input.PausedRemainingMS = &milliseconds
	}
	if execution.Recovery != nil {
		reason := string(execution.Recovery.Reason)
		input.RecoveryReason = &reason
	}
	if lastAction.Automatic {
		input.DecisionEvidence = cloneDecisionEvidence(lastAction.DecisionEvidence)
	}
	return input, nil
}

type participantDraftEvidenceDocument struct {
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
	payload, err := json.Marshal(participantDraftEvidenceDocument{ //nolint:musttag // Persisted nested evidence has a versioned shape.
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
	if err := json.Unmarshal(payload, &evidence); err != nil { //nolint:musttag // See participantDraftEvidenceDocument.
		return nil, nil, domain.ErrInternal
	}
	return evidence.Recovery, evidence.Transition, nil
}

func participantDraftRevisionByID(
	revisions []DraftRevisionRecord,
	revisionID uuid.UUID,
) (DraftRevisionRecord, bool) {
	for _, revision := range revisions {
		if revision.ID == revisionID {
			return revision, true
		}
	}
	return DraftRevisionRecord{}, false
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

func participantDraftExecutionState(state DraftPersistenceState) draftusecase.ExecutionState {
	return draftusecase.ExecutionState(state)
}

func participantDraftPersistenceState(state draftusecase.ExecutionState) DraftPersistenceState {
	return DraftPersistenceState(state)
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

var _ draftusecase.Repository = (*ParticipantDraftRepository)(nil)
