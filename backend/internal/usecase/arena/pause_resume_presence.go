package arena

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
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

type PauseResumeGameClock struct {
	PauseID          uuid.UUID
	GameID           uuid.UUID
	OriginalDeadline time.Time
	FrozenAt         time.Time
	Remaining        time.Duration
	ResumedAt        *time.Time
	ResumedDeadline  *time.Time
	Revision         int64
}

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
	GameClock         *PauseResumeGameClock
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
	GameClock         *PauseResumeGameClock
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
	Presence        []PausePresence
	Reconnect       []PauseReconnectInterval
	Counters        []PauseReconnectCounter
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
	Presence         []PausePresence
	Reconnect        []PauseReconnectInterval
	Counters         []PauseReconnectCounter
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
	Counter         PauseReconnectCounter
	SourceInterval  *PauseReconnectInterval
	CurrentInterval *PauseReconnectInterval
}

type PauseResumePresenceRecord struct {
	Command               PauseResumePresenceCommand
	GameDecision          PauseResumeDecisionRecord
	SeriesDecision        *PauseResumeDecisionRecord
	GameClock             PauseResumeGameClock
	GamePauseState        PauseState
	SeriesPauseState      PauseState
	NormalPauseState      PauseState
	NormalPauseResolvedAt *time.Time
	Graph                 PauseGraph
	First                 PauseResumeParticipantResolution
	Second                PauseResumeParticipantResolution
	DecidedAt             time.Time
}

// PauseResumePresenceRepository commits the normal Wave pause, old Series/Game
// pause heads, Game clock, decision rows and reconnect rows in one transaction.
type PauseResumePresenceRepository interface {
	FindPauseResumePresenceCommand(ctx context.Context, tournamentID, commandID uuid.UUID) (*PauseResumePresenceRecord, error)
	LoadPauseResumePresenceAuthority(ctx context.Context, scope PauseGraphScope, normalPauseID, seriesPauseID, gamePauseID uuid.UUID) (PauseResumePresenceAuthority, error)
	CommitPauseResumePresence(ctx context.Context, expected PauseResumePresenceExpectation, record PauseResumePresenceRecord) (*PauseResumePresenceRecord, bool, error)
}

type PauseResumePresenceUseCase struct {
	transactions TransactionManager
	repository   PauseResumePresenceRepository
	clock        Clock
}

