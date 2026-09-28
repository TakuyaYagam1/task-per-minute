package lifecycle

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const TournamentDeletionReason = "Tournament deleted by operator"

type TournamentDeletionCommand struct {
	CommandScope

	ExpectedRevision int64
	Confirmed        bool
}

type TournamentDeletionScope struct {
	TournamentID uuid.UUID
	RosterID     uuid.UUID
	State        domain.TournamentState
	Revision     int64
	UpdatedAt    time.Time
	FinishedAt   *time.Time
	DeletedAt    *time.Time
}

type TournamentDeletionInput struct {
	CommandID        uuid.UUID
	TournamentID     uuid.UUID
	ActorID          uuid.UUID
	ExpectedRevision int64
	SourceRevision   int64
	SourceState      domain.TournamentState
	Cancelled        bool
	Reason           string
	DeletedAt        time.Time
	CreatedAt        time.Time
}

type TournamentDeletionRecord struct {
	CommandID         uuid.UUID
	TournamentID      uuid.UUID
	ActorID           uuid.UUID
	SourceRevision    int64
	ResultingRevision int64
	SourceState       domain.TournamentState
	Cancelled         bool
	Reason            string
	DeletedAt         time.Time
	CreatedAt         time.Time
}

type tournamentDeletionTransition struct {
	ResultingRevision int64
	UpdatedAt         time.Time
	Cancelled         bool
}

type DeletionRepository interface {
	LockTournamentDeletionScope(ctx context.Context, tournamentID uuid.UUID) (TournamentDeletionScope, error)
	FindTournamentDeletion(ctx context.Context, tournamentID, commandID uuid.UUID) (*TournamentDeletionRecord, error)
	MarkTournamentDeleted(ctx context.Context, input TournamentDeletionInput) (*TournamentDeletionRecord, error)
}

func (w *LifecycleWorkflow) DeleteTournament(
	ctx context.Context,
	command TournamentDeletionCommand,
) (TournamentDeletionRecord, error) {
	if ctx == nil || !ValidTournamentDeletionCommand(command) || !w.deletionAvailable() {
		return TournamentDeletionRecord{}, domain.ErrValidation
	}

	var result TournamentDeletionRecord
	err := w.transactions.Do(ctx, func(txCtx context.Context) error {
		var err error
		result, err = w.deleteTournamentInTransaction(txCtx, command)
		return err
	})
	if err != nil {
		return TournamentDeletionRecord{}, err
	}
	return result, nil
}

func (w *LifecycleWorkflow) deleteTournamentInTransaction(
	ctx context.Context,
	command TournamentDeletionCommand,
) (TournamentDeletionRecord, error) {
	recorded, err := w.deletions.FindTournamentDeletion(ctx, command.TournamentID, command.CommandID)
	if err != nil {
		return TournamentDeletionRecord{}, err
	}
	if result, handled, replayErr := replayTournamentDeletion(recorded, command); handled {
		return result, replayErr
	}

	scope, err := w.deletions.LockTournamentDeletionScope(ctx, command.TournamentID)
	if err != nil {
		return TournamentDeletionRecord{}, err
	}
	// A concurrent request may have waited on the row lock after the first
	// request committed its receipt. Re-read the durable receipt while the
	// lock is held so the retry returns the same successful result.
	recorded, err = w.deletions.FindTournamentDeletion(ctx, command.TournamentID, command.CommandID)
	if err != nil {
		return TournamentDeletionRecord{}, err
	}
	if result, handled, replayErr := replayTournamentDeletion(recorded, command); handled {
		return result, replayErr
	}
	if err := validateTournamentDeletionScope(command, scope); err != nil {
		return TournamentDeletionRecord{}, err
	}

	transition, err := w.transitionForTournamentDeletion(ctx, command, scope)
	if err != nil {
		return TournamentDeletionRecord{}, err
	}
	return w.markTournamentDeleted(ctx, command, scope, transition)
}

func replayTournamentDeletion(
	recorded *TournamentDeletionRecord,
	command TournamentDeletionCommand,
) (TournamentDeletionRecord, bool, error) {
	if recorded == nil {
		return TournamentDeletionRecord{}, false, nil
	}
	if !deletionRecordMatches(*recorded, command) {
		return TournamentDeletionRecord{}, true, domain.ErrConflict
	}
	return *recorded, true, nil
}

func validateTournamentDeletionScope(
	command TournamentDeletionCommand,
	scope TournamentDeletionScope,
) error {
	if scope.DeletedAt != nil {
		return domain.ErrTournamentNotFound
	}
	if scope.Revision != command.ExpectedRevision {
		return deletionConflict(command, scope)
	}
	if scope.TournamentID != command.TournamentID || scope.RosterID == uuid.Nil ||
		!scope.State.IsValid() || scope.Revision < 1 ||
		!domain.IsValidServerTime(scope.UpdatedAt) {
		return domain.ErrInternal
	}
	return nil
}

