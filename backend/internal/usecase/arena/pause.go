package arena

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
	ErrTournamentPauseNotConfirmed = errors.New("arena tournament technical pause is not confirmed")
	ErrTournamentPauseGraphPartial = errors.New("arena tournament pause graph is incomplete")
	ErrTournamentGoldenActive      = errors.New("arena Golden activity rejects a tournament-level pause")
)

type TournamentPauseUseCase struct {
	transactions TransactionManager
	repository   TournamentPauseRepository
	clock        Clock
}

func NewTournamentPauseUseCase(
	transactions TransactionManager,
	repository TournamentPauseRepository,
	clock Clock,
) *TournamentPauseUseCase {
	return &TournamentPauseUseCase{transactions: transactions, repository: repository, clock: clock}
}

func (u *TournamentPauseUseCase) EnterTechnicalPause(
	ctx context.Context,
	command TournamentTechnicalPauseCommand,
) (*TournamentTechnicalPauseRecord, bool, error) {
	if !u.isAvailable() || !validTournamentTechnicalPauseCommand(command) {
		return nil, false, domain.ErrValidation
	}
	if !command.Confirmed {
		return nil, false, ErrTournamentPauseNotConfirmed
	}
	current, err := u.repository.GetTournament(ctx, command.TournamentID)
	if err != nil {
		return nil, false, tournamentPauseLookupError("EnterTechnicalPause", err)
	}
	if err := validateTournamentRecordPointer(current, command.TournamentID); err != nil {
		return nil, false, err
	}
	if current.State == domain.ArenaTournamentStateTechnicalPause {
		return u.reconcileTechnicalPause(ctx, command)
	}
	if err := validateTechnicalPauseOrigin(current, command); err != nil {
		return nil, false, err
	}

	pausedAt := u.clock.Now()
	if !validArenaServerTime(pausedAt) {
		return nil, false, domain.ErrValidation
	}
	record, changed, err := u.enterTechnicalPauseTransaction(ctx, command, current.State, pausedAt)
	if err != nil {
		return nil, false, err
	}
	if !changed {
		return u.reconcileTechnicalPause(ctx, command)
	}
	if err := validateTechnicalPauseRecord(record, command, current.State); err != nil {
		return nil, false, err
	}
	return cloneTournamentTechnicalPauseRecord(*record), true, nil
}

func (u *TournamentPauseUseCase) isAvailable() bool {
	return u != nil && u.transactions != nil && u.repository != nil && u.clock != nil
}

func (u *TournamentPauseUseCase) reconcileTechnicalPause(
	ctx context.Context,
	command TournamentTechnicalPauseCommand,
) (*TournamentTechnicalPauseRecord, bool, error) {
	record, err := u.repository.GetTournamentTechnicalPause(ctx, command.TournamentID, command.CommandID)
	if err != nil {
		if errors.Is(err, ErrTournamentNotFound) {
			return nil, false, domain.ErrConflict
		}
		return nil, false, tournamentPauseLookupError("reconcile technical pause", err)
	}
	if record == nil || record.Tournament.PausedFromState == nil {
		return nil, false, domain.ErrInternal
	}
	if err := validateTechnicalPauseRecord(record, command, *record.Tournament.PausedFromState); err != nil {
		return nil, false, err
	}
	return cloneTournamentTechnicalPauseRecord(*record), false, nil
}

func validTournamentTechnicalPauseCommand(command TournamentTechnicalPauseCommand) bool {
	reason := strings.TrimSpace(command.Reason)
	return command.TournamentID != uuid.Nil && command.ExpectedRevision >= 1 && command.CommandID != uuid.Nil &&
		command.PauseID != uuid.Nil && command.ActorID != uuid.Nil && reason != "" &&
		len(reason) <= maxTournamentCommandReasonLength
}

func validateTechnicalPauseOrigin(
	current *TournamentRecord,
	command TournamentTechnicalPauseCommand,
) error {
	if current.Revision != command.ExpectedRevision {
		return domain.ErrConflict
	}
	next := domain.ArenaTournament{State: current.State, PausedFromState: current.PausedFromState}
	_, err := next.TransitionTo(domain.ArenaTournamentStateTechnicalPause)
	return err
}

