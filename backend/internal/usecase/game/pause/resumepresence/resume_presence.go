package resumepresence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
)

const pauseResumePresenceAttempts = 2

var (
	ErrInvalidPauseResumePresence      = errors.New("invalid pause resume presence")
	ErrPauseResumePresenceConflict     = errors.New("pause resume presence conflict")
	ErrPauseResumePresenceCommandReuse = errors.New("pause resume presence command reuse")
	ErrPauseResumePresenceIncomplete   = errors.New("pause resume presence evidence incomplete")
	ErrPauseResumePresenceIneligible   = errors.New("pause resume presence ineligible")
	ErrPauseResumePresenceOverflow     = errors.New("pause resume presence overflow")
)

type PauseResumeDecisionScopeKind string

const (
	PauseResumeDecisionScopeSeries      PauseResumeDecisionScopeKind = "series"
	PauseResumeDecisionScopeGameAttempt PauseResumeDecisionScopeKind = "game_attempt"
)

type PauseResumeDecisionAuthority struct {
	PauseID           uuid.UUID
	ScopeKind         PauseResumeDecisionScopeKind
	CurrentRevisionID uuid.UUID
	State             PauseState
	Revision          int64
	SeriesID          uuid.UUID
	GameID            uuid.UUID
	ParentPauseID     *uuid.UUID
	Depth             int
	DecisionNumber    int64
	StartedAt         time.Time
	GameClock         *pausedomain.PauseResumeGameClock
}

type PauseResumeDecisionExpectation struct {
	PauseID           uuid.UUID
	ScopeKind         PauseResumeDecisionScopeKind
	CurrentRevisionID uuid.UUID
	State             PauseState
	Revision          int64
	SeriesID          uuid.UUID
	GameID            uuid.UUID
	ParentPauseID     *uuid.UUID
	Depth             int
	DecisionNumber    int64
	StartedAt         time.Time
	GameClock         *pausedomain.PauseResumeGameClock
}

type PauseResumeDecisionRecord struct {
	ID                        uuid.UUID
	PauseID                   uuid.UUID
	DecisionNumber            int64
	Action                    PauseResumePresenceAction
	FirstReconnectIntervalID  *uuid.UUID
	SecondReconnectIntervalID *uuid.UUID
	DecidedAt                 time.Time
}

type PauseResumePresenceAuthority struct {
	Resume         PauseResumeAuthority
	SeriesDecision PauseResumeDecisionAuthority
	GameDecision   PauseResumeDecisionAuthority
}

type PauseResumePresenceExpectation struct {
	Resume          PauseResumeExpectation
	Series          PauseResumeDecisionExpectation
	Game            PauseResumeDecisionExpectation
	Presence        []pausedomain.PausePresence
	Reconnect       []pausedomain.PauseReconnectInterval
	Counters        []pausedomain.PauseReconnectCounter
	FrozenDeadlines []PauseFrozenDeadline
}

type PauseResumeIntervalInput struct {
	ParticipantID uuid.UUID
	IntervalID    uuid.UUID
	Window        time.Duration
}

type PauseResumePresenceCommand struct {
	Resume           PauseResumeCommand
	SeriesDecisionID uuid.UUID
	GameDecisionID   uuid.UUID
	SeriesExpected   PauseResumeDecisionExpectation
	GameExpected     PauseResumeDecisionExpectation
	Presence         []pausedomain.PausePresence
	Reconnect        []pausedomain.PauseReconnectInterval
	Counters         []pausedomain.PauseReconnectCounter
	FrozenDeadlines  []PauseFrozenDeadline
	FirstInterval    *PauseResumeIntervalInput
	SecondInterval   *PauseResumeIntervalInput
}

type PauseResumePresenceAction string

const (
	PauseResumeActionResume     PauseResumePresenceAction = "resume"
	PauseResumeActionWaitFirst  PauseResumePresenceAction = "wait_first"
	PauseResumeActionWaitSecond PauseResumePresenceAction = "wait_second"
	PauseResumeActionWaitBoth   PauseResumePresenceAction = "wait_both"
)

type PauseResumeParticipantDisposition string

const (
	PauseResumeParticipantConnected    PauseResumeParticipantDisposition = "connected"
	PauseResumeParticipantContinuation PauseResumeParticipantDisposition = "continuation"
	PauseResumeParticipantFresh        PauseResumeParticipantDisposition = "fresh"
)