func NewPauseResumePresenceUseCase(transactions TransactionManager, repository PauseResumePresenceRepository, clock Clock) *PauseResumePresenceUseCase {
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
	if !validArenaServerTime(decidedAt) {
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
		ParentPauseID: cloneUUIDPointer(authority.ParentPauseID), Depth: authority.Depth, DecisionNumber: authority.DecisionNumber,
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

func addPauseResumeTime(at time.Time, duration time.Duration) (time.Time, error) {
	deadline, ok := safePauseTimeAdd(at, duration)
	if !ok {
		return time.Time{}, ErrPauseResumePresenceOverflow
	}
	return deadline, nil
}

func validatePauseResumePresenceCommand(command PauseResumePresenceCommand) error {
	if err := validatePauseResumeCommand(command.Resume); err != nil {
		return pauseResumePresenceError("invalid normal resume command: %v", err)
	}
	if !validPauseResumeDecisionIDs(command) {
		return pauseResumePresenceError("invalid decision identity")
	}
	if err := validatePauseResumeDecisionExpectation(command.SeriesExpected, PauseResumeDecisionScopeSeries); err != nil {
		return err
	}
	if err := validatePauseResumeDecisionExpectation(command.GameExpected, PauseResumeDecisionScopeGameAttempt); err != nil {
		return err
	}
	if !validPauseResumeCommandTopology(command) {
		return pauseResumePresenceError("invalid pause topology")
	}
	if err := validatePauseResumeCommandBaselines(command); err != nil {
		return err
	}
	return validatePauseResumeCommandIntervals(command)
}

func validPauseResumeDecisionIDs(command PauseResumePresenceCommand) bool {
	reservedDecisionIDs := map[uuid.UUID]struct{}{
		uuid.Nil: {}, command.Resume.PauseID: {}, command.Resume.CommandID: {}, command.Resume.ActorID: {},
		command.SeriesExpected.PauseID: {}, command.GameExpected.PauseID: {},
		command.SeriesExpected.CurrentRevisionID: {}, command.GameExpected.CurrentRevisionID: {},
	}
	if _, reserved := reservedDecisionIDs[command.SeriesDecisionID]; reserved {
		return false
	}
	if _, reserved := reservedDecisionIDs[command.GameDecisionID]; reserved || command.GameDecisionID == command.SeriesDecisionID {
		return false
	}
	return true
}

func validPauseResumeCommandTopology(command PauseResumePresenceCommand) bool {
	return command.SeriesExpected.ParentPauseID == nil && command.SeriesExpected.Depth == 0 &&
		command.GameExpected.ParentPauseID != nil && *command.GameExpected.ParentPauseID == command.SeriesExpected.PauseID &&
		command.GameExpected.Depth == 1 && command.SeriesExpected.SeriesID == command.GameExpected.SeriesID &&
		command.Resume.PauseID != command.SeriesExpected.PauseID && command.Resume.PauseID != command.GameExpected.PauseID
}

func validatePauseResumeCommandBaselines(command PauseResumePresenceCommand) error {
	if !validPauseResumeFrozenBaseline(command.FrozenDeadlines) {
		return pauseResumePresenceError("invalid frozen deadline baseline")
	}
	if !validPauseResumePresenceBaseline(command.Presence) {
		return pauseResumePresenceError("invalid Presence baseline")
	}
	if !validPauseResumeReconnectBaseline(command.Reconnect) {
		return pauseResumePresenceError("invalid Reconnect baseline")
	}
	if !validPauseResumeCounterBaseline(command.Counters) {
		return pauseResumePresenceError("invalid counter baseline")
	}
	return nil
}

func validatePauseResumeCommandIntervals(command PauseResumePresenceCommand) error {
	if err := validatePauseResumeIntervalInput(command.FirstInterval); err != nil {
		return err
	}
	if err := validatePauseResumeIntervalInput(command.SecondInterval); err != nil {
		return err
	}
	if command.FirstInterval != nil && command.SecondInterval != nil &&
		command.FirstInterval.IntervalID == command.SecondInterval.IntervalID {
		return pauseResumePresenceError("duplicate interval identity")
	}
	return nil
}

func validatePauseResumeIntervalInput(input *PauseResumeIntervalInput) error {
	if input == nil {
		return nil
	}
	if input.ParticipantID == uuid.Nil || input.IntervalID == uuid.Nil || input.Window < 0 {
		return pauseResumePresenceError("invalid interval input")
	}
	return nil
}

func validatePauseResumeDecisionExpectation(value PauseResumeDecisionExpectation, scope PauseResumeDecisionScopeKind) error {
	if !validPauseResumeDecisionExpectationHeader(value, scope) {
		return pauseResumePresenceError("invalid decision expectation")
	}
	switch scope {
	case PauseResumeDecisionScopeSeries:
		if !validPauseResumeSeriesExpectation(value) {
			return pauseResumePresenceError("invalid Series expectation")
		}
	case PauseResumeDecisionScopeGameAttempt:
		if !validPauseResumeGameExpectation(value) {
			return pauseResumePresenceError("invalid Game expectation")
		}
	default:
		return pauseResumePresenceError("invalid decision scope")
	}
	return nil
}

func validPauseResumeDecisionExpectationHeader(value PauseResumeDecisionExpectation, scope PauseResumeDecisionScopeKind) bool {
	return value.PauseID != uuid.Nil && value.ScopeKind == scope && value.CurrentRevisionID != uuid.Nil &&
		value.State == PauseStateActive && value.Revision >= 1 && value.SeriesID != uuid.Nil &&
		value.DecisionNumber >= 0 && !value.StartedAt.IsZero()
}

func validPauseResumeSeriesExpectation(value PauseResumeDecisionExpectation) bool {
	return value.GameID == uuid.Nil && value.ParentPauseID == nil && value.Depth == 0 && value.GameClock == nil
}

func validPauseResumeGameExpectation(value PauseResumeDecisionExpectation) bool {
	return value.GameID != uuid.Nil && value.ParentPauseID != nil && value.Depth == 1 && value.GameClock != nil &&
		validatePauseResumeGameClockExpectation(*value.GameClock) == nil && value.GameClock.PauseID == value.PauseID &&
		value.GameClock.GameID == value.GameID
}

func validatePauseResumePresenceAuthority(authority PauseResumePresenceAuthority) error {
	if err := validatePauseResumePresenceOwnership(authority); err != nil {
		return err
	}
	if pauseResumePresenceHasExhaustedFreshAbsence(authority) {
		return ErrPauseResumePresenceIneligible
	}
	resume := pauseResumeAuthorityWithOrderedCounters(authority.Resume)
	if err := validatePauseResumeAuthority(resume); err != nil {
		if errors.Is(err, ErrPauseResumeIncomplete) {
			return ErrPauseResumePresenceIncomplete
		}
		return pauseResumePresenceError("invalid normal pause authority: %v", err)
	}
	normal := authority.Resume.Pause
	if !validPauseResumeNormalAuthority(normal) {
		return pauseResumePresenceError("normal pause is not an active Wave pause")
	}
	if err := validatePauseResumeDecisionAuthority(authority.SeriesDecision, PauseResumeDecisionScopeSeries); err != nil {
		return err
	}
	if err := validatePauseResumeDecisionAuthority(authority.GameDecision, PauseResumeDecisionScopeGameAttempt); err != nil {
		return err
	}
	series := authority.SeriesDecision
	game := authority.GameDecision
	if !validPauseResumeIndependentTopology(normal, series, game) {
		return pauseResumePresenceError("invalid independent pause topology")
	}
	graphSeries := pauseSeriesByID(normal.Graph.Series, series.SeriesID)
	graphGame := pauseGameByID(normal.Graph.Games, game.GameID)
	if graphSeries == nil || graphGame == nil || graphGame.SeriesID != series.SeriesID {
		return ErrPauseResumePresenceConflict
	}
	return nil
}

func validPauseResumeNormalAuthority(value NormalPauseRecord) bool {
	return value.ScopeKind == PauseScopeWave && value.ScopeID == value.Scope.WaveID && value.State == PauseStateActive
}

func validPauseResumeIndependentTopology(
	normal NormalPauseRecord,
	series PauseResumeDecisionAuthority,
	game PauseResumeDecisionAuthority,
) bool {
	return series.ParentPauseID == nil && series.Depth == 0 && game.ParentPauseID != nil &&
		*game.ParentPauseID == series.PauseID && game.Depth == 1 && series.SeriesID == game.SeriesID &&
		series.StartedAt.Before(normal.PausedAt) && game.StartedAt.Before(normal.PausedAt) &&
		series.PauseID != normal.PauseID && game.PauseID != normal.PauseID
}

func validatePauseResumePresenceOwnership(authority PauseResumePresenceAuthority) error {
	for _, counter := range authority.Resume.Counters {
		if counter.PauseID != authority.GameDecision.PauseID {
			return pauseResumePresenceError("counter belongs to another pause")
		}
	}
	for _, interval := range authority.Resume.Reconnect {
		if interval.PauseID != authority.GameDecision.PauseID {
			return pauseResumePresenceError("Reconnect belongs to another pause")
		}
	}
	return nil
}

func pauseResumePresenceHasExhaustedFreshAbsence(authority PauseResumePresenceAuthority) bool {
	for _, live := range authority.Resume.Presence {
		if pauseResumePresenceFreshSlotExhausted(authority, live) {
			return true
		}
	}
	return false
}

func pauseResumePresenceFreshSlotExhausted(authority PauseResumePresenceAuthority, live PausePresence) bool {
	snapshot := pausePresenceByParticipant(authority.Resume.Pause.Graph.Presence, live.ParticipantID)
	counter := pauseCounterByParticipant(authority.Resume.Counters, authority.GameDecision.PauseID, live.ParticipantID)
	snapshotCounter := pauseCounterByParticipant(authority.Resume.Pause.Graph.Counters, authority.GameDecision.PauseID, live.ParticipantID)
	if snapshot == nil || counter == nil || snapshotCounter == nil || validatePausePresence(live) != nil {
		return false
	}
	return pauseResumeFreshPresenceMatches(*snapshot, live, authority.Resume.Pause.PausedAt) &&
		pauseResumeFreshCounterExhausted(*counter, *snapshotCounter)
}

func pauseResumeFreshPresenceMatches(snapshot, live PausePresence, pausedAt time.Time) bool {
	return samePausePresenceIdentity(snapshot, live) && live.State == PresenceStateDisconnected &&
		live.PresenceEpoch > snapshot.PresenceEpoch &&
		live.PresenceEpoch-snapshot.PresenceEpoch == live.Revision-snapshot.Revision && !live.UpdatedAt.Before(pausedAt)
}

func pauseResumeFreshCounterExhausted(counter, snapshot PauseReconnectCounter) bool {
	return counter.PauseID == snapshot.PauseID && counter.RosterID == snapshot.RosterID &&
		counter.ParticipantID == snapshot.ParticipantID && counter.Used == snapshot.Used &&
		counter.Revision == snapshot.Revision && counter.Limit >= 0 && counter.Used == counter.Limit
}

func pauseResumeAuthorityWithOrderedCounters(value PauseResumeAuthority) PauseResumeAuthority {
	clone := value
	if len(clone.Counters) != len(clone.Pause.Graph.Counters) {
		return clone
	}
	ordered := make([]PauseReconnectCounter, 0, len(clone.Counters))
	for _, snapshot := range clone.Pause.Graph.Counters {
		counter := pauseCounterByParticipant(clone.Counters, snapshot.PauseID, snapshot.ParticipantID)
		if counter == nil {
			return clone
		}
		ordered = append(ordered, *counter)
	}
	clone.Counters = ordered
	return clone
}

func validatePauseResumeDecisionAuthority(value PauseResumeDecisionAuthority, scope PauseResumeDecisionScopeKind) error {
	expected := PauseResumeDecisionExpectationFrom(value)
	if err := validatePauseResumeDecisionExpectation(expected, scope); err != nil {
		return err
	}
	if scope == PauseResumeDecisionScopeGameAttempt && validatePauseResumeGameClock(*value.GameClock, true) != nil {
		return pauseResumePresenceError("invalid Game authority clock")
	}
	return nil
}

func validatePauseResumeGameClockExpectation(value PauseResumeGameClock) error {
	if value.PauseID == uuid.Nil || value.GameID == uuid.Nil || value.Remaining <= 0 || value.Revision < 1 ||
		value.OriginalDeadline.IsZero() || value.FrozenAt.IsZero() ||
		value.ResumedAt != nil || value.ResumedDeadline != nil {
		return pauseResumePresenceError("invalid Game clock expectation")
	}
	return nil
}

func validatePauseResumeGameClock(value PauseResumeGameClock, active bool) error {
	if !validPauseResumeGameClockHeader(value) {
		return pauseResumePresenceError("invalid Game clock")
	}
	if active {
		if value.ResumedAt != nil || value.ResumedDeadline != nil {
			return pauseResumePresenceError("active Game clock is resumed")
		}
		return nil
	}
	if !validResumedPauseResumeGameClock(value) {
		return pauseResumePresenceError("resolved Game clock lacks shifted deadline")
	}
	return nil
}

func validPauseResumeGameClockHeader(value PauseResumeGameClock) bool {
	return value.PauseID != uuid.Nil && value.GameID != uuid.Nil && value.Remaining > 0 && value.Revision >= 1 &&
		!value.OriginalDeadline.IsZero() && !value.FrozenAt.IsZero() && value.OriginalDeadline.After(value.FrozenAt) &&
		value.OriginalDeadline.Sub(value.FrozenAt) == value.Remaining
}

func validResumedPauseResumeGameClock(value PauseResumeGameClock) bool {
	return value.ResumedAt != nil && value.ResumedDeadline != nil && !value.ResumedAt.IsZero() &&
		!value.ResumedDeadline.IsZero() && value.ResumedDeadline.Sub(*value.ResumedAt) == value.Remaining
}

func pauseSeriesByID(values []PauseSeries, id uuid.UUID) *PauseSeries {
	for index := range values {
		if values[index].Execution.Series.ID == id {
			return &values[index]
		}
	}
	return nil
}

func pauseResumePresenceExpectationEqual(first, second PauseResumePresenceExpectation) bool {
	return pauseResumeExpectationEqual(first.Resume, second.Resume) && pauseResumeDecisionExpectationEqual(first.Series, second.Series) &&
		pauseResumeDecisionExpectationEqual(first.Game, second.Game) && pauseResumePresenceSetEqual(first.Presence, second.Presence) &&
		pauseResumeReconnectSetEqual(first.Reconnect, second.Reconnect) && pauseResumeCounterSetEqual(first.Counters, second.Counters) &&
		pauseResumeFrozenBaselineEqual(first.FrozenDeadlines, second.FrozenDeadlines)
}

func clonePauseResumePresenceExpectation(value PauseResumePresenceExpectation) PauseResumePresenceExpectation {
	return PauseResumePresenceExpectation{
		Resume:          clonePauseResumeExpectationPreservingSlices(value.Resume),
		Series:          clonePauseResumeDecisionExpectation(value.Series),
		Game:            clonePauseResumeDecisionExpectation(value.Game),
		Presence:        clonePausePresenceSlice(value.Presence),
		Reconnect:       clonePauseReconnectSlice(value.Reconnect),
		Counters:        clonePauseSlice(value.Counters),
		FrozenDeadlines: clonePauseFrozenDeadlineSlice(value.FrozenDeadlines),
	}
}

func clonePauseResumeExpectationPreservingSlices(value PauseResumeExpectation) PauseResumeExpectation {
	clone := value
	clone.Games = clonePauseSlice(value.Games)
	clone.Series = clonePauseSlice(value.Series)
	clone.Presence = clonePauseSlice(value.Presence)
	clone.Reconnect = clonePauseSlice(value.Reconnect)
	clone.Counters = clonePauseSlice(value.Counters)
	clone.FrozenDeadlines = clonePauseSlice(value.FrozenDeadlines)
	if value.Draft != nil {
		draft := *value.Draft
		clone.Draft = &draft
	}
	return clone
}

func validPauseResumeFrozenBaseline(values []PauseFrozenDeadline) bool {
	seen := make(map[pauseDeadlineIdentity]struct{}, len(values))
	for _, value := range values {
		key := pauseDeadlineIdentity{Kind: value.Kind, OwnerID: value.OwnerID}
		if validateFrozenDeadline(pauseResumeCanonicalFrozenDeadline(value), true) != nil {
			return false
		}
		if _, duplicate := seen[key]; duplicate {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}

func validPauseResumePresenceBaseline(values []PausePresence) bool {
	seen := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		if validatePausePresence(pauseResumeCanonicalPresence(value)) != nil {
			return false
		}
		if _, duplicate := seen[value.ParticipantID]; duplicate {
			return false
		}
		seen[value.ParticipantID] = struct{}{}
	}
	return true
}

func validPauseResumeReconnectBaseline(values []PauseReconnectInterval) bool {
	seen := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		if validatePauseReconnect(pauseResumeCanonicalReconnect(value)) != nil {
			return false
		}
		if _, duplicate := seen[value.ID]; duplicate {
			return false
		}
		seen[value.ID] = struct{}{}
	}
	return true
}

func validPauseResumeCounterBaseline(values []PauseReconnectCounter) bool {
	seen := make(map[[3]uuid.UUID]struct{}, len(values))
	for _, value := range values {
		key := [3]uuid.UUID{value.PauseID, value.RosterID, value.ParticipantID}
		if value.PauseID == uuid.Nil || value.RosterID == uuid.Nil || value.ParticipantID == uuid.Nil ||
			value.Limit < 0 || value.Used < 0 || value.Used > value.Limit || value.Revision < 1 {
			return false
		}
		if _, duplicate := seen[key]; duplicate {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}

func pauseResumeBaselineProjectionsMatch(command PauseResumePresenceCommand) bool {
	presence := make([]PausePresenceRevision, len(command.Presence))
	for index, value := range command.Presence {
		presence[index] = PausePresenceRevision{
			ID: value.ID, TournamentID: value.TournamentID, RosterID: value.RosterID, SeriesID: value.SeriesID,
			ParticipantID: value.ParticipantID, PresenceEpoch: value.PresenceEpoch, Revision: value.Revision,
		}
	}
	reconnect := make([]PauseChildRevision, len(command.Reconnect))
	for index, value := range command.Reconnect {
		reconnect[index] = PauseChildRevision{ID: value.ID, Revision: value.Revision}
	}
	counters := make([]PauseReconnectCounterRevision, len(command.Counters))
	for index, value := range command.Counters {
		counters[index] = PauseReconnectCounterRevision{
			PauseID: value.PauseID, RosterID: value.RosterID, ParticipantID: value.ParticipantID, Revision: value.Revision,
		}
	}
	frozen := make([]PauseFrozenDeadlineRevision, len(command.FrozenDeadlines))
	for index, value := range command.FrozenDeadlines {
		frozen[index] = PauseFrozenDeadlineRevision{Kind: value.Kind, OwnerID: value.OwnerID, Revision: value.Revision}
	}
	expected := command.Resume.Expected
	return presenceRevisionMapEqual(presence, expected.Presence) && revisionMapEqual(reconnect, expected.Reconnect) &&
		counterRevisionMapEqual(counters, expected.Counters) && frozenRevisionMapEqual(frozen, expected.FrozenDeadlines)
}

func pauseResumeFrozenBaselineEqual(first, second []PauseFrozenDeadline) bool {
	if len(first) != len(second) {
		return false
	}
	values := make(map[pauseDeadlineIdentity]PauseFrozenDeadline, len(first))
	for _, value := range first {
		values[pauseDeadlineIdentity{Kind: value.Kind, OwnerID: value.OwnerID}] = value
	}
	for _, value := range second {
		if !pauseResumeFrozenDeadlineEqual(values[pauseDeadlineIdentity{Kind: value.Kind, OwnerID: value.OwnerID}], value) {
			return false
		}
	}
	return true
}

func pauseResumePresenceSetEqual(first, second []PausePresence) bool {
	if len(first) != len(second) {
		return false
	}
	values := make(map[uuid.UUID]PausePresence, len(first))
	for _, value := range first {
		values[value.ParticipantID] = value
	}
	for _, value := range second {
		baseline, exists := values[value.ParticipantID]
		if !exists || !pauseResumePresenceEqual(baseline, value) {
			return false
		}
	}
	return true
}

func pauseResumeReconnectSetEqual(first, second []PauseReconnectInterval) bool {
	if len(first) != len(second) {
		return false
	}
	values := make(map[uuid.UUID]PauseReconnectInterval, len(first))
	for _, value := range first {
		values[value.ID] = value
	}
	for _, value := range second {
		baseline, exists := values[value.ID]
		if !exists || !pauseResumeReconnectEqual(baseline, value) {
			return false
		}
	}
	return true
}

func pauseResumeCounterSetEqual(first, second []PauseReconnectCounter) bool {
	if len(first) != len(second) {
		return false
	}
	values := make(map[[3]uuid.UUID]PauseReconnectCounter, len(first))
	for _, value := range first {
		values[[3]uuid.UUID{value.PauseID, value.RosterID, value.ParticipantID}] = value
	}
	for _, value := range second {
		baseline, exists := values[[3]uuid.UUID{value.PauseID, value.RosterID, value.ParticipantID}]
		if !exists || baseline != value {
			return false
		}
	}
	return true
}

func pauseResumePresenceEqual(first, second PausePresence) bool {
	return first.ID == second.ID && first.TournamentID == second.TournamentID && first.RosterID == second.RosterID &&
		first.SeriesID == second.SeriesID && first.ParticipantID == second.ParticipantID && first.State == second.State &&
		first.PresenceEpoch == second.PresenceEpoch && first.Revision == second.Revision && first.ConnectedAt.Equal(second.ConnectedAt) &&
		pauseResumeTimePointerEqual(first.DisconnectedAt, second.DisconnectedAt) && first.UpdatedAt.Equal(second.UpdatedAt)
}

func pauseResumeReconnectEqual(first, second PauseReconnectInterval) bool {
	return pauseResumeReconnectIdentityEqual(first, second) && pauseResumeReconnectLineageEqual(first, second) &&
		pauseResumeReconnectStateEqual(first, second)
}

func pauseResumeReconnectIdentityEqual(first, second PauseReconnectInterval) bool {
	return first.ID == second.ID && first.PauseID == second.PauseID && first.RosterID == second.RosterID &&
		first.SeriesID == second.SeriesID && first.GameID == second.GameID && first.ParticipantID == second.ParticipantID &&
		first.PresenceEpoch == second.PresenceEpoch
}

func pauseResumeReconnectLineageEqual(first, second PauseReconnectInterval) bool {
	return first.Number == second.Number && first.ContinuationNumber == second.ContinuationNumber &&
		pauseResumeUUIDPointerEqual(first.ContinuedFromID, second.ContinuedFromID) &&
		pauseResumeUUIDPointerEqual(first.SuspendedByPauseID, second.SuspendedByPauseID)
}

func pauseResumeReconnectStateEqual(first, second PauseReconnectInterval) bool {
	return first.State == second.State && first.OpenedAt.Equal(second.OpenedAt) && first.Deadline.Equal(second.Deadline) &&
		pauseResumeTimePointerEqual(first.ClosedAt, second.ClosedAt) && first.Revision == second.Revision && first.UpdatedAt.Equal(second.UpdatedAt)
}

func pauseResumeFrozenDeadlineEqual(first, second PauseFrozenDeadline) bool {
	return first.Kind == second.Kind && first.OwnerID == second.OwnerID && first.OriginalDeadline.Equal(second.OriginalDeadline) &&
		first.FrozenAt.Equal(second.FrozenAt) && first.Remaining == second.Remaining &&
		pauseResumeTimePointerEqual(first.ResumedAt, second.ResumedAt) &&
		pauseResumeTimePointerEqual(first.ResumedDeadline, second.ResumedDeadline) && first.Revision == second.Revision
}

func pauseResumeTimePointerEqual(first, second *time.Time) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return first.Equal(*second)
}

func pauseResumeUUIDPointerEqual(first, second *uuid.UUID) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func pauseResumeCanonicalPresence(value PausePresence) PausePresence {
	value.ConnectedAt = value.ConnectedAt.Round(0).UTC()
	value.UpdatedAt = value.UpdatedAt.Round(0).UTC()
	if value.DisconnectedAt != nil {
		at := value.DisconnectedAt.Round(0).UTC()
		value.DisconnectedAt = &at
	}
	return value
}

func pauseResumeCanonicalReconnect(value PauseReconnectInterval) PauseReconnectInterval {
	value.OpenedAt = value.OpenedAt.Round(0).UTC()
	value.Deadline = value.Deadline.Round(0).UTC()
	value.UpdatedAt = value.UpdatedAt.Round(0).UTC()
	if value.ClosedAt != nil {
		at := value.ClosedAt.Round(0).UTC()
		value.ClosedAt = &at
	}
	return value
}

func pauseResumeCanonicalFrozenDeadline(value PauseFrozenDeadline) PauseFrozenDeadline {
	value.OriginalDeadline = value.OriginalDeadline.Round(0).UTC()
	value.FrozenAt = value.FrozenAt.Round(0).UTC()
	if value.ResumedAt != nil {
		at := value.ResumedAt.Round(0).UTC()
		value.ResumedAt = &at
	}
	if value.ResumedDeadline != nil {
		deadline := value.ResumedDeadline.Round(0).UTC()
		value.ResumedDeadline = &deadline
	}
	return value
}

func pauseResumeDecisionExpectationEqual(first, second PauseResumeDecisionExpectation) bool {
	return first.PauseID == second.PauseID && first.ScopeKind == second.ScopeKind &&
		first.CurrentRevisionID == second.CurrentRevisionID && first.State == second.State && first.Revision == second.Revision &&
		first.SeriesID == second.SeriesID && first.GameID == second.GameID &&
		pauseResumeUUIDPointerEqual(first.ParentPauseID, second.ParentPauseID) && first.Depth == second.Depth &&
		first.DecisionNumber == second.DecisionNumber && first.StartedAt.Equal(second.StartedAt) &&
		pauseResumeGameClockPointerEqual(first.GameClock, second.GameClock)
}

func pauseResumeGameClockPointerEqual(first, second *PauseResumeGameClock) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return pauseResumeGameClockEqual(*first, *second)
}

func pauseResumeGameClockEqual(first, second PauseResumeGameClock) bool {
	return first.PauseID == second.PauseID && first.GameID == second.GameID &&
		first.OriginalDeadline.Equal(second.OriginalDeadline) && first.FrozenAt.Equal(second.FrozenAt) &&
		first.Remaining == second.Remaining && pauseResumeTimePointerEqual(first.ResumedAt, second.ResumedAt) &&
		pauseResumeTimePointerEqual(first.ResumedDeadline, second.ResumedDeadline) && first.Revision == second.Revision
}

func pauseResumePresenceCommandEqual(first, second PauseResumePresenceCommand) bool {
	return pauseResumePresenceResumeCommandEqual(first.Resume, second.Resume) &&
		pauseResumePresenceDecisionCommandEqual(first, second) && pauseResumePresenceInputCommandEqual(first, second)
}

func pauseResumePresenceResumeCommandEqual(first, second PauseResumeCommand) bool {
	return first.Scope == second.Scope && first.PauseID == second.PauseID &&
		first.CommandID == second.CommandID && first.ActorID == second.ActorID &&
		first.DraftResultRevisionID == second.DraftResultRevisionID &&
		pauseResumeExpectationEqual(first.Expected, second.Expected)
}

func pauseResumePresenceDecisionCommandEqual(first, second PauseResumePresenceCommand) bool {
	return first.SeriesDecisionID == second.SeriesDecisionID && first.GameDecisionID == second.GameDecisionID &&
		pauseResumeDecisionExpectationEqual(first.SeriesExpected, second.SeriesExpected) &&
		pauseResumeDecisionExpectationEqual(first.GameExpected, second.GameExpected)
}

func pauseResumePresenceInputCommandEqual(first, second PauseResumePresenceCommand) bool {
	return pauseResumePresenceSetEqual(first.Presence, second.Presence) &&
		pauseResumeReconnectSetEqual(first.Reconnect, second.Reconnect) && pauseResumeCounterSetEqual(first.Counters, second.Counters) &&
		pauseResumeFrozenBaselineEqual(first.FrozenDeadlines, second.FrozenDeadlines) &&
		pauseResumeIntervalInputEqual(first.FirstInterval, second.FirstInterval) &&
		pauseResumeIntervalInputEqual(first.SecondInterval, second.SecondInterval)
}

func pauseResumeIntervalInputEqual(first, second *PauseResumeIntervalInput) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func pauseResumeDecisionRecordEqual(first, second PauseResumeDecisionRecord) bool {
	return first.ID == second.ID && first.PauseID == second.PauseID && first.DecisionNumber == second.DecisionNumber &&
		first.Action == second.Action && pauseResumeUUIDPointerEqual(first.FirstReconnectIntervalID, second.FirstReconnectIntervalID) &&
		pauseResumeUUIDPointerEqual(first.SecondReconnectIntervalID, second.SecondReconnectIntervalID) && first.DecidedAt.Equal(second.DecidedAt)
}

func pauseResumeDecisionRecordPointerEqual(first, second *PauseResumeDecisionRecord) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return pauseResumeDecisionRecordEqual(*first, *second)
}

func pauseResumeParticipantResolutionEqual(first, second PauseResumeParticipantResolution) bool {
	return first.ParticipantID == second.ParticipantID && first.PresenceEpoch == second.PresenceEpoch &&
		first.Disposition == second.Disposition && first.Counter == second.Counter &&
		pauseResumeReconnectPointerEqual(first.SourceInterval, second.SourceInterval) &&
		pauseResumeReconnectPointerEqual(first.CurrentInterval, second.CurrentInterval)
}

func pauseResumeReconnectPointerEqual(first, second *PauseReconnectInterval) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return pauseResumeReconnectEqual(*first, *second)
}

