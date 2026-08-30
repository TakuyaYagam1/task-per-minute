package arena

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const DraftTurnDuration = 15 * time.Second

type DraftExecutionState string

const (
	DraftExecutionStateActive           DraftExecutionState = "active"
	DraftExecutionStatePaused           DraftExecutionState = "paused"
	DraftExecutionStateRecoveryRequired DraftExecutionState = "recovery_required"
	DraftExecutionStateCompleted        DraftExecutionState = "completed"
	DraftExecutionStateSuperseded       DraftExecutionState = "superseded"
)

type DraftRecoveryPolicy string

const (
	DraftRecoveryPolicyShiftRemaining DraftRecoveryPolicy = "shift_remaining"
	DraftRecoveryPolicyFreshOnResume  DraftRecoveryPolicy = "fresh_on_resume"
)

type DraftRecoveryReason string

const (
	DraftRecoveryReasonOperatorPause  DraftRecoveryReason = "operator_pause"
	DraftRecoveryReasonEpochMismatch  DraftRecoveryReason = "epoch_mismatch"
	DraftRecoveryReasonServiceRestart DraftRecoveryReason = "service_restart"
	DraftRecoveryReasonCorrection     DraftRecoveryReason = "correction"
)

type DraftTransitionOperation string

const (
	DraftTransitionPause         DraftTransitionOperation = "pause"
	DraftTransitionResume        DraftTransitionOperation = "resume"
	DraftTransitionEpochRecovery DraftTransitionOperation = "epoch_recovery"
	DraftTransitionFreshResume   DraftTransitionOperation = "fresh_resume"
	DraftTransitionSupersede     DraftTransitionOperation = "supersede"
)

var (
	ErrInvalidDraftExecution = errors.New("invalid draft execution")
	ErrDraftNotFound         = errors.New("arena draft not found")
	ErrDraftActionConflict   = errors.New("arena draft action conflict")
)

type DraftRecoveryEvidence struct {
	Policy               DraftRecoveryPolicy
	Reason               DraftRecoveryReason
	PreviousState        DraftExecutionState
	PreviousServiceEpoch uuid.UUID
	CurrentServiceEpoch  uuid.UUID
	PreviousDeadline     time.Time
	RecordedAt           time.Time
	ActorID              uuid.UUID
	Note                 string
}

type DraftTransitionEvidence struct {
	Operation  DraftTransitionOperation
	ActorID    uuid.UUID
	Reason     string
	OccurredAt time.Time
}

type DraftActionRecord struct {
	ID                uuid.UUID
	ResultRevisionID  uuid.UUID
	CommandID         uuid.UUID
	Turn              int
	ActorID           uuid.UUID
	Action            domain.ArenaDraftActionType
	Category          domain.Category
	ScheduledDeadline time.Time
	OccurredAt        time.Time
	Automatic         bool
	DecisionEvidence  *domain.ArenaDecisionEvidence
}

type DraftExecution struct {
	ID                  uuid.UUID
	SeriesID            uuid.UUID
	Format              domain.ArenaSeriesFormat
	FirstParticipantID  uuid.UUID
	SecondParticipantID uuid.UUID
	Pool                []domain.Category
	State               DraftExecutionState
	RevisionID          uuid.UUID
	PreviousRevisionID  uuid.UUID
	Revision            int64
	CommandID           uuid.UUID
	ServiceEpoch        uuid.UUID
	Turn                int
	CurrentActorID      *uuid.UUID
	CurrentAction       *domain.ArenaDraftActionType
	TurnDeadline        time.Time
	AbsoluteDeadline    *time.Time
	PausedRemaining     time.Duration
	Recovery            *DraftRecoveryEvidence
	Transition          *DraftTransitionEvidence
	LegalCategories     []domain.Category
	Actions             []DraftActionRecord
	SelectedCategories  []domain.Category
	FirstActorDecision  domain.ArenaDecisionEvidence
}

type DraftExecutionStartCommand struct {
	CategoryRevision   SeriesCategoryRevision
	DraftID            uuid.UUID
	InitialRevisionID  uuid.UUID
	DecisionEvidenceID uuid.UUID
	ParticipantIDs     [2]uuid.UUID
	ServiceEpoch       uuid.UUID
	CommandID          uuid.UUID
	StartedAt          time.Time
}

type DraftRevisionExpectation struct {
	RevisionID   uuid.UUID
	Revision     int64
	ServiceEpoch uuid.UUID
}

type DraftPlayerActionCommand struct {
	DraftID              uuid.UUID
	ExpectedRevisionID   uuid.UUID
	ExpectedRevision     int64
	ExpectedServiceEpoch uuid.UUID
	ExpectedTurn         int
	CommandID            uuid.UUID
	ResultRevisionID     uuid.UUID
	ActionID             uuid.UUID
	ActorID              uuid.UUID
	Action               domain.ArenaDraftActionType
	Category             domain.Category
}