type PauseResumeParticipantResolution struct {
	ParticipantID   uuid.UUID
	PresenceEpoch   int64
	Disposition     PauseResumeParticipantDisposition
	Counter         pausedomain.PauseReconnectCounter
	SourceInterval  *pausedomain.PauseReconnectInterval
	CurrentInterval *pausedomain.PauseReconnectInterval
}

type PauseResumePresenceRecord struct {
	Command               PauseResumePresenceCommand
	GameDecision          PauseResumeDecisionRecord
	SeriesDecision        *PauseResumeDecisionRecord
	GameClock             pausedomain.PauseResumeGameClock
	GamePauseState        PauseState
	SeriesPauseState      PauseState
	NormalPauseState      PauseState
	NormalPauseResolvedAt *time.Time
	Graph                 PauseGraph
	First                 PauseResumeParticipantResolution
	Second                PauseResumeParticipantResolution
	DecidedAt             time.Time
}

type PauseResumePresenceUseCase struct {
	transactions TransactionManager
	repository   PauseResumePresenceRepository
	clock        PauseClock
}

func NewPauseResumePresenceUseCase(transactions TransactionManager, repository PauseResumePresenceRepository, clock PauseClock) *PauseResumePresenceUseCase {
	return &PauseResumePresenceUseCase{transactions: transactions, repository: repository, clock: clock}
}

func (u *PauseResumePresenceUseCase) Resume(ctx context.Context, command PauseResumePresenceCommand) (*PauseResumePresenceRecord, bool, error) {
	command = clonePauseResumePresenceCommand(command)
	if u == nil || u.transactions == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validatePauseResumePresenceCommand(command); err != nil {
		return nil, false, err
	}
	for range pauseResumePresenceAttempts {
		record, changed, retry, err := u.resumeAttempt(ctx, command)
		if retry {
			continue
		}
		return record, changed, err
	}
	return nil, false, ErrPauseResumePresenceConflict
}

type pauseResumePresenceAttempt struct {
	record  *PauseResumePresenceRecord
	changed bool
	retry   bool
}

func (u *PauseResumePresenceUseCase) resumeAttempt(ctx context.Context, command PauseResumePresenceCommand) (*PauseResumePresenceRecord, bool, bool, error) {
	var outcome pauseResumePresenceAttempt
	err := u.transactions.Do(ctx, func(txCtx context.Context) error {
		var err error
		outcome, err = u.resumeLocked(txCtx, command)
		return err
	})
	if err != nil {
		return nil, false, false, fmt.Errorf("pause resume presence - transaction: %w", err)
	}
	return outcome.record, outcome.changed, outcome.retry, nil
}

func (u *PauseResumePresenceUseCase) resumeLocked(ctx context.Context, command PauseResumePresenceCommand) (pauseResumePresenceAttempt, error) {
	recorded, err := u.findCommand(ctx, command)
	if err != nil || recorded != nil {
		return reconcilePauseResumePresenceOutcome(recorded, command, err)
	}
	authority, err := u.repository.LoadPauseResumePresenceAuthority(ctx, command.Resume.Scope, command.Resume.PauseID, command.SeriesExpected.PauseID, command.GameExpected.PauseID)
	if err != nil {
		return pauseResumePresenceAttempt{}, fmt.Errorf("pause resume presence - load authority: %w", err)
	}
	recorded, err = u.findCommand(ctx, command)
	if err != nil || recorded != nil {
		return reconcilePauseResumePresenceOutcome(recorded, command, err)
	}
	decidedAt := u.clock.Now().Round(0).UTC()
	if !pauseValidServerTime(decidedAt) {
		return pauseResumePresenceAttempt{}, domain.ErrValidation
	}
	if err := validatePauseResumePresenceAuthority(authority); err != nil {
		return pauseResumePresenceAttempt{}, err
	}
	if !pauseResumePresenceAuthorityMatchesCommand(authority, command) {
		return pauseResumePresenceAttempt{}, ErrPauseResumePresenceConflict
	}
	built, err := buildPauseResumePresenceRecord(authority, command, decidedAt)
	if err != nil {
		return pauseResumePresenceAttempt{}, err
	}
	return u.commitPauseResumePresence(ctx, authority, command, built)
}