func pauseResumePresenceRecordEqual(first, second PauseResumePresenceRecord) bool {
	return pauseResumePresenceCommandEqual(first.Command, second.Command) &&
		pauseResumeDecisionRecordEqual(first.GameDecision, second.GameDecision) &&
		pauseResumeDecisionRecordPointerEqual(first.SeriesDecision, second.SeriesDecision) &&
		pauseResumeGameClockEqual(first.GameClock, second.GameClock) && first.GamePauseState == second.GamePauseState &&
		first.SeriesPauseState == second.SeriesPauseState && first.NormalPauseState == second.NormalPauseState &&
		pauseResumeTimePointerEqual(first.NormalPauseResolvedAt, second.NormalPauseResolvedAt) &&
		pauseResumeGraphEvidenceEqual(first.Graph, second.Graph) &&
		pauseResumeParticipantResolutionEqual(first.First, second.First) &&
		pauseResumeParticipantResolutionEqual(first.Second, second.Second) && first.DecidedAt.Equal(second.DecidedAt)
}

func pauseResumeGraphEvidenceEqual(first, second PauseGraph) bool {
	return first.Scope == second.Scope && pauseGraphRevisionsEqual(PauseGraphRevisionsFrom(first), PauseGraphRevisionsFrom(second)) &&
		pauseResumeTournamentRecordEqual(first.Tournament, second.Tournament) &&
		pauseResumeWaveEqual(first.Wave, second.Wave) && pauseResumeSeriesSliceEqual(first.Series, second.Series) &&
		pauseResumeGameSliceEqual(first.Games, second.Games) && pauseResumeDraftPointerEqual(first.Draft, second.Draft) &&
		pauseResumePresenceSetEqual(first.Presence, second.Presence) &&
		pauseResumeReconnectSetEqual(first.Reconnect, second.Reconnect) && pauseResumeCounterSetEqual(first.Counters, second.Counters) &&
		pauseResumeFrozenBaselineEqual(first.FrozenDeadlines, second.FrozenDeadlines) && first.ActivePauseID == second.ActivePauseID &&
		pauseResumeTimePointerEqual(first.PausedAt, second.PausedAt) && first.DeadlinesSuppressed == second.DeadlinesSuppressed &&
		first.TerminalActionRevision == second.TerminalActionRevision
}