type DraftTimeoutArm struct {
	DraftID      uuid.UUID
	RevisionID   uuid.UUID
	Revision     int64
	ServiceEpoch uuid.UUID
	Turn         int
	Deadline     time.Time
}

type DraftActionResult struct {
	Draft       DraftExecution
	Changed     bool
	NextTimeout *DraftTimeoutArm
}

// DraftRepository owns one atomic compare-and-set over the current immutable
// revision. Multi-revision recovery commits must succeed or roll back as one
// transaction, and command lookup must include the complete revision history.
type DraftRepository interface {
	LoadDraft(ctx context.Context, draftID uuid.UUID) (*DraftExecution, error)
	FindDraftCommand(ctx context.Context, draftID uuid.UUID, commandID uuid.UUID) (*DraftExecution, error)
	CommitDraftRevisions(
		ctx context.Context,
		expected DraftRevisionExpectation,
		revisions []DraftExecution,
	) (*DraftExecution, bool, error)
}

type DraftActionUseCase struct {
	repository DraftRepository
	clock      Clock
}

func NewDraftActionUseCase(repository DraftRepository, clock Clock) *DraftActionUseCase {
	return &DraftActionUseCase{repository: repository, clock: clock}
}

func StartDraftExecution(command DraftExecutionStartCommand) (DraftExecution, error) {
	if err := validateDraftExecutionStartCommand(command); err != nil {
		return DraftExecution{}, err
	}
	inputs := []string{command.ParticipantIDs[0].String(), command.ParticipantIDs[1].String()}
	evidence, err := domain.NewArenaDecisionEvidence(
		command.DecisionEvidenceID,
		domain.ArenaDecisionPurposeDraftOrder,
		domain.ArenaDecisionAlgorithmV1,
		inputs,
		command.DraftID,
		command.StartedAt,
	)
	if err != nil {
		return DraftExecution{}, draftExecutionError("first actor decision: %v", err)
	}
	firstParticipantID, err := uuid.Parse(evidence.Result[0])
	if err != nil {
		return DraftExecution{}, draftExecutionError("first actor decision result: %v", err)
	}
	secondParticipantID, err := uuid.Parse(evidence.Result[1])
	if err != nil {
		return DraftExecution{}, draftExecutionError("second actor decision result: %v", err)
	}

	deadline := command.StartedAt.Add(DraftTurnDuration)
	start := DraftStartCommand{
		DraftID: command.DraftID, FirstParticipantID: firstParticipantID,
		SecondParticipantID: secondParticipantID, FirstDeadline: deadline,
	}
	var draft domain.ArenaDraft
	switch command.CategoryRevision.Format {
	case domain.ArenaSeriesFormatBO1:
		draft, err = NewBO1Draft(command.CategoryRevision, start)
	case domain.ArenaSeriesFormatBO3:
		draft, err = NewBO3FinalDraft(command.CategoryRevision, start)
	default:
		err = domain.ErrValidation
	}
	if err != nil {
		return DraftExecution{}, draftExecutionError("start policy: %v", err)
	}
	execution, err := draftExecutionFromDomain(
		draft,
		DraftExecutionStateActive,
		command.InitialRevisionID,
		uuid.Nil,
		1,
		command.CommandID,
		command.ServiceEpoch,
		evidence,
		nil,
	)
	if err != nil {
		return DraftExecution{}, err
	}
	return execution, nil
}

func (u *DraftActionUseCase) Apply(
	ctx context.Context,
	command DraftPlayerActionCommand,
) (DraftActionResult, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return DraftActionResult{}, domain.ErrValidation
	}
	if err := validateDraftPlayerActionCommand(command); err != nil {
		return DraftActionResult{}, err
	}
	if result, found, err := findRecordedDraftPlayerAction(ctx, u.repository, command); found || err != nil {
		return result, err
	}

	current, err := loadDraftExecution(ctx, u.repository, command.DraftID, "DraftActionUseCase")
	if err != nil {
		return DraftActionResult{}, err
	}
	if current.State == DraftExecutionStateCompleted || current.State == DraftExecutionStateSuperseded {
		if current.CommandID == command.CommandID {
			return reconcileDraftPlayerAction(current, command)
		}
		return DraftActionResult{}, domain.ErrArenaDraftCompleted
	}
	if current.State != DraftExecutionStateActive {
		return DraftActionResult{}, ErrDraftActionConflict
	}
	if !draftExpectationMatches(current, command.ExpectedRevisionID, command.ExpectedRevision, command.ExpectedServiceEpoch) {
		return reconcileDraftActionConflict(ctx, u.repository, command)
	}

	now := u.clock.Now()
	if !validArenaServerTime(now) {
		return DraftActionResult{}, domain.ErrValidation
	}
	next, err := applyDraftAction(current, command, now, false, nil)
	if err != nil {
		return DraftActionResult{}, err
	}
	return commitDraftPlayerAction(ctx, u.repository, current, next, command)
}

