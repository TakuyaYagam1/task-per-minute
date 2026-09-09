package draft

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const TurnDuration = 15 * time.Second

type Clock interface {
	Now() time.Time
}

type ExecutionState string

const (
	ExecutionStateActive           ExecutionState = "active"
	ExecutionStatePaused           ExecutionState = "paused"
	ExecutionStateRecoveryRequired ExecutionState = "recovery_required"
	ExecutionStateCompleted        ExecutionState = "completed"
	ExecutionStateSuperseded       ExecutionState = "superseded"
)

type RecoveryPolicy string

const (
	RecoveryPolicyShiftRemaining RecoveryPolicy = "shift_remaining"
	RecoveryPolicyFreshOnResume  RecoveryPolicy = "fresh_on_resume"
)

type RecoveryReason string

const (
	RecoveryReasonOperatorPause  RecoveryReason = "operator_pause"
	RecoveryReasonEpochMismatch  RecoveryReason = "epoch_mismatch"
	RecoveryReasonServiceRestart RecoveryReason = "service_restart"
	RecoveryReasonCorrection     RecoveryReason = "correction"
)

type TransitionOperation string

const (
	TransitionPause         TransitionOperation = "pause"
	TransitionResume        TransitionOperation = "resume"
	TransitionEpochRecovery TransitionOperation = "epoch_recovery"
	TransitionFreshResume   TransitionOperation = "fresh_resume"
	TransitionSupersede     TransitionOperation = "supersede"
)

var (
	ErrInvalidExecution = errors.New("invalid draft execution")
	ErrNotFound         = errors.New("draft not found")
	ErrActionConflict   = errors.New("draft action conflict")
)

type RecoveryEvidence struct {
	Policy               RecoveryPolicy
	Reason               RecoveryReason
	PreviousState        ExecutionState
	PreviousServiceEpoch uuid.UUID
	CurrentServiceEpoch  uuid.UUID
	PreviousDeadline     time.Time
	RecordedAt           time.Time
	ActorID              uuid.UUID
	Note                 string
}

type TransitionEvidence struct {
	Operation  TransitionOperation
	ActorID    uuid.UUID
	Reason     string
	OccurredAt time.Time
}

type ActionRecord struct {
	ID                uuid.UUID
	ResultRevisionID  uuid.UUID
	CommandID         uuid.UUID
	Turn              int
	ActorID           uuid.UUID
	Action            domain.DraftActionType
	Category          domain.Category
	ScheduledDeadline time.Time
	OccurredAt        time.Time
	Automatic         bool
	DecisionEvidence  *domain.DecisionEvidence
}

type Execution struct {
	ID                  uuid.UUID
	SeriesID            uuid.UUID
	Format              domain.SeriesFormat
	FirstParticipantID  uuid.UUID
	SecondParticipantID uuid.UUID
	Pool                []domain.Category
	State               ExecutionState
	RevisionID          uuid.UUID
	PreviousRevisionID  uuid.UUID
	Revision            int64
	CommandID           uuid.UUID
	ServiceEpoch        uuid.UUID
	Turn                int
	CurrentActorID      *uuid.UUID
	CurrentAction       *domain.DraftActionType
	TurnDeadline        time.Time
	AbsoluteDeadline    *time.Time
	PausedRemaining     time.Duration
	Recovery            *RecoveryEvidence
	Transition          *TransitionEvidence
	LegalCategories     []domain.Category
	Actions             []ActionRecord
	SelectedCategories  []domain.Category
	FirstActorDecision  domain.DecisionEvidence
}

type ExecutionStartCommand struct {
	CategoryRevision   CategoryRevision
	DraftID            uuid.UUID
	InitialRevisionID  uuid.UUID
	DecisionEvidenceID uuid.UUID
	ParticipantIDs     [2]uuid.UUID
	ServiceEpoch       uuid.UUID
	CommandID          uuid.UUID
	StartedAt          time.Time
}

type RevisionExpectation struct {
	RevisionID   uuid.UUID
	Revision     int64
	ServiceEpoch uuid.UUID
}

type PlayerActionCommand struct {
	DraftID              uuid.UUID
	ExpectedRevisionID   uuid.UUID
	ExpectedRevision     int64
	ExpectedServiceEpoch uuid.UUID
	ExpectedTurn         int
	CommandID            uuid.UUID
	ResultRevisionID     uuid.UUID
	ActionID             uuid.UUID
	ActorID              uuid.UUID
	Action               domain.DraftActionType
	Category             domain.Category
}

type TimeoutArm struct {
	DraftID      uuid.UUID
	RevisionID   uuid.UUID
	Revision     int64
	ServiceEpoch uuid.UUID
	Turn         int
	Deadline     time.Time
}

type ActionResult struct {
	Draft       Execution
	Changed     bool
	NextTimeout *TimeoutArm
}