func pauseResumeTournamentRecordEqual(first, second TournamentRecord) bool {
	return first.ID == second.ID && first.RosterID == second.RosterID && first.Preset == second.Preset && first.State == second.State &&
		pauseResumeTournamentStatePointerEqual(first.PausedFromState, second.PausedFromState) && first.Revision == second.Revision &&
		first.RosterSize == second.RosterSize && first.CreatedAt.Equal(second.CreatedAt) && first.UpdatedAt.Equal(second.UpdatedAt) &&
		pauseResumeTimePointerEqual(first.StartedAt, second.StartedAt) && pauseResumeTimePointerEqual(first.FinishedAt, second.FinishedAt)
}

func pauseResumeWaveEqual(first, second PauseWave) bool {
	return first.Revision == second.Revision && pauseResumeArenaWaveEqual(first.Wave, second.Wave)
}

func pauseResumeArenaWaveEqual(first, second domain.ArenaWave) bool {
	return first.ID == second.ID && first.TournamentID == second.TournamentID && first.RevisionID == second.RevisionID &&
		first.State == second.State && pauseResumeComparableSliceEqual(first.Members, second.Members) &&
		pauseResumeReadyWindowPointerEqual(first.ReadyWindow, second.ReadyWindow) &&
		pauseResumeTimePointerEqual(first.StartedAt, second.StartedAt) && pauseResumeTimePointerEqual(first.PausedAt, second.PausedAt)
}

