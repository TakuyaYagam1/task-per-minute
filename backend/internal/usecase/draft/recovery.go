package draft

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrPauseCutoff      = errors.New("draft pause cutoff reached")
	ErrRecoveryConflict = errors.New("draft recovery conflict")
	ErrRecoveryState    = errors.New("draft state does not allow recovery")
	ErrCorrectionCutoff = errors.New("draft correction cutoff reached")
)

type PauseCommand struct {
	DraftID              uuid.UUID
	ExpectedRevisionID   uuid.UUID
	ExpectedRevision     int64
	ExpectedServiceEpoch uuid.UUID
	CommandID            uuid.UUID
	ResultRevisionID     uuid.UUID
	ActorID              uuid.UUID
	Reason               string
}

type ResumeCommand struct {
	DraftID              uuid.UUID
	ExpectedRevisionID   uuid.UUID
	ExpectedRevision     int64
	ExpectedServiceEpoch uuid.UUID
	CommandID            uuid.UUID
	ResultRevisionID     uuid.UUID
	ActorID              uuid.UUID
	Reason               string
}

type EpochRecoveryCommand struct {
	DraftID              uuid.UUID
	ExpectedRevisionID   uuid.UUID
	ExpectedRevision     int64
	PreviousServiceEpoch uuid.UUID
	CurrentServiceEpoch  uuid.UUID
	RecoveryCommandID    uuid.UUID
	RecoveryRevisionID   uuid.UUID
	ResumeCommandID      uuid.UUID
	ResumeRevisionID     uuid.UUID
	RecoveryOwnerID      uuid.UUID
}

type SupersedeCommand struct {
	DraftID              uuid.UUID
	ExpectedRevisionID   uuid.UUID
	ExpectedRevision     int64
	ExpectedServiceEpoch uuid.UUID
	CommandID            uuid.UUID
	ResultRevisionID     uuid.UUID
	ActorID              uuid.UUID
	Reason               string
}

type RecoveryResult struct {
	Draft       Execution
	Changed     bool
	NextTimeout *TimeoutArm
}

type RecoveryUseCase struct {
	repository Repository
	clock      Clock
}

func NewRecoveryUseCase(repository Repository, clock Clock) *RecoveryUseCase {
	return &RecoveryUseCase{repository: repository, clock: clock}
}

func (u *RecoveryUseCase) available() bool {
	return u != nil && u.repository != nil && u.clock != nil
}

func (u *RecoveryUseCase) serverTime() (time.Time, error) {
	now := u.clock.Now()
	if !domain.IsValidServerTime(now) {
		return time.Time{}, domain.ErrValidation
	}
	return now, nil
}

func (u *RecoveryUseCase) loadExpectedDraft(
	ctx context.Context,
	draftID uuid.UUID,
	revisionID uuid.UUID,
	revision int64,
	serviceEpoch uuid.UUID,
) (Execution, error) {
	current, err := u.repository.LoadDraft(ctx, draftID)
	if err != nil {
		return Execution{}, fmt.Errorf("RecoveryUseCase - load draft: %w", err)
	}
	if current == nil {
		return Execution{}, ErrNotFound
	}
	if err := current.Validate(); err != nil {
		return Execution{}, domain.ErrInternal
	}
	if !draftExpectationMatches(*current, revisionID, revision, serviceEpoch) {
		return Execution{}, ErrRecoveryConflict
	}
	return cloneDraftExecution(*current), nil
}

func validateDraftPauseCommand(command PauseCommand) error {
	return validateDraftRecoveryMutation(
		command.DraftID,
		command.ExpectedRevisionID,
		command.ExpectedRevision,
		command.ExpectedServiceEpoch,
		command.CommandID,
		command.ResultRevisionID,
		command.ActorID,
		command.Reason,
	)
}

func validateDraftResumeCommand(command ResumeCommand) error {
	return validateDraftRecoveryMutation(
		command.DraftID,
		command.ExpectedRevisionID,
		command.ExpectedRevision,
		command.ExpectedServiceEpoch,
		command.CommandID,
		command.ResultRevisionID,
		command.ActorID,
		command.Reason,
	)
}

func validateDraftSupersedeCommand(command SupersedeCommand) error {
	return validateDraftRecoveryMutation(
		command.DraftID,
		command.ExpectedRevisionID,
		command.ExpectedRevision,
		command.ExpectedServiceEpoch,
		command.CommandID,
		command.ResultRevisionID,
		command.ActorID,
		command.Reason,
	)
}

func validateDraftRecoveryMutation(
	draftID uuid.UUID,
	expectedRevisionID uuid.UUID,
	expectedRevision int64,
	expectedServiceEpoch uuid.UUID,
	commandID uuid.UUID,
	resultRevisionID uuid.UUID,
	actorID uuid.UUID,
	reason string,
) error {
	ids := []uuid.UUID{
		draftID, expectedRevisionID, expectedServiceEpoch, commandID, resultRevisionID, actorID,
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
	if expectedRevision < 1 || reason == "" || reason != strings.TrimSpace(reason) {
		return domain.ErrValidation
	}
	return nil
}

func validateDraftEpochRecoveryCommand(command EpochRecoveryCommand) error {
	ids := []uuid.UUID{
		command.DraftID, command.ExpectedRevisionID, command.PreviousServiceEpoch,
		command.CurrentServiceEpoch, command.RecoveryCommandID, command.RecoveryRevisionID,
		command.ResumeCommandID, command.ResumeRevisionID, command.RecoveryOwnerID,
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
	if command.ExpectedRevision < 1 {
		return domain.ErrValidation
	}
	return nil
}

func advanceDraftRevision(
	draft *Execution,
	revisionID uuid.UUID,
	commandID uuid.UUID,
	serviceEpoch uuid.UUID,
) {
	draft.PreviousRevisionID = draft.RevisionID
	draft.RevisionID = revisionID
	draft.Revision++
	draft.CommandID = commandID
	draft.ServiceEpoch = serviceEpoch
}

func AdvanceRevision(
	draft *Execution,
	revisionID uuid.UUID,
	commandID uuid.UUID,
	serviceEpoch uuid.UUID,
) {
	advanceDraftRevision(draft, revisionID, commandID, serviceEpoch)
}