func pauseResumePresenceAuthorityMatchesCommand(authority PauseResumePresenceAuthority, command PauseResumePresenceCommand) bool {
	expected := PauseResumePresenceExpectation{
		Resume: clonePauseResumeExpectation(command.Resume.Expected), Series: clonePauseResumeDecisionExpectation(command.SeriesExpected),
		Game: clonePauseResumeDecisionExpectation(command.GameExpected), Presence: clonePausePresenceSlice(command.Presence),
		Reconnect: clonePauseReconnectSlice(command.Reconnect), Counters: clonePauseSlice(command.Counters),
		FrozenDeadlines: clonePauseFrozenDeadlineSlice(command.FrozenDeadlines),
	}
	return authority.Resume.Pause.Scope == command.Resume.Scope && authority.Resume.Pause.PauseID == command.Resume.PauseID &&
		pauseResumePresenceExpectationEqual(PauseResumePresenceExpectationFrom(authority), expected)
}

func (u *PauseResumePresenceUseCase) commitPauseResumePresence(
	ctx context.Context,
	authority PauseResumePresenceAuthority,
	command PauseResumePresenceCommand,
	built PauseResumePresenceRecord,
) (pauseResumePresenceAttempt, error) {
	canonical := clonePauseResumePresenceRecord(built)
	commitExpected := clonePauseResumePresenceExpectation(PauseResumePresenceExpectationFrom(authority))
	committed, changed, err := u.repository.CommitPauseResumePresence(ctx, commitExpected, clonePauseResumePresenceRecord(canonical))
	if errors.Is(err, domain.ErrConflict) {
		return pauseResumePresenceAttempt{retry: true}, nil
	}
	if err != nil {
		return pauseResumePresenceAttempt{}, fmt.Errorf("pause resume presence - commit: %w", err)
	}
	if committed == nil {
		return pauseResumePresenceAttempt{}, domain.ErrInternal
	}
	result, err := reconcilePauseResumePresence(*committed, command)
	if err != nil || (changed && !pauseResumePresenceRecordEqual(*result, canonical)) {
		return pauseResumePresenceAttempt{}, domain.ErrInternal
	}
	return pauseResumePresenceAttempt{record: result, changed: changed}, nil
}

func (u *PauseResumePresenceUseCase) findCommand(ctx context.Context, command PauseResumePresenceCommand) (*PauseResumePresenceRecord, error) {
	record, err := u.repository.FindPauseResumePresenceCommand(ctx, command.Resume.Scope.TournamentID, command.Resume.CommandID)
	if err != nil {
		return nil, fmt.Errorf("pause resume presence - find command: %w", err)
	}
	return record, nil
}

func reconcilePauseResumePresenceOutcome(recorded *PauseResumePresenceRecord, command PauseResumePresenceCommand, err error) (pauseResumePresenceAttempt, error) {
	if err != nil {
		return pauseResumePresenceAttempt{}, err
	}
	result, err := reconcilePauseResumePresence(*recorded, command)
	return pauseResumePresenceAttempt{record: result}, err
}

func PauseResumeDecisionExpectationFrom(authority PauseResumeDecisionAuthority) PauseResumeDecisionExpectation {
	return PauseResumeDecisionExpectation{
		PauseID: authority.PauseID, ScopeKind: authority.ScopeKind, CurrentRevisionID: authority.CurrentRevisionID,
		State: authority.State, Revision: authority.Revision, SeriesID: authority.SeriesID, GameID: authority.GameID,
		ParentPauseID: pauseCloneUUIDPointer(authority.ParentPauseID), Depth: authority.Depth, DecisionNumber: authority.DecisionNumber,
		StartedAt: authority.StartedAt, GameClock: clonePauseResumeGameClockPointer(authority.GameClock),
	}
}

func PauseResumePresenceExpectationFrom(authority PauseResumePresenceAuthority) PauseResumePresenceExpectation {
	return PauseResumePresenceExpectation{
		Resume:          PauseResumeExpectationFrom(authority.Resume),
		Series:          PauseResumeDecisionExpectationFrom(authority.SeriesDecision),
		Game:            PauseResumeDecisionExpectationFrom(authority.GameDecision),
		Presence:        clonePausePresenceSlice(authority.Resume.Presence),
		Reconnect:       clonePauseReconnectSlice(authority.Resume.Reconnect),
		Counters:        clonePauseSlice(authority.Resume.Counters),
		FrozenDeadlines: clonePauseFrozenDeadlineSlice(authority.Resume.FrozenDeadlines),
	}
}

func pauseResumePresenceError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidPauseResumePresence, fmt.Sprintf(format, arguments...))
}