func findRecordedDraftPlayerAction(
	ctx context.Context,
	repository DraftRepository,
	command DraftPlayerActionCommand,
) (DraftActionResult, bool, error) {
	recorded, err := repository.FindDraftCommand(ctx, command.DraftID, command.CommandID)
	if err != nil {
		return DraftActionResult{}, true, fmt.Errorf("DraftActionUseCase - find command: %w", err)
	}
	if recorded == nil {
		return DraftActionResult{}, false, nil
	}
	result, err := reconcileDraftPlayerAction(*recorded, command)
	return result, true, err
}

func loadDraftExecution(
	ctx context.Context,
	repository DraftRepository,
	draftID uuid.UUID,
	owner string,
) (DraftExecution, error) {
	current, err := repository.LoadDraft(ctx, draftID)
	if err != nil {
		return DraftExecution{}, fmt.Errorf("%s - load draft: %w", owner, err)
	}
	if current == nil {
		return DraftExecution{}, ErrDraftNotFound
	}
	if err := current.Validate(); err != nil {
		return DraftExecution{}, domain.ErrInternal
	}
	return cloneDraftExecution(*current), nil
}

func (d DraftExecution) Validate() error {
	if err := validateDraftExecutionIdentity(d); err != nil {
		return err
	}
	if err := validateDraftFirstActorDecision(d); err != nil {
		return err
	}
	domainDraft, err := d.domainDraft()
	if err != nil {
		return err
	}
	if err := domainDraft.Validate(); err != nil {
		return draftExecutionError("domain snapshot: %v", err)
	}
	if err := validateDraftActionRecords(d, domainDraft); err != nil {
		return err
	}
	if err := validateDraftRecoveryEvidence(d); err != nil {
		return err
	}
	if err := validateDraftTransitionEvidence(d); err != nil {
		return err
	}
	return validateDraftExecutionState(d, domainDraft)
}

func validateDraftExecutionStartCommand(command DraftExecutionStartCommand) error {
	ids := []uuid.UUID{
		command.DraftID, command.InitialRevisionID, command.DecisionEvidenceID,
		command.ParticipantIDs[0], command.ParticipantIDs[1], command.ServiceEpoch, command.CommandID,
	}
	seen := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			return draftExecutionError("missing identity")
		}
		if _, duplicate := seen[id]; duplicate {
			return draftExecutionError("reused identity")
		}
		seen[id] = struct{}{}
	}
	if !validArenaServerTime(command.StartedAt) || command.StartedAt.Before(command.CategoryRevision.CreatedAt) {
		return draftExecutionError("start timestamp must be server UTC after category revision")
	}
	if err := command.CategoryRevision.Validate(); err != nil ||
		command.CategoryRevision.Mode != domain.ArenaCategoryModeDraft {
		return draftExecutionError("category revision must be valid draft mode")
	}
	return nil
}

func validateDraftPlayerActionCommand(command DraftPlayerActionCommand) error {
	ids := []uuid.UUID{
		command.DraftID, command.ExpectedRevisionID, command.ExpectedServiceEpoch,
		command.CommandID, command.ResultRevisionID, command.ActionID, command.ActorID,
	}
	seen := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			return domain.ErrValidation
		}
		if _, duplicate := seen[id]; duplicate {
			return domain.ErrValidation
		}
		seen[id] = struct{}{}
	}
	if command.ExpectedRevision < 1 || command.ExpectedTurn < 1 ||
		!command.Action.IsValid() || !command.Category.IsValid() {
		return domain.ErrValidation
	}
	return nil
}

