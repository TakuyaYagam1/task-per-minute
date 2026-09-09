package game

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

const pauseResumeAttempts = 2

var (
	ErrInvalidPauseResume      = errors.New("invalid pause resume")
	ErrPauseResumeConflict     = errors.New("pause resume conflict")
	ErrPauseResumeCommandReuse = errors.New("pause resume command reuse")
	ErrPauseResumeIncomplete   = errors.New("pause resume evidence incomplete")
	ErrPauseResumePresence     = errors.New("pause resume requires connected participants")
	ErrPauseResumeOverflow     = errors.New("pause resume revision overflow")
)

type PauseResumeExpectation struct {
	PauseID                 uuid.UUID
	GraphRevision           int64
	PauseRevision           int64
	Authority               authoritydomain.Identity
	TournamentState         domain.TournamentState
	TournamentRevision      int64
	WaveRevision            int64
	Series                  []PauseChildRevision
	Games                   []PauseChildRevision
	Draft                   *draftusecase.RevisionExpectation
	DraftPreviousRevisionID uuid.UUID
	Presence                []PausePresenceRevision
	Reconnect               []PauseChildRevision
	Counters                []PauseReconnectCounterRevision
	FrozenDeadlines         []PauseFrozenDeadlineRevision
	TerminalActionRevision  int64
}

type PauseResumeCommand struct {
	Scope                 pausedomain.GraphScope
	PauseID               uuid.UUID
	CommandID             uuid.UUID
	ActorID               uuid.UUID
	DraftResultRevisionID uuid.UUID
	Expected              PauseResumeExpectation
}

type PauseResumeAuthority struct {
	Pause                  NormalPauseRecord
	Presence               []pausedomain.PausePresence
	Reconnect              []pausedomain.PauseReconnectInterval
	Counters               []pausedomain.PauseReconnectCounter
	FrozenDeadlines        []PauseFrozenDeadline
	TerminalActionRevision int64
}

type PauseResumeRecord struct {
	Scope                 pausedomain.GraphScope
	PauseID               uuid.UUID
	CommandID             uuid.UUID
	ActorID               uuid.UUID
	DraftResultRevisionID uuid.UUID
	State                 PauseState
	Revision              int64
	Expected              PauseResumeExpectation
	Graph                 PauseGraph
	ResumedAt             time.Time
}

type PauseResumeUseCase struct {
	transactions TransactionManager
	repository   PauseResumeRepository
	clock        PauseClock
}

func NewPauseResumeUseCase(transactions TransactionManager, repository PauseResumeRepository, clock PauseClock) *PauseResumeUseCase {
	return &PauseResumeUseCase{transactions: transactions, repository: repository, clock: clock}
}

func (u *PauseResumeUseCase) Resume(ctx context.Context, command PauseResumeCommand) (*PauseResumeRecord, bool, error) {
	command.Expected = clonePauseResumeExpectation(command.Expected)
	if u == nil || u.transactions == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validatePauseResumeCommand(command); err != nil {
		return nil, false, err
	}
	for range pauseResumeAttempts {
		record, changed, retry, err := u.resumeAttempt(ctx, command)
		if retry {
			continue
		}
		return record, changed, err
	}
	return nil, false, ErrPauseResumeConflict
}

func (u *PauseResumeUseCase) resumeAttempt(ctx context.Context, command PauseResumeCommand) (*PauseResumeRecord, bool, bool, error) {
	var outcome pauseResumeAttemptOutcome
	err := u.transactions.Do(ctx, func(txCtx context.Context) error {
		var err error
		outcome, err = u.resumeLocked(txCtx, command)
		return err
	})
	if err != nil {
		return nil, false, false, fmt.Errorf("pause resume - transaction: %w", err)
	}
	return outcome.record, outcome.changed, outcome.retry, nil
}

type pauseResumeAttemptOutcome struct {
	record  *PauseResumeRecord
	changed bool
	retry   bool
}

func (u *PauseResumeUseCase) resumeLocked(ctx context.Context, command PauseResumeCommand) (pauseResumeAttemptOutcome, error) {
	recorded, err := u.findPauseResumeCommand(ctx, command, "find command")
	if err != nil || recorded != nil {
		return reconcilePauseResumeOutcome(recorded, command, err)
	}
	authority, err := u.repository.LoadPauseResumeAuthority(ctx, command.Scope, command.PauseID)
	if err != nil {
		return pauseResumeAttemptOutcome{}, fmt.Errorf("pause resume - load authority: %w", err)
	}
	recorded, err = u.findPauseResumeCommand(ctx, command, "find locked command")
	if err != nil || recorded != nil {
		return reconcilePauseResumeOutcome(recorded, command, err)
	}
	resumedAt := u.clock.Now().Round(0).UTC()
	if !pauseValidServerTime(resumedAt) {
		return pauseResumeAttemptOutcome{}, domain.ErrValidation
	}
	if err := validatePauseResumeAuthority(authority); err != nil {
		return pauseResumeAttemptOutcome{}, err
	}
	if authority.Pause.Scope != command.Scope || authority.Pause.PauseID != command.PauseID ||
		!pauseResumeExpectationEqual(PauseResumeExpectationFrom(authority), command.Expected) {
		return pauseResumeAttemptOutcome{}, ErrPauseResumeConflict
	}
	built, err := buildPauseResumeRecord(authority, command, resumedAt)
	if err != nil {
		return pauseResumeAttemptOutcome{}, err
	}
	return u.commitPauseResume(ctx, command, built)
}

func (u *PauseResumeUseCase) findPauseResumeCommand(ctx context.Context, command PauseResumeCommand, operation string) (*PauseResumeRecord, error) {
	recorded, err := u.repository.FindPauseResumeCommand(ctx, command.Scope.TournamentID, command.CommandID)
	if err != nil {
		return nil, fmt.Errorf("pause resume - %s: %w", operation, err)
	}
	return recorded, nil
}

func reconcilePauseResumeOutcome(recorded *PauseResumeRecord, command PauseResumeCommand, err error) (pauseResumeAttemptOutcome, error) {
	if err != nil {
		return pauseResumeAttemptOutcome{}, err
	}
	result, err := reconcilePauseResume(*recorded, command)
	return pauseResumeAttemptOutcome{record: result}, err
}

func (u *PauseResumeUseCase) commitPauseResume(ctx context.Context, command PauseResumeCommand, built PauseResumeRecord) (pauseResumeAttemptOutcome, error) {
	committed, changed, err := u.repository.CommitPauseResume(ctx, clonePauseResumeExpectation(command.Expected), built)
	if errors.Is(err, domain.ErrConflict) {
		return pauseResumeAttemptOutcome{retry: true}, nil
	}
	if err != nil {
		return pauseResumeAttemptOutcome{}, fmt.Errorf("pause resume - commit graph: %w", err)
	}
	if committed == nil {
		return pauseResumeAttemptOutcome{}, domain.ErrInternal
	}
	result, err := reconcilePauseResume(*committed, command)
	if err != nil || (changed && !reflect.DeepEqual(*result, built)) {
		return pauseResumeAttemptOutcome{}, domain.ErrInternal
	}
	return pauseResumeAttemptOutcome{record: result, changed: changed}, nil
}