func (u *TournamentPauseUseCase) enterTechnicalPauseTransaction(
	ctx context.Context,
	command TournamentTechnicalPauseCommand,
	expectedState domain.ArenaTournamentState,
	pausedAt time.Time,
) (*TournamentTechnicalPauseRecord, bool, error) {
	var record *TournamentTechnicalPauseRecord
	changed := false
	err := u.transactions.Do(ctx, func(txCtx context.Context) error {
		admission, err := u.loadPauseAdmission(txCtx, command.TournamentID)
		if err != nil {
			return err
		}
		record, changed, err = u.repository.EnterTournamentTechnicalPause(txCtx, TournamentTechnicalPauseInput{
			TournamentID: command.TournamentID, ExpectedRevision: command.ExpectedRevision,
			ExpectedState: expectedState, GraphRevision: admission.GraphRevision,
			CommandID: command.CommandID, PauseID: command.PauseID, ActorID: command.ActorID,
			Reason: strings.TrimSpace(command.Reason), PausedAt: pausedAt,
		})
		return err
	})
	if err != nil {
		return nil, false, tournamentPauseMutationError(err)
	}
	return record, changed, nil
}

func (u *TournamentPauseUseCase) loadPauseAdmission(
	ctx context.Context,
	tournamentID uuid.UUID,
) (*TournamentPauseAdmission, error) {
	admission, err := u.repository.InspectTournamentPauseAdmission(ctx, tournamentID)
	if err != nil {
		return nil, tournamentPauseLookupError("inspect pause admission", err)
	}
	if err := validateTournamentPauseAdmission(admission, tournamentID); err != nil {
		return nil, err
	}
	if admission.ActiveGolden {
		return nil, ErrTournamentGoldenActive
	}
	if !admission.Complete || admission.ExpectedChildren != admission.ObservedChildren {
		return nil, ErrTournamentPauseGraphPartial
	}
	return admission, nil
}

func validateTournamentPauseAdmission(admission *TournamentPauseAdmission, tournamentID uuid.UUID) error {
	if admission == nil || admission.TournamentID != tournamentID || admission.GraphRevision < 1 ||
		admission.ExpectedChildren < 0 || admission.ObservedChildren < 0 ||
		admission.ObservedChildren > admission.ExpectedChildren {
		return domain.ErrInternal
	}
	return nil
}

func validateTechnicalPauseRecord(
	record *TournamentTechnicalPauseRecord,
	command TournamentTechnicalPauseCommand,
	origin domain.ArenaTournamentState,
) error {
	if record == nil || record.CommandID != command.CommandID || record.PauseID != command.PauseID ||
		record.ActorID != command.ActorID || record.Reason != strings.TrimSpace(command.Reason) ||
		!validArenaServerTime(record.PausedAt) {
		return domain.ErrInternal
	}
	if err := validateTournamentRecordPointer(&record.Tournament, command.TournamentID); err != nil {
		return err
	}
	if record.Tournament.State != domain.ArenaTournamentStateTechnicalPause ||
		record.Tournament.Revision != command.ExpectedRevision+1 || record.Tournament.PausedFromState == nil ||
		*record.Tournament.PausedFromState != origin || !record.Tournament.UpdatedAt.Equal(record.PausedAt) {
		return domain.ErrInternal
	}
	return nil
}

func tournamentPauseLookupError(operation string, err error) error {
	if errors.Is(err, ErrTournamentNotFound) {
		return ErrTournamentNotFound
	}
	return fmt.Errorf("TournamentPauseUseCase - %s - repository lookup: %w", operation, err)
}

func tournamentPauseMutationError(err error) error {
	if errors.Is(err, domain.ErrConflict) || errors.Is(err, ErrTournamentNotFound) ||
		errors.Is(err, ErrTournamentPauseGraphPartial) || errors.Is(err, ErrTournamentGoldenActive) {
		return err
	}
	return fmt.Errorf("TournamentPauseUseCase - EnterTechnicalPause - repository mutation: %w", err)
}

func cloneTournamentTechnicalPauseRecord(record TournamentTechnicalPauseRecord) *TournamentTechnicalPauseRecord {
	cloned := record
	cloned.Tournament = *cloneArenaTournamentRecord(record.Tournament)
	return &cloned
}