// Repository owns one atomic compare-and-set over the current immutable
// revision. Multi-revision recovery commits must succeed or roll back as one
// transaction, and command lookup must include the complete revision history.
type Repository interface {
	LoadDraft(ctx context.Context, draftID uuid.UUID) (*Execution, error)
	FindDraftCommand(ctx context.Context, draftID uuid.UUID, commandID uuid.UUID) (*Execution, error)
	CommitDraftRevisions(
		ctx context.Context,
		expected RevisionExpectation,
		revisions []Execution,
	) (*Execution, bool, error)
}

type ActionUseCase struct {
	repository Repository
	clock      Clock
}

func NewActionUseCase(repository Repository, clock Clock) *ActionUseCase {
	return &ActionUseCase{repository: repository, clock: clock}
}

func StartExecution(command ExecutionStartCommand) (Execution, error) {
	if err := validateDraftExecutionStartCommand(command); err != nil {
		return Execution{}, err
	}
	inputs := []string{command.ParticipantIDs[0].String(), command.ParticipantIDs[1].String()}
	evidence, err := domain.NewDecisionEvidence(
		command.DecisionEvidenceID,
		domain.DecisionPurposeDraftOrder,
		domain.DecisionAlgorithmV1,
		inputs,
		command.DraftID,
		command.StartedAt,
	)
	if err != nil {
		return Execution{}, draftExecutionError("first actor decision: %v", err)
	}
	firstParticipantID, err := uuid.Parse(evidence.Result[0])
	if err != nil {
		return Execution{}, draftExecutionError("first actor decision result: %v", err)
	}
	secondParticipantID, err := uuid.Parse(evidence.Result[1])
	if err != nil {
		return Execution{}, draftExecutionError("second actor decision result: %v", err)
	}

	deadline := command.StartedAt.Add(TurnDuration)
	start := StartCommand{
		DraftID: command.DraftID, FirstParticipantID: firstParticipantID,
		SecondParticipantID: secondParticipantID, FirstDeadline: deadline,
	}
	var draft domain.Draft
	switch command.CategoryRevision.Format {
	case domain.SeriesFormatBO1:
		draft, err = NewBO1(command.CategoryRevision, start)
	case domain.SeriesFormatBO3:
		draft, err = NewBO3Final(command.CategoryRevision, start)
	default:
		err = domain.ErrValidation
	}
	if err != nil {
		return Execution{}, draftExecutionError("start policy: %v", err)
	}
	execution, err := draftExecutionFromDomain(
		draft,
		ExecutionStateActive,
		command.InitialRevisionID,
		uuid.Nil,
		1,
		command.CommandID,
		command.ServiceEpoch,
		evidence,
		nil,
	)
	if err != nil {
		return Execution{}, err
	}
	return execution, nil
}

func (u *ActionUseCase) Apply(
	ctx context.Context,
	command PlayerActionCommand,
) (ActionResult, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return ActionResult{}, domain.ErrValidation
	}
	if err := validateDraftPlayerActionCommand(command); err != nil {
		return ActionResult{}, err
	}
	if result, found, err := findRecordedDraftPlayerAction(ctx, u.repository, command); found || err != nil {
		return result, err
	}

	current, err := loadDraftExecution(ctx, u.repository, command.DraftID, "ActionUseCase")
	if err != nil {
		return ActionResult{}, err
	}
	if current.State == ExecutionStateCompleted || current.State == ExecutionStateSuperseded {
		if current.CommandID == command.CommandID {
			return reconcileDraftPlayerAction(current, command)
		}
		return ActionResult{}, domain.ErrDraftCompleted
	}
	if current.State != ExecutionStateActive {
		return ActionResult{}, ErrActionConflict
	}
	if !draftExpectationMatches(current, command.ExpectedRevisionID, command.ExpectedRevision, command.ExpectedServiceEpoch) {
		return reconcileDraftActionConflict(ctx, u.repository, command)
	}

	now := u.clock.Now()
	if !domain.IsValidServerTime(now) {
		return ActionResult{}, domain.ErrValidation
	}
	next, err := applyDraftAction(current, command, now, false, nil)
	if err != nil {
		return ActionResult{}, err
	}
	return commitDraftPlayerAction(ctx, u.repository, current, next, command)
}

func findRecordedDraftPlayerAction(
	ctx context.Context,
	repository Repository,
	command PlayerActionCommand,
) (ActionResult, bool, error) {
	recorded, err := repository.FindDraftCommand(ctx, command.DraftID, command.CommandID)
	if err != nil {
		return ActionResult{}, true, fmt.Errorf("ActionUseCase - find command: %w", err)
	}
	if recorded == nil {
		return ActionResult{}, false, nil
	}
	result, err := reconcileDraftPlayerAction(*recorded, command)
	return result, true, err
}

func loadDraftExecution(
	ctx context.Context,
	repository Repository,
	draftID uuid.UUID,
	owner string,
) (Execution, error) {
	current, err := repository.LoadDraft(ctx, draftID)
	if err != nil {
		return Execution{}, fmt.Errorf("%s - load draft: %w", owner, err)
	}
	if current == nil {
		return Execution{}, ErrNotFound
	}
	if err := current.Validate(); err != nil {
		return Execution{}, domain.ErrInternal
	}
	return cloneDraftExecution(*current), nil
}