func pauseResumeReadyWindowPointerEqual(first, second *domain.ArenaReadyWindow) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return first.ID == second.ID && first.WaveID == second.WaveID && first.RevisionID == second.RevisionID &&
		first.State == second.State && first.OpenedAt.Equal(second.OpenedAt) && first.Deadline.Equal(second.Deadline) &&
		pauseResumeTimePointerEqual(first.ConsumedAt, second.ConsumedAt)
}

func pauseResumeSeriesSliceEqual(first, second []PauseSeries) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if !pauseResumeSeriesEqual(first[index], second[index]) {
			return false
		}
	}
	return true
}

func pauseResumeSeriesEqual(first, second PauseSeries) bool {
	return first.Revision == second.Revision && pauseResumeUUIDPointerEqual(first.CurrentGameID, second.CurrentGameID) &&
		pauseResumeSeriesExecutionEqual(first.Execution, second.Execution)
}

func pauseResumeSeriesExecutionEqual(first, second SeriesExecution) bool {
	return pauseResumeArenaSeriesEqual(first.Series, second.Series) &&
		pauseResumeComparablePointerEqual(first.ResumeState, second.ResumeState)
}

func pauseResumeArenaSeriesEqual(first, second domain.ArenaSeries) bool {
	return first.ID == second.ID && first.TournamentID == second.TournamentID &&
		first.FirstParticipantID == second.FirstParticipantID && first.SecondParticipantID == second.SecondParticipantID &&
		first.Format == second.Format && first.State == second.State && first.Score == second.Score &&
		pauseResumeUUIDPointerEqual(first.WinnerID, second.WinnerID) && pauseResumeGameSlotSliceEqual(first.Slots, second.Slots) &&
		pauseResumeComparablePointerEqual(first.CurrentScoreRevisionID, second.CurrentScoreRevisionID) &&
		pauseResumeComparablePointerEqual(first.CurrentResultRevisionID, second.CurrentResultRevisionID)
}