func applyDraftAction(
	current DraftExecution,
	command DraftPlayerActionCommand,
	occurredAt time.Time,
	automatic bool,
	decision *domain.ArenaDecisionEvidence,
) (DraftExecution, error) {
	draft, err := current.domainDraft()
	if err != nil {
		return DraftExecution{}, err
	}
	nextDeadline := occurredAt.Add(DraftTurnDuration)
	if draftFinalTurn(draft) {
		nextDeadline = time.Time{}
	}
	action := DraftActionCommand{
		ExpectedTurn: command.ExpectedTurn, ActorID: command.ActorID, Action: command.Action,
		Category: command.Category, OccurredAt: occurredAt, NextDeadline: nextDeadline,
	}
	var nextDomain domain.ArenaDraft
	switch draft.Format {
	case domain.ArenaSeriesFormatBO1:
		nextDomain, err = ApplyBO1DraftAction(draft, action)
	case domain.ArenaSeriesFormatBO3:
		nextDomain, err = ApplyBO3FinalDraftAction(draft, action)
	default:
		err = domain.ErrValidation
	}
	if err != nil {
		return DraftExecution{}, err
	}

	nextState := DraftExecutionStateActive
	if nextDomain.State == domain.ArenaDraftStateCompleted {
		nextState = DraftExecutionStateCompleted
	}
	nextActions := append(cloneDraftActionRecords(current.Actions), DraftActionRecord{
		ID: command.ActionID, ResultRevisionID: command.ResultRevisionID, CommandID: command.CommandID,
		Turn: command.ExpectedTurn, ActorID: command.ActorID, Action: command.Action, Category: command.Category,
		ScheduledDeadline: current.TurnDeadline, OccurredAt: occurredAt, Automatic: automatic,
		DecisionEvidence: cloneDraftDecisionEvidence(decision),
	})
	next, err := draftExecutionFromDomain(
		nextDomain,
		nextState,
		command.ResultRevisionID,
		current.RevisionID,
		current.Revision+1,
		command.CommandID,
		current.ServiceEpoch,
		current.FirstActorDecision,
		nextActions,
	)
	if err != nil {
		return DraftExecution{}, err
	}
	return next, nil
}

func commitDraftPlayerAction(
	ctx context.Context,
	repository DraftRepository,
	current DraftExecution,
	next DraftExecution,
	command DraftPlayerActionCommand,
) (DraftActionResult, error) {
	committed, changed, err := repository.CommitDraftRevisions(
		ctx,
		draftExpectation(current),
		[]DraftExecution{next},
	)
	if errors.Is(err, domain.ErrConflict) {
		return reconcileDraftActionConflict(ctx, repository, command)
	}
	if err != nil {
		return DraftActionResult{}, fmt.Errorf("DraftActionUseCase - commit action: %w", err)
	}
	if committed == nil {
		return reconcileDraftActionConflict(ctx, repository, command)
	}
	result, reconcileErr := reconcileDraftPlayerAction(*committed, command)
	if reconcileErr != nil {
		return DraftActionResult{}, domain.ErrInternal
	}
	result.Changed = changed
	return result, nil
}

func reconcileDraftActionConflict(
	ctx context.Context,
	repository DraftRepository,
	command DraftPlayerActionCommand,
) (DraftActionResult, error) {
	recorded, err := repository.FindDraftCommand(ctx, command.DraftID, command.CommandID)
	if err != nil {
		return DraftActionResult{}, fmt.Errorf("DraftActionUseCase - reconcile command: %w", err)
	}
	if recorded == nil {
		return DraftActionResult{}, ErrDraftActionConflict
	}
	return reconcileDraftPlayerAction(*recorded, command)
}

func reconcileDraftPlayerAction(
	recorded DraftExecution,
	command DraftPlayerActionCommand,
) (DraftActionResult, error) {
	if err := recorded.Validate(); err != nil || recorded.CommandID != command.CommandID ||
		recorded.RevisionID != command.ResultRevisionID || len(recorded.Actions) == 0 {
		return DraftActionResult{}, ErrDraftActionConflict
	}
	action := recorded.Actions[len(recorded.Actions)-1]
	if action.ID != command.ActionID || action.CommandID != command.CommandID ||
		action.ResultRevisionID != command.ResultRevisionID || action.Turn != command.ExpectedTurn ||
		action.ActorID != command.ActorID || action.Action != command.Action ||
		action.Category != command.Category || action.Automatic {
		return DraftActionResult{}, ErrDraftActionConflict
	}
	return DraftActionResult{
		Draft:       cloneDraftExecution(recorded),
		NextTimeout: draftTimeoutArm(recorded),
	}, nil
}

