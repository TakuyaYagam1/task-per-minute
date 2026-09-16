package exactdraft

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	assignmentadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
)

func taskHintsToDomain(hint1, hint2, hint3 *string) []string {
	hints, _ := domain.NormalizeTaskHints([]string{
		stringValue(hint1),
		stringValue(hint2),
		stringValue(hint3),
	})
	return hints
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func nullableUUID(value *uuid.UUID) uuid.NullUUID {
	if value == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *value, Valid: true}
}

func nullableUUIDValue(value uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: value, Valid: value != uuid.Nil}
}

func tstz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func decisionEvidenceFromStorage(
	id uuid.UUID,
	purpose domain.DecisionPurpose,
	algorithm string,
	inputsJSON []byte,
	seed []byte,
	resultJSON []byte,
	digest []byte,
	ownerID uuid.UUID,
	decidedAt time.Time,
) (domain.DecisionEvidence, error) {
	if len(seed) != domain.DecisionSeedSize || len(digest) != domain.DecisionSeedSize {
		return domain.DecisionEvidence{}, domain.ErrValidation
	}
	var inputs, result []string
	if err := json.Unmarshal(inputsJSON, &inputs); err != nil {
		return domain.DecisionEvidence{}, err
	}
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		return domain.DecisionEvidence{}, err
	}
	evidence := domain.DecisionEvidence{
		ID:               id,
		Purpose:          purpose,
		AlgorithmVersion: algorithm,
		NormalizedInputs: inputs,
		Result:           result,
		OwnerID:          ownerID,
		DecidedAt:        decidedAt.UTC(),
	}
	copy(evidence.Seed[:], seed)
	copy(evidence.ReplayDigest[:], digest)
	if err := evidence.Validate(); err != nil {
		return domain.DecisionEvidence{}, err
	}
	return evidence, nil
}

func categoryJSON(categories []domain.Category) ([]byte, error) {
	values := make([]string, len(categories))
	for index, category := range categories {
		values[index] = string(category)
	}
	return marshalJSON("category sequence", values)
}

func marshalJSON(operation string, value any) ([]byte, error) {
	reflected := reflect.ValueOf(value)
	if reflected.IsValid() && reflected.Kind() == reflect.Slice && reflected.IsNil() {
		return []byte("[]"), nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%s - marshal JSON: %w", operation, err)
	}
	return data, nil
}

func requiredJSONObject(value map[string]any) ([]byte, error) {
	return assignmentadapter.RequiredJSONObject(value)
}

func mapRepositoryWriteError(operation string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505", "23503", "23514", "40001":
			return domain.WrapError(err, domain.ErrConflict)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func progressionInt32(value int) (int32, error) {
	if value < math.MinInt32 || value > math.MaxInt32 {
		return 0, domain.ErrConflict
	}
	return int32(value), nil
}

func tournamentAdminExecutionID(namespace uuid.UUID, role string) uuid.UUID {
	return uuid.NewSHA1(namespace, []byte(role))
}

func swissDraftIdentity(seriesID uuid.UUID) (playoff.FinalStageIDs, error) {
	ids, err := playoff.FinalStageIdentity(seriesID)
	if err != nil {
		return playoff.FinalStageIDs{}, err
	}
	ids.FinalSeriesID = seriesID
	ids.CategoryRevisionID = tournamentAdminExecutionID(seriesID, "swiss-category-revision")
	ids.DraftID = tournamentAdminExecutionID(seriesID, "swiss-draft")
	ids.FirstSlotID = tournamentAdminExecutionID(seriesID, "swiss-game-slot-1")
	if !ids.Valid() {
		return playoff.FinalStageIDs{}, domain.ErrValidation
	}
	return ids, nil
}

func exactDraftIdentity(commandID, seriesID uuid.UUID, format domain.SeriesFormat) (playoff.FinalStageIDs, error) {
	if format == domain.SeriesFormatBO1 {
		if commandID != seriesID {
			return playoff.FinalStageIDs{}, domain.ErrConflict
		}
		return swissDraftIdentity(seriesID)
	}
	if format != domain.SeriesFormatBO3 {
		return playoff.FinalStageIDs{}, domain.ErrConflict
	}
	return playoff.FinalStageIdentity(commandID)
}

func exactDraftCategoryCount(format domain.SeriesFormat) int {
	switch format {
	case domain.SeriesFormatBO1:
		return 1
	case domain.SeriesFormatBO3:
		return 3
	default:
		return 0
	}
}

func taskSnapshotParams(
	edge AssignmentEdgeInput,
	createdAt time.Time,

) (sqlc.CreateAssignmentTaskSnapshotParams, error) {
	return assignmentadapter.TaskSnapshotParams(edge, createdAt)
}

func taskSnapshotRecord(row sqlc.TaskSnapshot) (TaskSnapshotRecord, error) {
	return assignmentadapter.MapTaskSnapshotRecord(row)
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
	execution := participantDraftExecutionFromState(aggregate, revision, firstDecision, actions, recovery, transition)
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
		FirstParticipantID: aggregate.Draft.FirstParticipantID, SecondParticipantID: aggregate.Draft.SecondParticipantID,
		Pool: append([]domain.Category(nil), aggregate.Pool...), State: state,
		RevisionID: revision.ID, Revision: revision.Revision, CommandID: revision.CommandID,
		ServiceEpoch: revision.ServiceEpoch, Turn: revision.TurnNumber,
		CurrentActorID:   cloneParticipantUUIDPointer(revision.CurrentActorID),
		CurrentAction:    cloneDraftActionPointer(revision.CurrentAction),
		AbsoluteDeadline: cloneParticipantTimePointer(revision.AbsoluteDeadline),
		Recovery:         recovery, Transition: transition, Actions: actions,
		SelectedCategories: append([]domain.Category(nil), revision.SelectedCategories...),
		FirstActorDecision: *cloneDecisionEvidence(firstDecision),
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

func participantDraftActionsAt(aggregate *DraftAggregate, targetRevision int64) []draftusecase.ActionRecord {
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

func participantDraftEvidence(document map[string]any) (*draftusecase.RecoveryEvidence, *draftusecase.TransitionEvidence, error) {
	if len(document) == 0 {
		return nil, nil, nil
	}
	payload, err := json.Marshal(document)
	if err != nil {
		return nil, nil, domain.ErrInternal
	}
	var evidence participantDraftEvidenceDocument
	if err := json.Unmarshal(payload, &evidence); err != nil {
		return nil, nil, domain.ErrInternal
	}
	return evidence.Recovery, evidence.Transition, nil
}

func participantDraftRevisionByID(revisions []DraftRevisionRecord, revisionID uuid.UUID) (DraftRevisionRecord, bool) {
	for _, revision := range revisions {
		if revision.ID == revisionID {
			return revision, true
		}
	}
	return DraftRevisionRecord{}, false
}

func participantDraftLegalCategories(pool []domain.Category, actions []draftusecase.ActionRecord) []domain.Category {
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