func pauseResumeGameSlotSliceEqual(first, second []domain.ArenaGameSlot) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if !pauseResumeGameSlotEqual(first[index], second[index]) {
			return false
		}
	}
	return true
}

func pauseResumeGameSlotEqual(first, second domain.ArenaGameSlot) bool {
	return first.ID == second.ID && first.SeriesID == second.SeriesID && first.Position == second.Position &&
		first.Category == second.Category && first.ScoreBefore == second.ScoreBefore &&
		pauseResumeArenaGameSliceEqual(first.Attempts, second.Attempts)
}

func pauseResumeArenaGameSliceEqual(first, second []domain.ArenaGame) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if !pauseResumeArenaGameEqual(first[index], second[index]) {
			return false
		}
	}
	return true
}

func pauseResumeArenaGameEqual(first, second domain.ArenaGame) bool {
	return first.ID == second.ID && first.SlotID == second.SlotID && first.AttemptNo == second.AttemptNo &&
		first.State == second.State && first.ResultReason == second.ResultReason &&
		pauseResumeUUIDPointerEqual(first.WinnerID, second.WinnerID) &&
		pauseResumeComparablePointerEqual(first.ResultRevisionID, second.ResultRevisionID)
}

func pauseResumeGameSliceEqual(first, second []PauseGame) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if !pauseResumeGameEqual(first[index], second[index]) {
			return false
		}
	}
	return true
}