func draftExecutionFromDomain(
	draft domain.ArenaDraft,
	state DraftExecutionState,
	revisionID uuid.UUID,
	previousRevisionID uuid.UUID,
	revision int64,
	commandID uuid.UUID,
	serviceEpoch uuid.UUID,
	firstActorDecision domain.ArenaDecisionEvidence,
	actions []DraftActionRecord,
) (DraftExecution, error) {
	execution := DraftExecution{
		ID: draft.ID, SeriesID: draft.SeriesID, Format: draft.Format,
		FirstParticipantID: draft.FirstParticipantID, SecondParticipantID: draft.SecondParticipantID,
		Pool: append([]domain.Category(nil), draft.Pool...), State: state,
		RevisionID: revisionID, PreviousRevisionID: previousRevisionID, Revision: revision,
		CommandID: commandID, ServiceEpoch: serviceEpoch, Turn: draft.Turn,
		TurnDeadline: draft.TurnDeadline, Actions: cloneDraftActionRecords(actions),
		SelectedCategories: append([]domain.Category(nil), draft.SelectedCategories...),
		FirstActorDecision: cloneDraftDecisionEvidenceValue(firstActorDecision),
	}
	if state == DraftExecutionStateActive {
		deadline := draft.TurnDeadline
		execution.AbsoluteDeadline = &deadline
	}
	if state == DraftExecutionStateActive {
		turn, err := draft.CurrentTurn()
		if err != nil {
			return DraftExecution{}, draftExecutionError("current turn: %v", err)
		}
		execution.CurrentActorID = cloneDraftUUID(&turn.ActorID)
		execution.CurrentAction = cloneDraftActionType(&turn.Action)
		execution.LegalCategories = draftLegalCategories(draft)
	}
	if err := execution.Validate(); err != nil {
		return DraftExecution{}, err
	}
	return cloneDraftExecution(execution), nil
}

func validateDraftExecutionIdentity(d DraftExecution) error {
	if d.ID == uuid.Nil || d.SeriesID == uuid.Nil || d.RevisionID == uuid.Nil || d.Revision < 1 ||
		d.CommandID == uuid.Nil || d.ServiceEpoch == uuid.Nil || !d.Format.IsValid() ||
		d.FirstParticipantID == uuid.Nil || d.SecondParticipantID == uuid.Nil ||
		d.FirstParticipantID == d.SecondParticipantID {
		return draftExecutionError("missing identity or revision")
	}
	if (d.Revision == 1) != (d.PreviousRevisionID == uuid.Nil) || d.PreviousRevisionID == d.RevisionID {
		return draftExecutionError("invalid revision predecessor")
	}
	return nil
}

func validateDraftFirstActorDecision(d DraftExecution) error {
	evidence := d.FirstActorDecision
	if err := evidence.Validate(); err != nil || evidence.Purpose != domain.ArenaDecisionPurposeDraftOrder ||
		evidence.OwnerID != d.ID || len(evidence.Result) != 2 ||
		evidence.Result[0] != d.FirstParticipantID.String() ||
		evidence.Result[1] != d.SecondParticipantID.String() {
		return draftExecutionError("first actor evidence does not match participants")
	}
	return nil
}

func (d DraftExecution) domainDraft() (domain.ArenaDraft, error) {
	state := domain.ArenaDraftStateActive
	deadline := d.TurnDeadline
	selected := []domain.Category(nil)
	if d.State == DraftExecutionStateCompleted {
		state = domain.ArenaDraftStateCompleted
		deadline = time.Time{}
		selected = append([]domain.Category(nil), d.SelectedCategories...)
	}
	actions := make([]domain.ArenaDraftAction, len(d.Actions))
	for index, action := range d.Actions {
		actions[index] = domain.ArenaDraftAction{
			Turn: action.Turn, ActorID: action.ActorID, Action: action.Action, Category: action.Category,
			OccurredAt: action.OccurredAt, TurnDeadline: action.ScheduledDeadline,
		}
	}
	draft := domain.ArenaDraft{
		ID: d.ID, SeriesID: d.SeriesID, Format: d.Format,
		FirstParticipantID: d.FirstParticipantID, SecondParticipantID: d.SecondParticipantID,
		Pool: append([]domain.Category(nil), d.Pool...), State: state, Turn: d.Turn,
		TurnDeadline: deadline, Actions: actions, SelectedCategories: selected,
	}
	if err := draft.Validate(); err != nil {
		return domain.ArenaDraft{}, draftExecutionError("domain snapshot: %v", err)
	}
	return draft, nil
}

func validateDraftActionRecords(d DraftExecution, draft domain.ArenaDraft) error {
	if len(d.Actions) != len(draft.Actions) {
		return draftExecutionError("action evidence count does not match")
	}
	seenCommands := make(map[uuid.UUID]struct{}, len(d.Actions))
	seenIDs := make(map[uuid.UUID]struct{}, len(d.Actions))
	available := append([]domain.Category(nil), d.Pool...)
	for index, action := range d.Actions {
		if err := validateDraftActionIdentity(action, index, seenCommands, seenIDs); err != nil {
			return err
		}
		seenCommands[action.CommandID] = struct{}{}
		seenIDs[action.ID] = struct{}{}
		if err := validateDraftActionDecision(d.ID, action, available); err != nil {
			return err
		}
		available = slices.DeleteFunc(available, func(category domain.Category) bool {
			return category == action.Category
		})
	}
	return nil
}

