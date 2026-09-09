package cancellation

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const cancellationMaxTournamentCommandReasonLength = 512

var ErrTournamentCancellationNotConfirmed = errors.New("tournament cancellation is not confirmed")

type TournamentCancellationUseCase struct {
	repository TournamentCancellationRepository
	clock      CancellationClock
}

func NewTournamentCancellationUseCase(
	repository TournamentCancellationRepository,
	clock CancellationClock,
) *TournamentCancellationUseCase {
	return &TournamentCancellationUseCase{repository: repository, clock: clock}
}

func (u *TournamentCancellationUseCase) Cancel(
	ctx context.Context,
	command TournamentCancellationCommand,
) (*TournamentCancellationRecord, bool, error) {
	if !u.isAvailable() || !validTournamentCancellationCommand(command) {
		return nil, false, domain.ErrValidation
	}
	if !command.Confirmed {
		return nil, false, ErrTournamentCancellationNotConfirmed
	}
	current, err := u.repository.GetTournament(ctx, command.TournamentID)
	if err != nil {
		return nil, false, tournamentCancellationLookupError("Cancel", err)
	}
	if err := cancellationValidateTournamentRecordPointer(current, command.TournamentID); err != nil {
		return nil, false, err
	}
	if current.State == domain.TournamentStateCancelled {
		return u.reconcileCancellation(ctx, command)
	}
	if current.Revision != command.ExpectedRevision {
		return nil, false, domain.ErrConflict
	}
	next := domain.Tournament{State: current.State, PausedFromState: current.PausedFromState}
	if _, err := next.TransitionTo(domain.TournamentStateCancelled); err != nil {
		return nil, false, err
	}
	cancelledAt := u.clock.Now()
	if !cancellationValidServerTime(cancelledAt) {
		return nil, false, domain.ErrValidation
	}
	reason := strings.TrimSpace(command.Reason)
	record, changed, err := u.repository.CancelTournament(ctx, TournamentCancellationInput{
		TournamentID: command.TournamentID, ExpectedRevision: command.ExpectedRevision,
		ExpectedState: current.State, CommandID: command.CommandID, ActorID: command.ActorID,
		Reason: reason, CancelledAt: cancelledAt,
	})
	if err != nil {
		return nil, false, tournamentCancellationMutationError(err)
	}
	if !changed {
		return u.reconcileCancellation(ctx, command)
	}
	if err := validateCancellationRecord(record, command); err != nil {
		return nil, false, err
	}
	return cloneTournamentCancellationRecord(*record), true, nil
}

func (u *TournamentCancellationUseCase) isAvailable() bool {
	return u != nil && u.repository != nil && u.clock != nil
}

func (u *TournamentCancellationUseCase) reconcileCancellation(
	ctx context.Context,
	command TournamentCancellationCommand,
) (*TournamentCancellationRecord, bool, error) {
	record, err := u.repository.GetTournamentCancellation(ctx, command.TournamentID, command.CommandID)
	if err != nil {
		if errors.Is(err, CancellationErrTournamentNotFound) {
			return nil, false, domain.ErrConflict
		}
		return nil, false, tournamentCancellationLookupError("reconcile cancellation", err)
	}
	if err := validateCancellationRecord(record, command); err != nil {
		return nil, false, err
	}
	return cloneTournamentCancellationRecord(*record), false, nil
}

func validTournamentCancellationCommand(command TournamentCancellationCommand) bool {
	reason := strings.TrimSpace(command.Reason)
	return command.TournamentID != uuid.Nil && command.ExpectedRevision >= 1 && command.CommandID != uuid.Nil &&
		command.ActorID != uuid.Nil && reason != "" && len(reason) <= cancellationMaxTournamentCommandReasonLength
}

func validateCancellationRecord(
	record *TournamentCancellationRecord,
	command TournamentCancellationCommand,
) error {
	if !validCancellationEvidence(record, command) {
		return domain.ErrInternal
	}
	if err := cancellationValidateTournamentRecordPointer(&record.Tournament, command.TournamentID); err != nil {
		return err
	}
	if !validCancelledTournamentRecord(record, command) {
		return domain.ErrInternal
	}
	return nil
}

func validCancellationEvidence(
	record *TournamentCancellationRecord,
	command TournamentCancellationCommand,
) bool {
	if record == nil {
		return false
	}
	return record.CommandID == command.CommandID && record.ActorID == command.ActorID &&
		record.Reason == strings.TrimSpace(command.Reason) && record.AuditEventID != uuid.Nil &&
		record.OutboxEventID != uuid.Nil && record.AuditEventID != record.OutboxEventID &&
		cancellationValidServerTime(record.CancelledAt) && record.ChampionID == nil
}

func validCancelledTournamentRecord(
	record *TournamentCancellationRecord,
	command TournamentCancellationCommand,
) bool {
	return record.Tournament.State == domain.TournamentStateCancelled &&
		record.Tournament.Revision == command.ExpectedRevision+1 && record.Tournament.PausedFromState == nil &&
		record.Tournament.FinishedAt != nil && record.Tournament.FinishedAt.Equal(record.CancelledAt) &&
		record.Tournament.UpdatedAt.Equal(record.CancelledAt)
}

func tournamentCancellationLookupError(operation string, err error) error {
	if errors.Is(err, CancellationErrTournamentNotFound) {
		return CancellationErrTournamentNotFound
	}
	return fmt.Errorf("TournamentCancellationUseCase - %s - repository lookup: %w", operation, err)
}

func tournamentCancellationMutationError(err error) error {
	if errors.Is(err, domain.ErrConflict) || errors.Is(err, CancellationErrTournamentNotFound) {
		return err
	}
	return fmt.Errorf("TournamentCancellationUseCase - Cancel - repository mutation: %w", err)
}

func cloneTournamentCancellationRecord(record TournamentCancellationRecord) *TournamentCancellationRecord {
	cloned := record
	cloned.Tournament = *cancellationCloneTournamentRecord(record.Tournament)
	if record.ChampionID != nil {
		championID := *record.ChampionID
		cloned.ChampionID = &championID
	}
	return &cloned
}