func pauseResumeGameEqual(first, second PauseGame) bool {
	return first.SeriesID == second.SeriesID && pauseResumeArenaGameEqual(first.Game, second.Game) &&
		first.Revision == second.Revision && pauseResumeTimePointerEqual(first.Deadline, second.Deadline) &&
		pauseResumeComparablePointerEqual(first.ResumeState, second.ResumeState)
}

func pauseResumeDraftPointerEqual(first, second *DraftExecution) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return pauseResumeDraftEqual(*first, *second)
}

func pauseResumeDraftEqual(first, second DraftExecution) bool {
	return pauseResumeDraftIdentityEqual(first, second) && pauseResumeDraftStateEqual(first, second) &&
		pauseResumeDraftTimingEqual(first, second) && pauseResumeDraftCollectionsEqual(first, second)
}

func pauseResumeDraftIdentityEqual(first, second DraftExecution) bool {
	return first.ID == second.ID && first.SeriesID == second.SeriesID && first.Format == second.Format &&
		first.FirstParticipantID == second.FirstParticipantID && first.SecondParticipantID == second.SecondParticipantID &&
		first.RevisionID == second.RevisionID && first.PreviousRevisionID == second.PreviousRevisionID &&
		first.Revision == second.Revision && first.CommandID == second.CommandID && first.ServiceEpoch == second.ServiceEpoch &&
		first.Turn == second.Turn
}