func validateDraftActionIdentity(
	action DraftActionRecord,
	index int,
	seenCommands map[uuid.UUID]struct{},
	seenIDs map[uuid.UUID]struct{},
) error {
	if action.ID == uuid.Nil || action.ResultRevisionID == uuid.Nil || action.CommandID == uuid.Nil ||
		action.Turn != index+1 || action.ID == action.ResultRevisionID || action.ID == action.CommandID ||
		action.ResultRevisionID == action.CommandID {
		return draftExecutionError("invalid action identity")
	}
	if _, duplicate := seenCommands[action.CommandID]; duplicate {
		return draftExecutionError("duplicate action command")
	}
	if _, duplicate := seenIDs[action.ID]; duplicate {
		return draftExecutionError("duplicate action identity")
	}
	return nil
}

func validateDraftActionDecision(
	draftID uuid.UUID,
	action DraftActionRecord,
	available []domain.Category,
) error {
	if !action.Automatic {
		if action.DecisionEvidence != nil {
			return draftExecutionError("player action contains automatic decision evidence")
		}
		return nil
	}
	if action.DecisionEvidence == nil {
		return draftExecutionError("automatic action decision evidence is missing")
	}
	evidence := action.DecisionEvidence
	if evidence.Purpose != domain.ArenaDecisionPurposeCategory || evidence.OwnerID != draftID ||
		evidence.Validate() != nil || !evidence.DecidedAt.Equal(action.ScheduledDeadline) ||
		!slices.Equal(evidence.NormalizedInputs, categoryDecisionInputs(available)) ||
		len(evidence.Result) != len(available) || domain.Category(evidence.Result[0]) != action.Category {
		return draftExecutionError("automatic action decision evidence is invalid")
	}
	return nil
}

func validateDraftExecutionState(d DraftExecution, draft domain.ArenaDraft) error {
	switch d.State {
	case DraftExecutionStateActive:
		return validateActiveDraftExecution(d, draft)
	case DraftExecutionStatePaused:
		return validatePausedDraftExecution(d, draft)
	case DraftExecutionStateRecoveryRequired:
		return validateRecoveringDraftExecution(d, draft)
	case DraftExecutionStateCompleted:
		return validateCompletedDraftExecution(d, draft)
	case DraftExecutionStateSuperseded:
		return validateSupersededDraftExecution(d)
	default:
		return draftExecutionError("unknown state %q", d.State)
	}
}

func validateActiveDraftExecution(d DraftExecution, draft domain.ArenaDraft) error {
	if d.AbsoluteDeadline == nil || !d.AbsoluteDeadline.Equal(d.TurnDeadline) ||
		d.PausedRemaining != 0 || d.Recovery != nil {
		return draftExecutionError("invalid active deadline evidence")
	}
	return validateDraftCurrentTurn(d, draft)
}

func validatePausedDraftExecution(d DraftExecution, draft domain.ArenaDraft) error {
	if d.AbsoluteDeadline != nil || d.PausedRemaining <= 0 || d.PausedRemaining > DraftTurnDuration ||
		d.Recovery == nil || d.Recovery.Policy != DraftRecoveryPolicyShiftRemaining ||
		d.Recovery.Reason != DraftRecoveryReasonOperatorPause {
		return draftExecutionError("invalid paused evidence")
	}
	return validateDraftCurrentTurn(d, draft)
}

func validateRecoveringDraftExecution(d DraftExecution, draft domain.ArenaDraft) error {
	if d.AbsoluteDeadline != nil || d.PausedRemaining != 0 || d.Recovery == nil ||
		d.Recovery.Policy != DraftRecoveryPolicyFreshOnResume ||
		(d.Recovery.Reason != DraftRecoveryReasonEpochMismatch &&
			d.Recovery.Reason != DraftRecoveryReasonServiceRestart) {
		return draftExecutionError("invalid recovery evidence")
	}
	return validateDraftCurrentTurn(d, draft)
}

func validateCompletedDraftExecution(d DraftExecution, draft domain.ArenaDraft) error {
	if d.AbsoluteDeadline != nil || !d.TurnDeadline.IsZero() || d.PausedRemaining != 0 ||
		d.Recovery != nil || d.CurrentActorID != nil || d.CurrentAction != nil ||
		len(d.LegalCategories) != 0 || !slices.Equal(d.SelectedCategories, draft.SelectedCategories) {
		return draftExecutionError("invalid completed evidence")
	}
	return nil
}