func (w *LifecycleWorkflow) transitionForTournamentDeletion(
	ctx context.Context,
	command TournamentDeletionCommand,
	scope TournamentDeletionScope,
) (tournamentDeletionTransition, error) {
	transition := tournamentDeletionTransition{
		ResultingRevision: scope.Revision,
		UpdatedAt:         scope.UpdatedAt.UTC().Truncate(time.Microsecond),
	}
	if scope.State == domain.TournamentStateDraft || scope.State.IsTerminal() {
		return transition, nil
	}

	authority, err := w.repository.LockLifecycleAuthority(ctx, command.TournamentID)
	if err != nil {
		return tournamentDeletionTransition{}, err
	}
	if authority.Tournament.Revision != scope.Revision {
		return tournamentDeletionTransition{}, deletionConflict(command, scope)
	}
	cancellation, err := w.cancel(ctx, TournamentActionCommand{
		CommandScope:               command.CommandScope,
		ExpectedProjectionRevision: authority.ProjectionRevision,
		Action:                     TournamentActionCancel,
		Confirmed:                  true,
		Reason:                     TournamentDeletionReason,
	}, authority)
	if err != nil {
		return tournamentDeletionTransition{}, err
	}
	if err := validateLifecycleCommandRecord(cancellation); err != nil {
		return tournamentDeletionTransition{}, err
	}
	if err := w.repository.SaveLifecycleCommand(ctx, cancellation); err != nil {
		return tournamentDeletionTransition{}, err
	}
	return tournamentDeletionTransition{
		ResultingRevision: cancellation.Result.Revision,
		UpdatedAt:         cancellation.Result.UpdatedAt.UTC().Truncate(time.Microsecond),
		Cancelled:         true,
	}, nil
}

func (w *LifecycleWorkflow) markTournamentDeleted(
	ctx context.Context,
	command TournamentDeletionCommand,
	scope TournamentDeletionScope,
	transition tournamentDeletionTransition,
) (TournamentDeletionRecord, error) {
	deletedAt := w.clock.Now().Round(0).UTC().Truncate(time.Microsecond)
	if !domain.IsValidServerTime(deletedAt) || deletedAt.Before(transition.UpdatedAt) {
		return TournamentDeletionRecord{}, domain.ErrValidation
	}
	record, err := w.deletions.MarkTournamentDeleted(ctx, TournamentDeletionInput{
		CommandID: command.CommandID, TournamentID: command.TournamentID, ActorID: command.Operator.ActorID,
		ExpectedRevision: transition.ResultingRevision, SourceRevision: scope.Revision, SourceState: scope.State,
		Cancelled: transition.Cancelled, Reason: TournamentDeletionReason, DeletedAt: deletedAt, CreatedAt: deletedAt,
	})
	if err != nil {
		return TournamentDeletionRecord{}, err
	}
	if record == nil || !deletionRecordMatches(*record, command) {
		return TournamentDeletionRecord{}, domain.ErrInternal
	}
	return *record, nil
}

func (w *LifecycleWorkflow) deletionAvailable() bool {
	return w != nil && w.transactions != nil && w.repository != nil &&
		w.deletions != nil && w.cancellations != nil && w.clock != nil
}

func ValidTournamentDeletionCommand(command TournamentDeletionCommand) bool {
	return validCommandScope(command.CommandScope) && command.ExpectedRevision >= 1 && command.Confirmed
}

func deletionRecordMatches(record TournamentDeletionRecord, command TournamentDeletionCommand) bool {
	requiresCancellation := record.SourceState != domain.TournamentStateDraft && !record.SourceState.IsTerminal()
	return record.CommandID == command.CommandID && record.TournamentID == command.TournamentID &&
		record.ActorID == command.Operator.ActorID && record.SourceRevision == command.ExpectedRevision &&
		record.SourceState.IsValid() && record.Cancelled == requiresCancellation &&
		record.ResultingRevision == record.SourceRevision+1 &&
		record.Reason == TournamentDeletionReason && strings.TrimSpace(record.Reason) == record.Reason &&
		domain.IsValidServerTime(record.DeletedAt) && domain.IsValidServerTime(record.CreatedAt) &&
		!record.DeletedAt.After(record.CreatedAt)
}

func deletionConflict(command TournamentDeletionCommand, scope TournamentDeletionScope) error {
	return &RevisionConflictError{
		ExpectedRevision: command.ExpectedRevision,
		CurrentRevision:  scope.Revision,
		CurrentState:     scope.State,
		Detail:           "tournament revision conflict",
	}
}

var _ DeletionPort = (*LifecycleWorkflow)(nil)