func pauseResumeDraftStateEqual(first, second DraftExecution) bool {
	return first.State == second.State &&
		pauseResumeUUIDPointerEqual(first.CurrentActorID, second.CurrentActorID) &&
		pauseResumeComparablePointerEqual(first.CurrentAction, second.CurrentAction) &&
		pauseResumeDraftRecoveryPointerEqual(first.Recovery, second.Recovery) &&
		pauseResumeDraftTransitionPointerEqual(first.Transition, second.Transition)
}

func pauseResumeDraftTimingEqual(first, second DraftExecution) bool {
	return first.TurnDeadline.Equal(second.TurnDeadline) &&
		pauseResumeTimePointerEqual(first.AbsoluteDeadline, second.AbsoluteDeadline) &&
		first.PausedRemaining == second.PausedRemaining
}

func pauseResumeDraftCollectionsEqual(first, second DraftExecution) bool {
	return pauseResumeComparableSliceEqual(first.Pool, second.Pool) &&
		pauseResumeComparableSliceEqual(first.LegalCategories, second.LegalCategories) &&
		pauseResumeDraftActionSliceEqual(first.Actions, second.Actions) &&
		pauseResumeComparableSliceEqual(first.SelectedCategories, second.SelectedCategories) &&
		pauseResumeDecisionEvidenceEqual(first.FirstActorDecision, second.FirstActorDecision)
}

func pauseResumeDraftRecoveryPointerEqual(first, second *DraftRecoveryEvidence) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return first.Policy == second.Policy && first.Reason == second.Reason && first.PreviousState == second.PreviousState &&
		first.PreviousServiceEpoch == second.PreviousServiceEpoch && first.CurrentServiceEpoch == second.CurrentServiceEpoch &&
		first.PreviousDeadline.Equal(second.PreviousDeadline) && first.RecordedAt.Equal(second.RecordedAt) &&
		first.ActorID == second.ActorID && first.Note == second.Note
}

func pauseResumeDraftTransitionPointerEqual(first, second *DraftTransitionEvidence) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return first.Operation == second.Operation && first.ActorID == second.ActorID && first.Reason == second.Reason &&
		first.OccurredAt.Equal(second.OccurredAt)
}

func pauseResumeDraftActionSliceEqual(first, second []DraftActionRecord) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if !pauseResumeDraftActionEqual(first[index], second[index]) {
			return false
		}
	}
	return true
}

func pauseResumeDraftActionEqual(first, second DraftActionRecord) bool {
	return first.ID == second.ID && first.ResultRevisionID == second.ResultRevisionID && first.CommandID == second.CommandID &&
		first.Turn == second.Turn && first.ActorID == second.ActorID && first.Action == second.Action &&
		first.Category == second.Category && first.ScheduledDeadline.Equal(second.ScheduledDeadline) &&
		first.OccurredAt.Equal(second.OccurredAt) && first.Automatic == second.Automatic &&
		pauseResumeDecisionEvidencePointerEqual(first.DecisionEvidence, second.DecisionEvidence)
}

func pauseResumeDecisionEvidencePointerEqual(first, second *domain.ArenaDecisionEvidence) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return pauseResumeDecisionEvidenceEqual(*first, *second)
}

func pauseResumeDecisionEvidenceEqual(first, second domain.ArenaDecisionEvidence) bool {
	return first.ID == second.ID && first.Purpose == second.Purpose && first.AlgorithmVersion == second.AlgorithmVersion &&
		pauseResumeComparableSliceEqual(first.NormalizedInputs, second.NormalizedInputs) && first.Seed == second.Seed &&
		pauseResumeComparableSliceEqual(first.Result, second.Result) && first.ReplayDigest == second.ReplayDigest &&
		first.OwnerID == second.OwnerID && first.DecidedAt.Equal(second.DecidedAt)
}

func pauseResumeComparablePointerEqual[T comparable](first, second *T) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func pauseResumeComparableSliceEqual[T comparable](first, second []T) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if first[index] != second[index] {
			return false
		}
	}
	return true
}

func pauseResumeTournamentStatePointerEqual(first, second *domain.ArenaTournamentState) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func clonePauseResumeDecisionExpectation(value PauseResumeDecisionExpectation) PauseResumeDecisionExpectation {
	clone := value
	clone.ParentPauseID = cloneUUIDPointer(value.ParentPauseID)
	clone.GameClock = clonePauseResumeGameClockPointer(value.GameClock)
	return clone
}

func clonePauseResumeGameClockPointer(value *PauseResumeGameClock) *PauseResumeGameClock {
	if value == nil {
		return nil
	}
	clone := clonePauseResumeGameClock(*value)
	return &clone
}

func clonePauseResumeGameClock(value PauseResumeGameClock) PauseResumeGameClock {
	clone := value
	clone.ResumedAt = cloneTimePointer(value.ResumedAt)
	clone.ResumedDeadline = cloneTimePointer(value.ResumedDeadline)
	return clone
}