func validateSupersededDraftExecution(d DraftExecution) error {
	if d.AbsoluteDeadline != nil || d.PausedRemaining != 0 || d.Recovery == nil ||
		d.Recovery.Reason != DraftRecoveryReasonCorrection || d.CurrentActorID != nil ||
		d.CurrentAction != nil || len(d.LegalCategories) != 0 || len(d.SelectedCategories) != 0 {
		return draftExecutionError("invalid superseded evidence")
	}
	return nil
}

func validateDraftTransitionEvidence(d DraftExecution) error {
	if d.Transition == nil {
		return nil
	}
	transition := d.Transition
	if transition.ActorID == uuid.Nil || transition.Reason == "" ||
		transition.Reason != strings.TrimSpace(transition.Reason) ||
		!validArenaServerTime(transition.OccurredAt) {
		return draftExecutionError("invalid transition evidence")
	}
	switch transition.Operation {
	case DraftTransitionPause:
		if d.State != DraftExecutionStatePaused {
			return draftExecutionError("pause transition has wrong state")
		}
	case DraftTransitionResume, DraftTransitionFreshResume:
		if d.State != DraftExecutionStateActive {
			return draftExecutionError("resume transition has wrong state")
		}
	case DraftTransitionEpochRecovery:
		if d.State != DraftExecutionStateRecoveryRequired {
			return draftExecutionError("epoch recovery transition has wrong state")
		}
	case DraftTransitionSupersede:
		if d.State != DraftExecutionStateSuperseded {
			return draftExecutionError("supersede transition has wrong state")
		}
	default:
		return draftExecutionError("unknown transition operation %q", transition.Operation)
	}
	return nil
}

func validateDraftRecoveryEvidence(d DraftExecution) error {
	if d.Recovery == nil {
		return nil
	}
	if err := validateDraftRecoveryMetadata(d); err != nil {
		return err
	}
	switch d.Recovery.Reason {
	case DraftRecoveryReasonOperatorPause:
		return validateOperatorPauseRecovery(d)
	case DraftRecoveryReasonEpochMismatch, DraftRecoveryReasonServiceRestart:
		return validateEpochRecovery(d)
	case DraftRecoveryReasonCorrection:
		return validateDraftCorrection(d)
	default:
		return draftExecutionError("unknown recovery reason %q", d.Recovery.Reason)
	}
}

func validateDraftRecoveryMetadata(d DraftExecution) error {
	recovery := d.Recovery
	if recovery.PreviousState == "" || recovery.PreviousServiceEpoch == uuid.Nil ||
		recovery.CurrentServiceEpoch == uuid.Nil || recovery.ActorID == uuid.Nil ||
		!validArenaServerTime(recovery.PreviousDeadline) || !validArenaServerTime(recovery.RecordedAt) ||
		recovery.Note == "" || recovery.Note != strings.TrimSpace(recovery.Note) ||
		recovery.CurrentServiceEpoch != d.ServiceEpoch ||
		!recovery.PreviousDeadline.Equal(d.TurnDeadline) {
		return draftExecutionError("invalid recovery identity or timestamp evidence")
	}
	if d.Transition == nil || d.Transition.ActorID != recovery.ActorID ||
		d.Transition.Reason != recovery.Note || !d.Transition.OccurredAt.Equal(recovery.RecordedAt) {
		return draftExecutionError("recovery and transition evidence do not match")
	}
	return nil
}

func validateOperatorPauseRecovery(d DraftExecution) error {
	recovery := d.Recovery
	if d.State != DraftExecutionStatePaused || recovery.PreviousState != DraftExecutionStateActive ||
		recovery.Policy != DraftRecoveryPolicyShiftRemaining ||
		recovery.PreviousServiceEpoch != recovery.CurrentServiceEpoch ||
		!recovery.RecordedAt.Before(recovery.PreviousDeadline) ||
		recovery.PreviousDeadline.Sub(recovery.RecordedAt) != d.PausedRemaining {
		return draftExecutionError("operator pause evidence does not match")
	}
	return nil
}

func validateEpochRecovery(d DraftExecution) error {
	recovery := d.Recovery
	if d.State != DraftExecutionStateRecoveryRequired ||
		recovery.PreviousState != DraftExecutionStateActive ||
		recovery.Policy != DraftRecoveryPolicyFreshOnResume ||
		recovery.PreviousServiceEpoch == recovery.CurrentServiceEpoch {
		return draftExecutionError("epoch recovery evidence does not match")
	}
	return nil
}

func validateDraftCorrection(d DraftExecution) error {
	recovery := d.Recovery
	if d.State != DraftExecutionStateSuperseded ||
		(recovery.PreviousState != DraftExecutionStateActive &&
			recovery.PreviousState != DraftExecutionStatePaused) ||
		recovery.Policy != "" || recovery.PreviousServiceEpoch != recovery.CurrentServiceEpoch ||
		(recovery.PreviousState == DraftExecutionStateActive &&
			!recovery.RecordedAt.Before(recovery.PreviousDeadline)) {
		return draftExecutionError("correction evidence does not match")
	}
	return nil
}

func validateDraftCurrentTurn(d DraftExecution, draft domain.ArenaDraft) error {
	turn, err := draft.CurrentTurn()
	if err != nil || d.CurrentActorID == nil || d.CurrentAction == nil ||
		*d.CurrentActorID != turn.ActorID || *d.CurrentAction != turn.Action ||
		!slices.Equal(d.LegalCategories, draftLegalCategories(draft)) {
		return draftExecutionError("current turn evidence does not match")
	}
	return nil
}

func draftLegalCategories(draft domain.ArenaDraft) []domain.Category {
	used := make(map[domain.Category]struct{}, len(draft.Actions))
	for _, action := range draft.Actions {
		used[action.Category] = struct{}{}
	}
	legal := make([]domain.Category, 0, len(draft.Pool)-len(used))
	for _, category := range draft.Pool {
		if _, exists := used[category]; !exists {
			legal = append(legal, category)
		}
	}
	return legal
}

func draftFinalTurn(draft domain.ArenaDraft) bool {
	return (draft.Format == domain.ArenaSeriesFormatBO1 && draft.Turn == 2) ||
		(draft.Format == domain.ArenaSeriesFormatBO3 && draft.Turn == 4)
}

func draftExpectation(draft DraftExecution) DraftRevisionExpectation {
	return DraftRevisionExpectation{
		RevisionID: draft.RevisionID, Revision: draft.Revision, ServiceEpoch: draft.ServiceEpoch,
	}
}

func draftExpectationMatches(
	draft DraftExecution,
	revisionID uuid.UUID,
	revision int64,
	serviceEpoch uuid.UUID,
) bool {
	return draft.RevisionID == revisionID && draft.Revision == revision && draft.ServiceEpoch == serviceEpoch
}

func draftTimeoutArm(draft DraftExecution) *DraftTimeoutArm {
	if draft.State != DraftExecutionStateActive || draft.AbsoluteDeadline == nil {
		return nil
	}
	return &DraftTimeoutArm{
		DraftID: draft.ID, RevisionID: draft.RevisionID, Revision: draft.Revision,
		ServiceEpoch: draft.ServiceEpoch, Turn: draft.Turn, Deadline: *draft.AbsoluteDeadline,
	}
}

func cloneDraftExecution(draft DraftExecution) DraftExecution {
	cloned := draft
	cloned.Pool = append([]domain.Category(nil), draft.Pool...)
	cloned.CurrentActorID = cloneDraftUUID(draft.CurrentActorID)
	cloned.CurrentAction = cloneDraftActionType(draft.CurrentAction)
	cloned.AbsoluteDeadline = cloneDraftTime(draft.AbsoluteDeadline)
	cloned.LegalCategories = append([]domain.Category(nil), draft.LegalCategories...)
	cloned.Actions = cloneDraftActionRecords(draft.Actions)
	cloned.SelectedCategories = append([]domain.Category(nil), draft.SelectedCategories...)
	cloned.FirstActorDecision = cloneDraftDecisionEvidenceValue(draft.FirstActorDecision)
	if draft.Recovery != nil {
		recovery := *draft.Recovery
		cloned.Recovery = &recovery
	}
	if draft.Transition != nil {
		transition := *draft.Transition
		cloned.Transition = &transition
	}
	return cloned
}

func cloneDraftActionRecords(actions []DraftActionRecord) []DraftActionRecord {
	cloned := append([]DraftActionRecord(nil), actions...)
	for index := range cloned {
		cloned[index].DecisionEvidence = cloneDraftDecisionEvidence(actions[index].DecisionEvidence)
	}
	return cloned
}

func cloneDraftDecisionEvidence(evidence *domain.ArenaDecisionEvidence) *domain.ArenaDecisionEvidence {
	if evidence == nil {
		return nil
	}
	cloned := cloneDraftDecisionEvidenceValue(*evidence)
	return &cloned
}

func cloneDraftDecisionEvidenceValue(evidence domain.ArenaDecisionEvidence) domain.ArenaDecisionEvidence {
	cloned := evidence
	cloned.NormalizedInputs = append([]string(nil), evidence.NormalizedInputs...)
	cloned.Result = append([]string(nil), evidence.Result...)
	return cloned
}

func cloneDraftUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneDraftActionType(value *domain.ArenaDraftActionType) *domain.ArenaDraftActionType {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneDraftTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func draftExecutionError(message string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidDraftExecution, fmt.Sprintf(message, args...))
}
