package arena

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	normalPauseGraphAttempts = 2
	maxFrozenPauseDuration   = 7 * 24 * time.Hour
)

var (
	ErrInvalidNormalPauseGraph    = errors.New("invalid normal pause graph")
	ErrNormalPauseGraphConflict   = errors.New("normal pause graph conflict")
	ErrNormalPauseCommandReuse    = errors.New("normal pause command reuse")
	ErrNormalPauseGraphIncomplete = errors.New("normal pause graph incomplete")
	ErrNormalPauseGoldenActive    = errors.New("normal pause rejects active golden activity")
	ErrNormalPauseDeadline        = errors.New("normal pause deadline reached")
	ErrNormalPauseOverflow        = errors.New("normal pause revision overflow")
)

type PauseReason string

const (
	PauseReasonOperator       PauseReason = "operator"
	PauseReasonDisconnect     PauseReason = "disconnect"
	PauseReasonPlatform       PauseReason = "platform"
	PauseReasonExecutionEpoch PauseReason = "execution_epoch"
)

type PauseState string

const (
	PauseStateActive    PauseState = "active"
	PauseStateResumed   PauseState = "resumed"
	PauseStateCancelled PauseState = "cancelled"
)

type PresenceState string

const (
	PresenceStateConnected    PresenceState = "connected"
	PresenceStateDisconnected PresenceState = "disconnected"
)

type ReconnectState string

const (
	ReconnectStateOpen        ReconnectState = "open"
	ReconnectStateReconnected ReconnectState = "reconnected"
	ReconnectStateExpired     ReconnectState = "expired"
	ReconnectStateCancelled   ReconnectState = "cancelled"
)

type PauseDeadlineKind string

const (
	PauseDeadlineReadyWindow PauseDeadlineKind = "ready_window"
	PauseDeadlineGame        PauseDeadlineKind = "game"
	PauseDeadlineDraft       PauseDeadlineKind = "draft"
)

type PauseScopeKind string

const PauseScopeWave PauseScopeKind = "wave"

type PauseGraphScope struct {
	TournamentID uuid.UUID
	RosterID     uuid.UUID
	WaveID       uuid.UUID
	Authority    ExecutionAuthorityIdentity
}

type PauseChildRevision struct {
	ID       uuid.UUID
	Revision int64
}

type PausePresenceRevision struct {
	ID            uuid.UUID
	TournamentID  uuid.UUID
	RosterID      uuid.UUID
	SeriesID      uuid.UUID
	ParticipantID uuid.UUID
	PresenceEpoch int64
	Revision      int64
}

type PauseReconnectCounterRevision struct {
	PauseID       uuid.UUID
	RosterID      uuid.UUID
	ParticipantID uuid.UUID
	Revision      int64
}

type PauseFrozenDeadlineRevision struct {
	Kind     PauseDeadlineKind
	OwnerID  uuid.UUID
	Revision int64
}

type PauseGraphRevisions struct {
	GraphRevision           int64
	TournamentState         domain.ArenaTournamentState
	TournamentRevision      int64
	WaveRevision            int64
	Series                  []PauseChildRevision
	Games                   []PauseChildRevision
	Draft                   *DraftRevisionExpectation
	DraftPreviousRevisionID uuid.UUID
	Presence                []PausePresenceRevision
	Reconnect               []PauseChildRevision
	Counters                []PauseReconnectCounterRevision
	FrozenDeadlines         []PauseFrozenDeadlineRevision
	TerminalActionRevision  int64
}

type PauseWave struct {
	Wave     domain.ArenaWave
	Revision int64
}

type PauseSeries struct {
	Execution     SeriesExecution
	Revision      int64
	CurrentGameID *uuid.UUID
}

type PauseGame struct {
	SeriesID    uuid.UUID
	Game        domain.ArenaGame
	Revision    int64
	Deadline    *time.Time
	ResumeState *domain.ArenaGameState
}

type PausePresence struct {
	ID             uuid.UUID
	TournamentID   uuid.UUID
	RosterID       uuid.UUID
	SeriesID       uuid.UUID
	ParticipantID  uuid.UUID
	State          PresenceState
	PresenceEpoch  int64
	Revision       int64
	ConnectedAt    time.Time
	DisconnectedAt *time.Time
	UpdatedAt      time.Time
}

type PauseReconnectInterval struct {
	ID                 uuid.UUID
	PauseID            uuid.UUID
	RosterID           uuid.UUID
	SeriesID           uuid.UUID
	GameID             uuid.UUID
	ParticipantID      uuid.UUID
	PresenceEpoch      int64
	Number             int
	ContinuationNumber int
	ContinuedFromID    *uuid.UUID
	SuspendedByPauseID *uuid.UUID
	State              ReconnectState
	OpenedAt           time.Time
	Deadline           time.Time
	ClosedAt           *time.Time
	Revision           int64
	UpdatedAt          time.Time
}

type PauseReconnectCounter struct {
	PauseID       uuid.UUID
	RosterID      uuid.UUID
	ParticipantID uuid.UUID
	Limit         int
	Used          int
	Revision      int64
}

type PauseFrozenDeadline struct {
	Kind             PauseDeadlineKind
	OwnerID          uuid.UUID
	OriginalDeadline time.Time
	FrozenAt         time.Time
	Remaining        time.Duration
	ResumedAt        *time.Time
	ResumedDeadline  *time.Time
	Revision         int64
}

type pauseDeadlineIdentity struct {
	Kind    PauseDeadlineKind
	OwnerID uuid.UUID
}

type PauseGraph struct {
	Scope                  PauseGraphScope
	Revision               int64
	Tournament             TournamentRecord
	Wave                   PauseWave
	Series                 []PauseSeries
	Games                  []PauseGame
	Draft                  *DraftExecution
	Presence               []PausePresence
	Reconnect              []PauseReconnectInterval
	Counters               []PauseReconnectCounter
	FrozenDeadlines        []PauseFrozenDeadline
	ActivePauseID          uuid.UUID
	PausedAt               *time.Time
	DeadlinesSuppressed    bool
	TerminalActionRevision int64
}

type NormalPauseAuthority struct {
	Scope        PauseGraphScope
	Revisions    PauseGraphRevisions
	Graph        PauseGraph
	ActiveGolden bool
	Complete     bool
}

type NormalPauseCommand struct {
	Scope                 PauseGraphScope
	CommandID             uuid.UUID
	PauseID               uuid.UUID
	ActorID               uuid.UUID
	DraftResultRevisionID uuid.UUID
	Reason                PauseReason
	Expected              PauseGraphRevisions
}

type NormalPauseRecord struct {
	Scope                 PauseGraphScope
	ScopeKind             PauseScopeKind
	ScopeID               uuid.UUID
	CommandID             uuid.UUID
	PauseID               uuid.UUID
	ActorID               uuid.UUID
	DraftResultRevisionID uuid.UUID
	Reason                PauseReason
	State                 PauseState
	Revision              int64
	Expected              PauseGraphRevisions
	Graph                 PauseGraph
	SuspendedReconnect    []PauseChildRevision
	PausedAt              time.Time
	ResolvedAt            *time.Time
}

// NormalPauseRepository participates in the caller transaction. Load locks the
// complete authority. The stable lock order is execution-authority, pause and
// graph, Tournament, Wave, Series, Games, Draft, Presence, Reconnect, counters,
// frozen deadlines and terminal actions, with each collection ordered by its
// durable identity. Commit revalidates every expectation and publishes the
// complete graph and command result atomically or makes no write.
type NormalPauseRepository interface {
	FindNormalPauseCommand(ctx context.Context, tournamentID, commandID uuid.UUID) (*NormalPauseRecord, error)
	LoadNormalPauseAuthority(ctx context.Context, scope PauseGraphScope) (NormalPauseAuthority, error)
	CommitNormalPause(ctx context.Context, expected PauseGraphRevisions, record NormalPauseRecord) (*NormalPauseRecord, bool, error)
}

type NormalPauseGraphUseCase struct {
	transactions TransactionManager
	repository   NormalPauseRepository
	clock        Clock
}

func NewNormalPauseGraphUseCase(transactions TransactionManager, repository NormalPauseRepository, clock Clock) *NormalPauseGraphUseCase {
	return &NormalPauseGraphUseCase{transactions: transactions, repository: repository, clock: clock}
}

func (u *NormalPauseGraphUseCase) Enter(ctx context.Context, command NormalPauseCommand) (*NormalPauseRecord, bool, error) {
	command.Expected = clonePauseGraphRevisions(command.Expected)
	if u == nil || u.transactions == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validateNormalPauseCommand(command); err != nil {
		return nil, false, err
	}
	for range normalPauseGraphAttempts {
		record, changed, retry, err := u.enterAttempt(ctx, command)
		if retry {
			continue
		}
		return record, changed, err
	}
	return nil, false, ErrNormalPauseGraphConflict
}

func (u *NormalPauseGraphUseCase) enterAttempt(ctx context.Context, command NormalPauseCommand) (*NormalPauseRecord, bool, bool, error) {
	var outcome normalPauseAttemptOutcome
	err := u.transactions.Do(ctx, func(txCtx context.Context) error {
		var err error
		outcome, err = u.enterLocked(txCtx, command)
		return err
	})
	if err != nil {
		return nil, false, false, fmt.Errorf("normal pause - transaction: %w", err)
	}
	return outcome.record, outcome.changed, outcome.retry, nil
}

type normalPauseAttemptOutcome struct {
	record  *NormalPauseRecord
	changed bool
	retry   bool
}

func (u *NormalPauseGraphUseCase) enterLocked(ctx context.Context, command NormalPauseCommand) (normalPauseAttemptOutcome, error) {
	recorded, err := u.findNormalPauseCommand(ctx, command, "find command")
	if err != nil || recorded != nil {
		return reconcileNormalPauseOutcome(recorded, command, err)
	}
	authority, err := u.repository.LoadNormalPauseAuthority(ctx, command.Scope)
	if err != nil {
		return normalPauseAttemptOutcome{}, fmt.Errorf("normal pause - load authority: %w", err)
	}
	recorded, err = u.findNormalPauseCommand(ctx, command, "find locked command")
	if err != nil || recorded != nil {
		return reconcileNormalPauseOutcome(recorded, command, err)
	}
	pausedAt := u.clock.Now().Round(0).UTC()
	if !validArenaServerTime(pausedAt) {
		return normalPauseAttemptOutcome{}, domain.ErrValidation
	}
	if err := validateNormalPauseAuthority(authority); err != nil {
		return normalPauseAttemptOutcome{}, err
	}
	if authority.Scope != command.Scope || !pauseGraphRevisionsEqual(authority.Revisions, command.Expected) {
		return normalPauseAttemptOutcome{}, ErrNormalPauseGraphConflict
	}
	built, err := buildNormalPauseRecord(authority, command, pausedAt)
	if err != nil {
		return normalPauseAttemptOutcome{}, err
	}
	return u.commitNormalPause(ctx, command, built)
}

func (u *NormalPauseGraphUseCase) findNormalPauseCommand(ctx context.Context, command NormalPauseCommand, operation string) (*NormalPauseRecord, error) {
	recorded, err := u.repository.FindNormalPauseCommand(ctx, command.Scope.TournamentID, command.CommandID)
	if err != nil {
		return nil, fmt.Errorf("normal pause - %s: %w", operation, err)
	}
	return recorded, nil
}

func reconcileNormalPauseOutcome(recorded *NormalPauseRecord, command NormalPauseCommand, err error) (normalPauseAttemptOutcome, error) {
	if err != nil {
		return normalPauseAttemptOutcome{}, err
	}
	result, err := reconcileNormalPause(*recorded, command)
	return normalPauseAttemptOutcome{record: result}, err
}

func (u *NormalPauseGraphUseCase) commitNormalPause(ctx context.Context, command NormalPauseCommand, built NormalPauseRecord) (normalPauseAttemptOutcome, error) {
	canonical := cloneNormalPauseRecord(built)
	committed, changed, err := u.repository.CommitNormalPause(ctx, clonePauseGraphRevisions(command.Expected), cloneNormalPauseRecord(canonical))
	if errors.Is(err, domain.ErrConflict) {
		return normalPauseAttemptOutcome{retry: true}, nil
	}
	if err != nil {
		return normalPauseAttemptOutcome{}, fmt.Errorf("normal pause - commit graph: %w", err)
	}
	if committed == nil {
		return normalPauseAttemptOutcome{}, domain.ErrInternal
	}
	result, err := reconcileNormalPause(*committed, command)
	if err != nil || (changed && !reflect.DeepEqual(*result, canonical)) {
		return normalPauseAttemptOutcome{}, domain.ErrInternal
	}
	return normalPauseAttemptOutcome{record: result, changed: changed}, nil
}

func validateNormalPauseCommand(command NormalPauseCommand) error {
	if !validPauseGraphScope(command.Scope) || command.CommandID == uuid.Nil || command.PauseID == uuid.Nil ||
		command.ActorID == uuid.Nil || !command.Reason.allowsNormalPause() ||
		command.CommandID == command.PauseID || command.CommandID == command.ActorID || command.PauseID == command.ActorID {
		return normalPauseError("invalid command identity or reason")
	}
	if err := validatePauseGraphRevisions(command.Expected); err != nil {
		return err
	}
	if !validDraftResultRevisionIdentity(command.Expected.Draft, command.Expected.DraftPreviousRevisionID, command.DraftResultRevisionID,
		command.CommandID, command.PauseID, command.ActorID) {
		return normalPauseError("invalid Draft result revision identity")
	}
	return nil
}

func validDraftResultRevisionIdentity(
	expected *DraftRevisionExpectation,
	expectedPreviousRevisionID uuid.UUID,
	resultID uuid.UUID,
	commandID uuid.UUID,
	pauseID uuid.UUID,
	actorID uuid.UUID,
) bool {
	if expected == nil {
		return resultID == uuid.Nil && expectedPreviousRevisionID == uuid.Nil
	}
	return resultID != uuid.Nil && resultID != commandID && resultID != pauseID && resultID != actorID &&
		resultID != expected.RevisionID && resultID != expectedPreviousRevisionID && resultID != expected.ServiceEpoch &&
		validDraftPreviousRevision(*expected, expectedPreviousRevisionID)
}

func validateNormalPauseAuthority(authority NormalPauseAuthority) error {
	if !authority.Complete {
		return ErrNormalPauseGraphIncomplete
	}
	if authority.ActiveGolden || authority.Graph.Tournament.State == domain.ArenaTournamentStateGolden {
		return ErrNormalPauseGoldenActive
	}
	if authority.Scope != authority.Graph.Scope || !validPauseGraphScope(authority.Scope) {
		return normalPauseError("authority scope mismatch")
	}
	if authority.Graph.ActivePauseID != uuid.Nil || authority.Graph.PausedAt != nil ||
		authority.Graph.DeadlinesSuppressed || len(authority.Graph.FrozenDeadlines) != 0 {
		return normalPauseError("authority already contains pause evidence")
	}
	if err := validatePauseGraph(authority.Graph, false); err != nil {
		return err
	}
	if !pauseGraphRevisionsEqual(authority.Revisions, PauseGraphRevisionsFrom(authority.Graph)) {
		return normalPauseError("authority revisions do not match graph")
	}
	if authority.Graph.Tournament.State != domain.ArenaTournamentStateSwiss &&
		authority.Graph.Tournament.State != domain.ArenaTournamentStatePlayoffs {
		return normalPauseError("unsupported Tournament state %q", authority.Graph.Tournament.State)
	}
	return nil
}

func buildNormalPauseRecord(authority NormalPauseAuthority, command NormalPauseCommand, pausedAt time.Time) (NormalPauseRecord, error) {
	if !pauseTimeCoversGraphHistory(authority.Graph, pausedAt) {
		return NormalPauseRecord{}, normalPauseError("pause time precedes durable history")
	}
	graph := clonePauseGraph(authority.Graph)
	suspendedReconnect, err := suspendOpenReconnect(&graph, command.PauseID, pausedAt)
	if err != nil {
		return NormalPauseRecord{}, err
	}
	if graph.Revision == math.MaxInt64 || graph.Tournament.Revision == math.MaxInt64 {
		return NormalPauseRecord{}, ErrNormalPauseOverflow
	}
	tournament := domain.ArenaTournament{State: graph.Tournament.State, PausedFromState: graph.Tournament.PausedFromState}
	changed, err := tournament.TransitionTo(domain.ArenaTournamentStateTechnicalPause)
	if err != nil || !changed {
		return NormalPauseRecord{}, normalPauseError("pause Tournament: %v", err)
	}
	graph.Tournament.State = tournament.State
	graph.Tournament.PausedFromState = cloneTournamentStatePointer(tournament.PausedFromState)
	graph.Tournament.Revision++
	graph.Tournament.UpdatedAt = pausedAt

	if err := pauseWaveInGraph(&graph, pausedAt); err != nil {
		return NormalPauseRecord{}, err
	}
	if err := pauseSeriesAndGames(&graph, pausedAt); err != nil {
		return NormalPauseRecord{}, err
	}
	if err := pauseDraftInGraph(&graph, command, pausedAt); err != nil {
		return NormalPauseRecord{}, err
	}
	graph.Revision++
	graph.ActivePauseID = command.PauseID
	graph.PausedAt = cloneTimePointer(&pausedAt)
	graph.DeadlinesSuppressed = true
	record := NormalPauseRecord{
		Scope: command.Scope, ScopeKind: PauseScopeWave, ScopeID: command.Scope.WaveID,
		CommandID: command.CommandID, PauseID: command.PauseID,
		ActorID: command.ActorID, DraftResultRevisionID: command.DraftResultRevisionID,
		Reason: command.Reason, State: PauseStateActive, Revision: 1,
		Expected: clonePauseGraphRevisions(command.Expected), Graph: graph,
		SuspendedReconnect: suspendedReconnect, PausedAt: pausedAt,
	}
	if err := validateNormalPauseRecord(record); err != nil {
		return NormalPauseRecord{}, err
	}
	return cloneNormalPauseRecord(record), nil
}

func suspendOpenReconnect(graph *PauseGraph, normalPauseID uuid.UUID, pausedAt time.Time) ([]PauseChildRevision, error) {
	suspended := make([]PauseChildRevision, 0)
	for index := range graph.Reconnect {
		interval := &graph.Reconnect[index]
		if interval.State != ReconnectStateOpen {
			continue
		}
		if interval.Revision == math.MaxInt64 {
			return nil, ErrNormalPauseOverflow
		}
		if !interval.OpenedAt.Before(pausedAt) || !interval.Deadline.After(pausedAt) || interval.SuspendedByPauseID != nil {
			return nil, ErrNormalPauseDeadline
		}
		suspended = append(suspended, PauseChildRevision{ID: interval.ID, Revision: interval.Revision})
		interval.State = ReconnectStateCancelled
		interval.ClosedAt = cloneTimePointer(&pausedAt)
		interval.Revision++
		interval.UpdatedAt = pausedAt
		interval.SuspendedByPauseID = cloneUUIDPointer(&normalPauseID)
	}
	return suspended, nil
}

func pauseWaveInGraph(graph *PauseGraph, pausedAt time.Time) error {
	wave := &graph.Wave
	switch wave.Wave.State {
	case domain.ArenaWaveStateActive:
		if wave.Revision == math.MaxInt64 {
			return ErrNormalPauseOverflow
		}
		if err := wave.Wave.Pause(pausedAt); err != nil {
			return normalPauseError("pause Wave: %v", err)
		}
		wave.Revision++
	case domain.ArenaWaveStateReadyWindowOpen, domain.ArenaWaveStateReady:
		if wave.Revision == math.MaxInt64 || wave.Wave.ReadyWindow == nil {
			return ErrNormalPauseOverflow
		}
		frozen, err := newFrozenDeadline(PauseDeadlineReadyWindow, wave.Wave.ReadyWindow.ID, wave.Wave.ReadyWindow.Deadline, pausedAt)
		if err != nil {
			return err
		}
		graph.FrozenDeadlines = append(graph.FrozenDeadlines, frozen)
		wave.Revision++
	case domain.ArenaWaveStatePlanned, domain.ArenaWaveStateCompleted,
		domain.ArenaWaveStateReadyWindowExpired, domain.ArenaWaveStateSuperseded:
		return nil
	case domain.ArenaWaveStatePaused:
		return normalPauseError("Wave is already paused")
	default:
		return normalPauseError("unknown Wave state %q", wave.Wave.State)
	}
	return nil
}

func pauseSeriesAndGames(graph *PauseGraph, pausedAt time.Time) error {
	gameIndexes := make(map[uuid.UUID]int, len(graph.Games))
	for index := range graph.Games {
		gameIndexes[graph.Games[index].Game.ID] = index
	}
	for index := range graph.Series {
		series := &graph.Series[index]
		origin := series.Execution.Series.State
		mutated, err := pauseSeriesRecord(series)
		if err != nil {
			return err
		}
		if !mutated {
			continue
		}
		if err := pauseSeriesCurrentGame(graph, series, origin, gameIndexes, pausedAt); err != nil {
			return err
		}
	}
	return nil
}

func pauseSeriesRecord(series *PauseSeries) (bool, error) {
	switch series.Execution.Series.State {
	case domain.ArenaSeriesStateDraft, domain.ArenaSeriesStateReady,
		domain.ArenaSeriesStateActive, domain.ArenaSeriesStateReplayRequired:
		if series.Revision == math.MaxInt64 {
			return false, ErrNormalPauseOverflow
		}
		next, changed, err := TransitionSeriesExecution(series.Execution, SeriesExecutionTransitionCommand{
			NextState: domain.ArenaSeriesStateTechnicalPause,
		})
		if err != nil || !changed {
			return false, normalPauseError("pause Series: %v", err)
		}
		series.Execution = next
		series.Revision++
		return true, nil
	case domain.ArenaSeriesStatePlanned, domain.ArenaSeriesStateLocked,
		domain.ArenaSeriesStateCompleted, domain.ArenaSeriesStateCancelled:
		return false, nil
	case domain.ArenaSeriesStateTechnicalPause:
		return false, normalPauseError("Series is already paused")
	default:
		return false, normalPauseError("unknown Series state %q", series.Execution.Series.State)
	}
}

func pauseSeriesCurrentGame(
	graph *PauseGraph,
	series *PauseSeries,
	origin domain.ArenaSeriesState,
	gameIndexes map[uuid.UUID]int,
	pausedAt time.Time,
) error {
	if series.CurrentGameID == nil {
		if origin == domain.ArenaSeriesStateActive {
			return ErrNormalPauseGraphIncomplete
		}
		return nil
	}
	gameIndex, ok := gameIndexes[*series.CurrentGameID]
	if !ok {
		return ErrNormalPauseGraphIncomplete
	}
	game := &graph.Games[gameIndex]
	if game.Game.State != domain.ArenaGameStateActive {
		return nil
	}
	if game.Revision == math.MaxInt64 || game.Deadline == nil {
		return ErrNormalPauseOverflow
	}
	frozen, err := newFrozenDeadline(PauseDeadlineGame, game.Game.ID, *game.Deadline, pausedAt)
	if err != nil {
		return err
	}
	gameOrigin := game.Game.State
	game.Game.State = domain.ArenaGameStatePaused
	game.ResumeState = &gameOrigin
	game.Deadline = nil
	game.Revision++
	graph.FrozenDeadlines = append(graph.FrozenDeadlines, frozen)
	if !replaceSeriesGame(&series.Execution.Series, game.Game) {
		return ErrNormalPauseGraphIncomplete
	}
	return nil
}

func pauseDraftInGraph(graph *PauseGraph, command NormalPauseCommand, pausedAt time.Time) error {
	if graph.Draft == nil || graph.Draft.State != DraftExecutionStateActive {
		return nil
	}
	draft := cloneDraftExecution(*graph.Draft)
	if draft.Revision == math.MaxInt64 || draft.AbsoluteDeadline == nil {
		return ErrNormalPauseOverflow
	}
	frozen, err := newFrozenDeadline(PauseDeadlineDraft, draft.ID, *draft.AbsoluteDeadline, pausedAt)
	if err != nil {
		return err
	}
	if command.Expected.DraftPreviousRevisionID != draft.PreviousRevisionID ||
		command.DraftResultRevisionID == draft.ID || command.DraftResultRevisionID == draft.RevisionID ||
		command.DraftResultRevisionID == draft.PreviousRevisionID {
		return normalPauseError("Draft result revision identity matches Draft")
	}
	advanceDraftRevision(&draft, command.DraftResultRevisionID, command.CommandID, draft.ServiceEpoch)
	draft.State = DraftExecutionStatePaused
	draft.AbsoluteDeadline = nil
	draft.PausedRemaining = frozen.Remaining
	draft.Recovery = &DraftRecoveryEvidence{
		Policy: DraftRecoveryPolicyShiftRemaining, Reason: DraftRecoveryReasonOperatorPause,
		PreviousState: DraftExecutionStateActive, PreviousServiceEpoch: draft.ServiceEpoch,
		CurrentServiceEpoch: draft.ServiceEpoch, PreviousDeadline: frozen.OriginalDeadline,
		RecordedAt: pausedAt, ActorID: command.ActorID, Note: string(command.Reason),
	}
	draft.Transition = &DraftTransitionEvidence{Operation: DraftTransitionPause, ActorID: command.ActorID, Reason: string(command.Reason), OccurredAt: pausedAt}
	if err := draft.Validate(); err != nil {
		return normalPauseError("pause Draft: %v", err)
	}
	graph.Draft = &draft
	graph.FrozenDeadlines = append(graph.FrozenDeadlines, frozen)
	return nil
}

func newFrozenDeadline(kind PauseDeadlineKind, ownerID uuid.UUID, deadline, frozenAt time.Time) (PauseFrozenDeadline, error) {
	if ownerID == uuid.Nil || !validArenaServerTime(deadline) || !deadline.After(frozenAt) {
		return PauseFrozenDeadline{}, ErrNormalPauseDeadline
	}
	remaining := deadline.Sub(frozenAt)
	if remaining <= 0 || remaining > maxFrozenPauseDuration {
		return PauseFrozenDeadline{}, ErrNormalPauseDeadline
	}
	return PauseFrozenDeadline{Kind: kind, OwnerID: ownerID, OriginalDeadline: deadline, FrozenAt: frozenAt, Remaining: remaining, Revision: 1}, nil
}

func validateNormalPauseRecord(record NormalPauseRecord) error {
	if !validNormalPauseRecordHeader(record) || !validNormalPauseRecordGraphLink(record) {
		return normalPauseError("invalid active pause record")
	}
	if err := validatePauseGraph(record.Graph, true); err != nil {
		return err
	}
	return nil
}

func validNormalPauseRecordHeader(record NormalPauseRecord) bool {
	return validPauseGraphScope(record.Scope) && record.CommandID != uuid.Nil && record.PauseID != uuid.Nil &&
		record.ScopeKind == PauseScopeWave && record.ScopeID == record.Scope.WaveID &&
		record.ActorID != uuid.Nil && record.Reason.allowsNormalPause() && record.State == PauseStateActive &&
		record.CommandID != record.PauseID && record.CommandID != record.ActorID && record.PauseID != record.ActorID &&
		record.Revision == 1 && validArenaServerTime(record.PausedAt) && record.ResolvedAt == nil &&
		validDraftResultRevisionIdentity(record.Expected.Draft, record.Expected.DraftPreviousRevisionID, record.DraftResultRevisionID,
			record.CommandID, record.PauseID, record.ActorID)
}

func validNormalPauseRecordGraphLink(record NormalPauseRecord) bool {
	return record.Graph.Scope == record.Scope && record.Graph.ActivePauseID == record.PauseID &&
		record.Graph.PausedAt != nil && record.Graph.PausedAt.Equal(record.PausedAt) &&
		record.Graph.DeadlinesSuppressed && normalPauseReconnectSetTerminal(record.Graph.Reconnect) &&
		pauseTimeCoversGraphHistory(record.Graph, record.PausedAt) &&
		pausedGraphMatchesExpected(record.Graph, record.Expected, record.DraftResultRevisionID,
			record.CommandID, record.ActorID, record.Reason, record.PausedAt, record.PauseID, record.SuspendedReconnect)
}

func normalPauseReconnectSetTerminal(values []PauseReconnectInterval) bool {
	for _, value := range values {
		if value.State == ReconnectStateOpen {
			return false
		}
	}
	return true
}

func pausedGraphMatchesExpected(
	graph PauseGraph,
	expected PauseGraphRevisions,
	draftResultRevisionID uuid.UUID,
	commandID uuid.UUID,
	actorID uuid.UUID,
	reason PauseReason,
	pausedAt time.Time,
	pauseID uuid.UUID,
	suspended []PauseChildRevision,
) bool {
	if !pausedRootRevisionsMatch(graph, expected) {
		return false
	}
	current := PauseGraphRevisionsFrom(graph)
	if !pausedChildRevisionsMatch(graph, current, expected, draftResultRevisionID, commandID, actorID, reason, pausedAt, pauseID, suspended) {
		return false
	}
	for _, frozen := range graph.FrozenDeadlines {
		if frozen.Revision != 1 {
			return false
		}
	}
	return true
}

func pausedRootRevisionsMatch(graph PauseGraph, expected PauseGraphRevisions) bool {
	if validatePauseGraphRevisions(expected) != nil || expected.GraphRevision == math.MaxInt64 ||
		expected.TournamentRevision == math.MaxInt64 || graph.Revision != expected.GraphRevision+1 ||
		graph.Tournament.Revision != expected.TournamentRevision+1 || graph.PausedAt == nil ||
		graph.Tournament.State != domain.ArenaTournamentStateTechnicalPause || graph.Tournament.PausedFromState == nil ||
		!graph.Tournament.UpdatedAt.Equal(*graph.PausedAt) {
		return false
	}
	if *graph.Tournament.PausedFromState != expected.TournamentState {
		return false
	}
	return pausedWaveRevisionMatches(graph.Wave, expected.WaveRevision)
}

func pausedChildRevisionsMatch(
	graph PauseGraph,
	current PauseGraphRevisions,
	expected PauseGraphRevisions,
	draftResultRevisionID uuid.UUID,
	commandID uuid.UUID,
	actorID uuid.UUID,
	reason PauseReason,
	pausedAt time.Time,
	pauseID uuid.UUID,
	suspended []PauseChildRevision,
) bool {
	return pausedSeriesRevisionsMatch(graph.Series, expected.Series) && pausedGameRevisionsMatch(graph.Games, expected.Games) &&
		presenceRevisionMapEqual(current.Presence, expected.Presence) && pausedReconnectRevisionsMatch(graph.Reconnect, expected.Reconnect, suspended, pauseID, pausedAt) &&
		counterRevisionMapEqual(current.Counters, expected.Counters) && current.TerminalActionRevision == expected.TerminalActionRevision &&
		pausedDraftRevisionMatches(graph.Draft, expected.Draft, expected.DraftPreviousRevisionID,
			draftResultRevisionID, commandID, actorID, reason, pausedAt) && len(expected.FrozenDeadlines) == 0
}

func pausedWaveRevisionMatches(current PauseWave, expected int64) bool {
	switch current.Wave.State {
	case domain.ArenaWaveStatePaused, domain.ArenaWaveStateReadyWindowOpen, domain.ArenaWaveStateReady:
		return nextRevisionMatches(current.Revision, expected)
	case domain.ArenaWaveStatePlanned, domain.ArenaWaveStateCompleted,
		domain.ArenaWaveStateReadyWindowExpired, domain.ArenaWaveStateSuperseded:
		return current.Revision == expected
	case domain.ArenaWaveStateActive:
		return false
	default:
		return false
	}
}

func pausedSeriesRevisionsMatch(current []PauseSeries, expected []PauseChildRevision) bool {
	if len(current) != len(expected) {
		return false
	}
	for _, series := range current {
		revision, ok := childRevision(expected, series.Execution.Series.ID)
		if !ok || !pausedSeriesRevisionMatches(series, revision) {
			return false
		}
	}
	return true
}

func pausedSeriesRevisionMatches(current PauseSeries, expected int64) bool {
	switch current.Execution.Series.State {
	case domain.ArenaSeriesStateTechnicalPause:
		return nextRevisionMatches(current.Revision, expected)
	case domain.ArenaSeriesStatePlanned, domain.ArenaSeriesStateLocked,
		domain.ArenaSeriesStateCompleted, domain.ArenaSeriesStateCancelled:
		return current.Revision == expected
	case domain.ArenaSeriesStateDraft, domain.ArenaSeriesStateReady,
		domain.ArenaSeriesStateActive, domain.ArenaSeriesStateReplayRequired:
		return false
	default:
		return false
	}
}

func pausedGameRevisionsMatch(current []PauseGame, expected []PauseChildRevision) bool {
	if len(current) != len(expected) {
		return false
	}
	for _, game := range current {
		revision, ok := childRevision(expected, game.Game.ID)
		if !ok || !pausedGameRevisionMatches(game, revision) {
			return false
		}
	}
	return true
}

func pausedGameRevisionMatches(current PauseGame, expected int64) bool {
	switch current.Game.State {
	case domain.ArenaGameStatePaused:
		return nextRevisionMatches(current.Revision, expected)
	case domain.ArenaGameStatePlanned, domain.ArenaGameStateReady, domain.ArenaGameStateCompleted,
		domain.ArenaGameStateVoid, domain.ArenaGameStateCancelled, domain.ArenaGameStateSuperseded:
		return current.Revision == expected
	case domain.ArenaGameStateActive:
		return false
	default:
		return false
	}
}

func pausedReconnectRevisionsMatch(
	current []PauseReconnectInterval,
	expected []PauseChildRevision,
	suspended []PauseChildRevision,
	pauseID uuid.UUID,
	pausedAt time.Time,
) bool {
	if len(current) != len(expected) {
		return false
	}
	expectedByID, ok := pauseChildRevisionMap(expected)
	if !ok {
		return false
	}
	suspendedByID, ok := suspendedReconnectRevisionMap(suspended, expectedByID)
	if !ok {
		return false
	}
	seen := make(map[uuid.UUID]struct{}, len(current))
	for _, interval := range current {
		revision, exists := expectedByID[interval.ID]
		if !exists || pauseUUIDSeen(seen, interval.ID) {
			return false
		}
		seen[interval.ID] = struct{}{}
		if sourceRevision, wasSuspended := suspendedByID[interval.ID]; wasSuspended {
			if !pausedSuspendedReconnectMatches(interval, sourceRevision, pauseID, pausedAt) {
				return false
			}
		} else if !pausedUnchangedReconnectMatches(interval, revision, pauseID) {
			return false
		}
	}
	return len(seen) == len(expectedByID)
}

func pauseChildRevisionMap(values []PauseChildRevision) (map[uuid.UUID]int64, bool) {
	result := make(map[uuid.UUID]int64, len(values))
	for _, value := range values {
		if value.ID == uuid.Nil || pauseUUIDSeenRevision(result, value.ID) {
			return nil, false
		}
		result[value.ID] = value.Revision
	}
	return result, true
}

func suspendedReconnectRevisionMap(values []PauseChildRevision, expected map[uuid.UUID]int64) (map[uuid.UUID]int64, bool) {
	result := make(map[uuid.UUID]int64, len(values))
	for _, value := range values {
		revision, exists := expected[value.ID]
		if !exists || revision != value.Revision || pauseUUIDSeenRevision(result, value.ID) {
			return nil, false
		}
		result[value.ID] = value.Revision
	}
	return result, true
}

func pauseUUIDSeen(values map[uuid.UUID]struct{}, id uuid.UUID) bool {
	_, exists := values[id]
	return exists
}

func pauseUUIDSeenRevision(values map[uuid.UUID]int64, id uuid.UUID) bool {
	_, exists := values[id]
	return exists
}

func pausedSuspendedReconnectMatches(interval PauseReconnectInterval, sourceRevision int64, pauseID uuid.UUID, pausedAt time.Time) bool {
	return sourceRevision < math.MaxInt64 && interval.Revision == sourceRevision+1 &&
		interval.State == ReconnectStateCancelled && interval.ClosedAt != nil && interval.ClosedAt.Equal(pausedAt) &&
		interval.OpenedAt.Before(pausedAt) && interval.Deadline.After(pausedAt) && interval.UpdatedAt.Equal(pausedAt) &&
		interval.SuspendedByPauseID != nil && *interval.SuspendedByPauseID == pauseID
}

func pausedUnchangedReconnectMatches(interval PauseReconnectInterval, revision int64, pauseID uuid.UUID) bool {
	return interval.Revision == revision &&
		(interval.SuspendedByPauseID == nil || *interval.SuspendedByPauseID != pauseID)
}

func pausedDraftRevisionMatches(
	current *DraftExecution,
	expected *DraftRevisionExpectation,
	expectedPreviousRevisionID uuid.UUID,
	resultRevisionID uuid.UUID,
	commandID uuid.UUID,
	actorID uuid.UUID,
	reason PauseReason,
	pausedAt time.Time,
) bool {
	if current == nil || expected == nil {
		return absentDraftRevisionMatches(current, expected, resultRevisionID)
	}
	if current.ServiceEpoch != expected.ServiceEpoch {
		return false
	}
	switch current.State {
	case DraftExecutionStatePaused:
		return pausedDraftMutationMatches(current, expected, expectedPreviousRevisionID,
			resultRevisionID, commandID, actorID, reason, pausedAt)
	case DraftExecutionStateRecoveryRequired, DraftExecutionStateCompleted, DraftExecutionStateSuperseded:
		return unchangedDraftRevisionMatches(current, expected, expectedPreviousRevisionID, resultRevisionID)
	case DraftExecutionStateActive:
		return false
	default:
		return false
	}
}

func absentDraftRevisionMatches(current *DraftExecution, expected *DraftRevisionExpectation, resultRevisionID uuid.UUID) bool {
	return current == nil && expected == nil && resultRevisionID == uuid.Nil
}

func pausedDraftMutationMatches(
	current *DraftExecution,
	expected *DraftRevisionExpectation,
	expectedPreviousRevisionID uuid.UUID,
	resultRevisionID uuid.UUID,
	commandID uuid.UUID,
	actorID uuid.UUID,
	reason PauseReason,
	pausedAt time.Time,
) bool {
	lineageMatches := nextRevisionMatches(current.Revision, expected.Revision) &&
		current.PreviousRevisionID == expected.RevisionID && current.RevisionID == resultRevisionID
	identityMatches := current.CommandID == commandID && current.ID != resultRevisionID &&
		resultRevisionID != expectedPreviousRevisionID
	return lineageMatches && identityMatches && draftTransitionMatches(current.Transition,
		DraftTransitionPause, actorID, string(reason), pausedAt)
}

func unchangedDraftRevisionMatches(
	current *DraftExecution,
	expected *DraftRevisionExpectation,
	expectedPreviousRevisionID uuid.UUID,
	resultRevisionID uuid.UUID,
) bool {
	return current.Revision == expected.Revision && current.RevisionID == expected.RevisionID &&
		current.PreviousRevisionID == expectedPreviousRevisionID && current.ID != resultRevisionID
}

func draftTransitionMatches(
	transition *DraftTransitionEvidence,
	operation DraftTransitionOperation,
	actorID uuid.UUID,
	reason string,
	occurredAt time.Time,
) bool {
	return transition != nil && transition.Operation == operation && transition.ActorID == actorID &&
		transition.Reason == reason && transition.OccurredAt.Equal(occurredAt)
}

func childRevision(values []PauseChildRevision, id uuid.UUID) (int64, bool) {
	for _, value := range values {
		if value.ID == id {
			return value.Revision, true
		}
	}
	return 0, false
}

func nextRevisionMatches(current, expected int64) bool {
	return expected < math.MaxInt64 && current == expected+1
}

func reconcileNormalPause(record NormalPauseRecord, command NormalPauseCommand) (*NormalPauseRecord, error) {
	if validateNormalPauseRecord(record) != nil || record.Scope != command.Scope || record.CommandID != command.CommandID ||
		record.PauseID != command.PauseID || record.ActorID != command.ActorID || record.Reason != command.Reason ||
		record.DraftResultRevisionID != command.DraftResultRevisionID ||
		!pauseGraphRevisionsEqual(record.Expected, command.Expected) {
		return nil, ErrNormalPauseCommandReuse
	}
	clone := cloneNormalPauseRecord(record)
	return &clone, nil
}

func PauseGraphRevisionsFrom(graph PauseGraph) PauseGraphRevisions {
	revisions := PauseGraphRevisions{
		GraphRevision: graph.Revision, TournamentState: graph.Tournament.State, TournamentRevision: graph.Tournament.Revision,
		WaveRevision: graph.Wave.Revision, TerminalActionRevision: graph.TerminalActionRevision,
		Series: make([]PauseChildRevision, len(graph.Series)), Games: make([]PauseChildRevision, len(graph.Games)),
		Presence: make([]PausePresenceRevision, len(graph.Presence)), Reconnect: make([]PauseChildRevision, len(graph.Reconnect)),
		Counters: make([]PauseReconnectCounterRevision, len(graph.Counters)), FrozenDeadlines: make([]PauseFrozenDeadlineRevision, len(graph.FrozenDeadlines)),
	}
	for index := range graph.Series {
		revisions.Series[index] = PauseChildRevision{ID: graph.Series[index].Execution.Series.ID, Revision: graph.Series[index].Revision}
	}
	for index := range graph.Games {
		revisions.Games[index] = PauseChildRevision{ID: graph.Games[index].Game.ID, Revision: graph.Games[index].Revision}
	}
	if graph.Draft != nil {
		expected := draftExpectation(*graph.Draft)
		revisions.Draft = &expected
		revisions.DraftPreviousRevisionID = graph.Draft.PreviousRevisionID
	}
	for index := range graph.Presence {
		presence := graph.Presence[index]
		revisions.Presence[index] = PausePresenceRevision{ID: presence.ID, TournamentID: presence.TournamentID, RosterID: presence.RosterID, SeriesID: presence.SeriesID, ParticipantID: presence.ParticipantID, PresenceEpoch: presence.PresenceEpoch, Revision: presence.Revision}
	}
	for index := range graph.Reconnect {
		revisions.Reconnect[index] = PauseChildRevision{ID: graph.Reconnect[index].ID, Revision: graph.Reconnect[index].Revision}
	}
	for index, counter := range graph.Counters {
		revisions.Counters[index] = PauseReconnectCounterRevision{PauseID: counter.PauseID, RosterID: counter.RosterID, ParticipantID: counter.ParticipantID, Revision: counter.Revision}
	}
	for index, frozen := range graph.FrozenDeadlines {
		revisions.FrozenDeadlines[index] = PauseFrozenDeadlineRevision{Kind: frozen.Kind, OwnerID: frozen.OwnerID, Revision: frozen.Revision}
	}
	return revisions
}

func validatePauseGraph(graph PauseGraph, paused bool) error {
	if !validPauseGraphRoot(graph) {
		return normalPauseError("invalid graph root")
	}
	if !validPauseGraphStateEvidence(graph, paused) {
		return normalPauseError("invalid graph pause evidence")
	}
	index, err := validatePauseSeriesDescendants(graph)
	if err != nil {
		return fmt.Errorf("pause graph - Series: %w", err)
	}
	if err := validatePauseGameDescendants(graph, index); err != nil {
		return fmt.Errorf("pause graph - Games: %w", err)
	}
	if err := validatePauseDraftDescendant(graph, index); err != nil {
		return fmt.Errorf("pause graph - Draft: %w", err)
	}
	if err := validatePausePresenceSet(graph, index); err != nil {
		return fmt.Errorf("pause graph - Presence: %w", err)
	}
	if err := validateReconnectSet(graph, index); err != nil {
		return fmt.Errorf("pause graph - Reconnect: %w", err)
	}
	if err := validateFrozenDeadlineSet(graph, paused); err != nil {
		return fmt.Errorf("pause graph - frozen deadlines: %w", err)
	}
	return nil
}

func validPauseGraphRoot(graph PauseGraph) bool {
	return validPauseGraphScope(graph.Scope) && graph.Revision >= 1 &&
		graph.Tournament.ID == graph.Scope.TournamentID && graph.Tournament.RosterID == graph.Scope.RosterID &&
		graph.Wave.Wave.ID == graph.Scope.WaveID && graph.Wave.Wave.TournamentID == graph.Scope.TournamentID &&
		graph.Wave.Revision >= 1 && validateTournamentRecordPointer(&graph.Tournament, graph.Scope.TournamentID) == nil &&
		graph.Wave.Wave.Validate() == nil && len(graph.Series) > 0 && graph.TerminalActionRevision >= 0
}

func validPauseGraphStateEvidence(graph PauseGraph, paused bool) bool {
	if paused != graph.DeadlinesSuppressed {
		return false
	}
	if paused {
		return graph.ActivePauseID != uuid.Nil && graph.PausedAt != nil
	}
	return graph.ActivePauseID == uuid.Nil && graph.PausedAt == nil
}

type pauseGraphIndex struct {
	seriesByID            map[uuid.UUID]PauseSeries
	participantSeries     map[uuid.UUID]uuid.UUID
	currentGames          map[uuid.UUID]uuid.UUID
	gamesByID             map[uuid.UUID]PauseGame
	presenceByParticipant map[uuid.UUID]PausePresence
}

func validatePauseSeriesDescendants(graph PauseGraph) (pauseGraphIndex, error) {
	index := pauseGraphIndex{
		seriesByID:            make(map[uuid.UUID]PauseSeries, len(graph.Series)),
		participantSeries:     make(map[uuid.UUID]uuid.UUID, len(graph.Wave.Wave.Members)),
		currentGames:          make(map[uuid.UUID]uuid.UUID, len(graph.Series)),
		gamesByID:             make(map[uuid.UUID]PauseGame, len(graph.Games)),
		presenceByParticipant: make(map[uuid.UUID]PausePresence, len(graph.Presence)),
	}
	waveMembers := make(map[uuid.UUID]struct{}, len(graph.Wave.Wave.Members))
	for _, member := range graph.Wave.Wave.Members {
		waveMembers[member.ParticipantID] = struct{}{}
	}
	for _, series := range graph.Series {
		id := series.Execution.Series.ID
		if id == uuid.Nil || series.Revision < 1 || series.Execution.Validate() != nil || series.Execution.Series.TournamentID != graph.Scope.TournamentID {
			return pauseGraphIndex{}, normalPauseError("invalid Series descendant")
		}
		if _, duplicate := index.seriesByID[id]; duplicate {
			return pauseGraphIndex{}, normalPauseError("duplicate Series descendant")
		}
		index.seriesByID[id] = series
		participants := [2]uuid.UUID{series.Execution.Series.FirstParticipantID, series.Execution.Series.SecondParticipantID}
		for _, participantID := range participants {
			if _, exists := waveMembers[participantID]; !exists {
				return pauseGraphIndex{}, ErrNormalPauseGraphIncomplete
			}
			if _, duplicate := index.participantSeries[participantID]; duplicate {
				return pauseGraphIndex{}, ErrNormalPauseGraphIncomplete
			}
			index.participantSeries[participantID] = id
		}
		if series.CurrentGameID != nil {
			if *series.CurrentGameID == uuid.Nil {
				return pauseGraphIndex{}, normalPauseError("empty current Game identity")
			}
			if _, duplicate := index.currentGames[*series.CurrentGameID]; duplicate {
				return pauseGraphIndex{}, normalPauseError("duplicate current Game identity")
			}
			index.currentGames[*series.CurrentGameID] = id
		}
	}
	if len(index.participantSeries) != len(waveMembers) {
		return pauseGraphIndex{}, ErrNormalPauseGraphIncomplete
	}
	return index, nil
}

func validatePauseGameDescendants(graph PauseGraph, index pauseGraphIndex) error {
	seenGames := make(map[uuid.UUID]struct{}, len(graph.Games))
	for _, game := range graph.Games {
		seriesID, expected := index.currentGames[game.Game.ID]
		if !expected || seriesID != game.SeriesID || game.Revision < 1 || game.Game.Validate() != nil || game.Game.ID == uuid.Nil {
			return ErrNormalPauseGraphIncomplete
		}
		if _, duplicate := seenGames[game.Game.ID]; duplicate {
			return normalPauseError("duplicate Game descendant")
		}
		seenGames[game.Game.ID] = struct{}{}
		series := index.seriesByID[seriesID]
		embedded, found := embeddedSeriesGame(series.Execution.Series, game.Game.ID)
		if !found || !reflect.DeepEqual(embedded, game.Game) || !validSeriesGameState(series, game) {
			return ErrNormalPauseGraphIncomplete
		}
		index.gamesByID[game.Game.ID] = game
	}
	if len(seenGames) != len(index.currentGames) {
		return ErrNormalPauseGraphIncomplete
	}
	return nil
}

func embeddedSeriesGame(series domain.ArenaSeries, gameID uuid.UUID) (domain.ArenaGame, bool) {
	var result domain.ArenaGame
	found := false
	for _, slot := range series.Slots {
		for _, game := range slot.Attempts {
			if game.ID != gameID {
				continue
			}
			if found {
				return domain.ArenaGame{}, false
			}
			result, found = game, true
		}
	}
	return result, found
}

func validSeriesGameState(series PauseSeries, game PauseGame) bool {
	switch series.Execution.Series.State {
	case domain.ArenaSeriesStateActive:
		return game.Game.State == domain.ArenaGameStateActive && game.ResumeState == nil
	case domain.ArenaSeriesStateTechnicalPause:
		if series.Execution.ResumeState == nil || *series.Execution.ResumeState != domain.ArenaSeriesStateActive {
			return game.Game.State != domain.ArenaGameStateActive && game.Game.State != domain.ArenaGameStatePaused
		}
		return game.Game.State == domain.ArenaGameStatePaused && game.ResumeState != nil && *game.ResumeState == domain.ArenaGameStateActive
	case domain.ArenaSeriesStatePlanned, domain.ArenaSeriesStateLocked, domain.ArenaSeriesStateDraft,
		domain.ArenaSeriesStateReady, domain.ArenaSeriesStateReplayRequired, domain.ArenaSeriesStateCompleted,
		domain.ArenaSeriesStateCancelled:
		return game.Game.State != domain.ArenaGameStateActive && game.Game.State != domain.ArenaGameStatePaused
	default:
		return false
	}
}

func validatePauseDraftDescendant(graph PauseGraph, index pauseGraphIndex) error {
	if graph.Draft == nil {
		return validateMissingPauseDraft(graph.Series)
	}
	if graph.Draft.Validate() != nil {
		return normalPauseError("invalid Draft descendant")
	}
	series, exists := index.seriesByID[graph.Draft.SeriesID]
	if !exists || series.Execution.Series.FirstParticipantID != graph.Draft.FirstParticipantID ||
		series.Execution.Series.SecondParticipantID != graph.Draft.SecondParticipantID {
		return ErrNormalPauseGraphIncomplete
	}
	if !pauseDraftStateMatchesSeries(*graph.Draft, series) {
		return ErrNormalPauseGraphIncomplete
	}
	return nil
}

func validateMissingPauseDraft(seriesValues []PauseSeries) error {
	for _, series := range seriesValues {
		if series.Execution.Series.State == domain.ArenaSeriesStateDraft ||
			(series.Execution.Series.State == domain.ArenaSeriesStateTechnicalPause && series.Execution.ResumeState != nil && *series.Execution.ResumeState == domain.ArenaSeriesStateDraft) {
			return ErrNormalPauseGraphIncomplete
		}
	}
	return nil
}

func pauseDraftStateMatchesSeries(draft DraftExecution, series PauseSeries) bool {
	switch draft.State {
	case DraftExecutionStateActive:
		return series.Execution.Series.State == domain.ArenaSeriesStateDraft
	case DraftExecutionStatePaused:
		return series.Execution.Series.State == domain.ArenaSeriesStateTechnicalPause && series.Execution.ResumeState != nil &&
			*series.Execution.ResumeState == domain.ArenaSeriesStateDraft
	case DraftExecutionStateRecoveryRequired:
		return series.Execution.Series.State == domain.ArenaSeriesStateDraft
	case DraftExecutionStateCompleted, DraftExecutionStateSuperseded:
		return true
	default:
		return false
	}
}

func validatePausePresenceSet(graph PauseGraph, index pauseGraphIndex) error {
	seen := make(map[uuid.UUID]struct{}, len(graph.Presence))
	seenIDs := make(map[uuid.UUID]struct{}, len(graph.Presence))
	for _, presence := range graph.Presence {
		if validatePausePresence(presence) != nil || presence.TournamentID != graph.Scope.TournamentID || presence.RosterID != graph.Scope.RosterID {
			return normalPauseError("invalid Presence descendant")
		}
		seriesID, exists := index.participantSeries[presence.ParticipantID]
		if !exists || presence.SeriesID != seriesID {
			return ErrNormalPauseGraphIncomplete
		}
		if _, duplicate := seen[presence.ParticipantID]; duplicate {
			return normalPauseError("duplicate Presence descendant")
		}
		if _, duplicate := seenIDs[presence.ID]; duplicate {
			return normalPauseError("duplicate Presence row")
		}
		seen[presence.ParticipantID] = struct{}{}
		seenIDs[presence.ID] = struct{}{}
		index.presenceByParticipant[presence.ParticipantID] = presence
	}
	if len(seen) != len(index.participantSeries) {
		return ErrNormalPauseGraphIncomplete
	}
	return nil
}

type reconnectCounterIdentity struct {
	PauseID       uuid.UUID
	RosterID      uuid.UUID
	ParticipantID uuid.UUID
}

func validateReconnectSet(graph PauseGraph, index pauseGraphIndex) error {
	counters, err := validateReconnectCounters(graph, index)
	if err != nil {
		return err
	}
	counts, err := validateReconnectIntervals(graph, index, counters)
	if err != nil {
		return err
	}
	for key, counter := range counters {
		if counts[key] != counter.Used {
			return ErrNormalPauseGraphIncomplete
		}
	}
	return nil
}

func validateReconnectCounters(graph PauseGraph, index pauseGraphIndex) (map[reconnectCounterIdentity]PauseReconnectCounter, error) {
	counters := make(map[reconnectCounterIdentity]PauseReconnectCounter, len(graph.Counters))
	for _, counter := range graph.Counters {
		key := reconnectCounterIdentity{PauseID: counter.PauseID, RosterID: counter.RosterID, ParticipantID: counter.ParticipantID}
		if !validPauseReconnectCounter(counter) || counter.RosterID != graph.Scope.RosterID {
			return nil, normalPauseError("invalid reconnect counter")
		}
		if _, exists := index.participantSeries[counter.ParticipantID]; !exists {
			return nil, ErrNormalPauseGraphIncomplete
		}
		if _, duplicate := counters[key]; duplicate {
			return nil, normalPauseError("duplicate reconnect counter")
		}
		counters[key] = counter
	}
	return counters, nil
}

type reconnectLogicalSegment struct {
	number       int
	continuation int
}

func validateReconnectIntervals(graph PauseGraph, index pauseGraphIndex, counters map[reconnectCounterIdentity]PauseReconnectCounter) (map[reconnectCounterIdentity]int, error) {
	counts := make(map[reconnectCounterIdentity]int, len(counters))
	byID := make(map[uuid.UUID]PauseReconnectInterval, len(graph.Reconnect))
	seenSegments := make(map[reconnectCounterIdentity]map[reconnectLogicalSegment]struct{}, len(counters))
	for _, interval := range graph.Reconnect {
		key, err := validateReconnectIntervalMembership(graph, index, counters, interval)
		if err != nil {
			return nil, err
		}
		if err := recordReconnectInterval(byID, seenSegments, key, interval); err != nil {
			return nil, err
		}
		if interval.ContinuationNumber == 0 {
			counts[key]++
		}
	}
	if err := validateReconnectLineage(graph.Reconnect, byID); err != nil {
		return nil, err
	}
	return counts, nil
}

func validateReconnectIntervalMembership(
	graph PauseGraph,
	index pauseGraphIndex,
	counters map[reconnectCounterIdentity]PauseReconnectCounter,
	interval PauseReconnectInterval,
) (reconnectCounterIdentity, error) {
	key := reconnectCounterIdentity{PauseID: interval.PauseID, RosterID: interval.RosterID, ParticipantID: interval.ParticipantID}
	if validatePauseReconnect(interval) != nil {
		return key, normalPauseError("invalid Reconnect descendant")
	}
	presence, participantExists := index.presenceByParticipant[interval.ParticipantID]
	game, gameExists := index.gamesByID[interval.GameID]
	counter, counterExists := counters[key]
	if interval.RosterID != graph.Scope.RosterID || !participantExists || !gameExists || !counterExists ||
		presence.SeriesID != interval.SeriesID || presence.PresenceEpoch < interval.PresenceEpoch ||
		game.SeriesID != interval.SeriesID || interval.Number > counter.Used {
		return key, ErrNormalPauseGraphIncomplete
	}
	return key, nil
}

func recordReconnectInterval(
	byID map[uuid.UUID]PauseReconnectInterval,
	seen map[reconnectCounterIdentity]map[reconnectLogicalSegment]struct{},
	key reconnectCounterIdentity,
	interval PauseReconnectInterval,
) error {
	if _, duplicate := byID[interval.ID]; duplicate {
		return normalPauseError("duplicate Reconnect descendant")
	}
	byID[interval.ID] = interval
	if seen[key] == nil {
		seen[key] = make(map[reconnectLogicalSegment]struct{})
	}
	segment := reconnectLogicalSegment{number: interval.Number, continuation: interval.ContinuationNumber}
	if _, duplicate := seen[key][segment]; duplicate {
		return normalPauseError("duplicate Reconnect segment")
	}
	seen[key][segment] = struct{}{}
	return nil
}

func validateReconnectLineage(values []PauseReconnectInterval, byID map[uuid.UUID]PauseReconnectInterval) error {
	continued := make(map[uuid.UUID]uuid.UUID, len(values))
	for _, interval := range values {
		if interval.ContinuationNumber == 0 {
			if err := validateReconnectRoot(interval); err != nil {
				return err
			}
			continue
		}
		if err := validateReconnectContinuation(interval, byID, continued); err != nil {
			return err
		}
	}
	return nil
}

func validateReconnectRoot(interval PauseReconnectInterval) error {
	if interval.ContinuedFromID != nil {
		return normalPauseError("root Reconnect has predecessor")
	}
	return nil
}

func validateReconnectContinuation(
	interval PauseReconnectInterval,
	byID map[uuid.UUID]PauseReconnectInterval,
	continued map[uuid.UUID]uuid.UUID,
) error {
	if interval.ContinuedFromID == nil || *interval.ContinuedFromID == interval.ID {
		return normalPauseError("continuation Reconnect lacks predecessor")
	}
	predecessorID := *interval.ContinuedFromID
	if _, fork := continued[predecessorID]; fork {
		return normalPauseError("Reconnect predecessor has multiple continuations")
	}
	continued[predecessorID] = interval.ID
	predecessor, exists := byID[predecessorID]
	if !exists || !validReconnectPredecessor(predecessor) || !sameReconnectLineage(predecessor, interval) {
		return normalPauseError("invalid Reconnect continuation lineage")
	}
	if !validReconnectContinuationTime(predecessor, interval) {
		return normalPauseError("invalid Reconnect continuation deadline")
	}
	return nil
}

func validReconnectPredecessor(value PauseReconnectInterval) bool {
	return value.State == ReconnectStateCancelled && value.SuspendedByPauseID != nil &&
		*value.SuspendedByPauseID != uuid.Nil && value.ClosedAt != nil
}

func sameReconnectLineage(predecessor, interval PauseReconnectInterval) bool {
	return predecessor.PauseID == interval.PauseID && predecessor.RosterID == interval.RosterID &&
		predecessor.SeriesID == interval.SeriesID && predecessor.GameID == interval.GameID &&
		predecessor.ParticipantID == interval.ParticipantID && predecessor.PresenceEpoch == interval.PresenceEpoch &&
		predecessor.Number == interval.Number && predecessor.ContinuationNumber+1 == interval.ContinuationNumber
}

func validReconnectContinuationTime(predecessor, interval PauseReconnectInterval) bool {
	if predecessor.ClosedAt == nil || !predecessor.Deadline.After(*predecessor.ClosedAt) ||
		!interval.OpenedAt.After(*predecessor.ClosedAt) {
		return false
	}
	deadline, ok := safePauseTimeAdd(interval.OpenedAt, predecessor.Deadline.Sub(*predecessor.ClosedAt))
	return ok && interval.Deadline.Equal(deadline)
}

func validPauseReconnectCounter(counter PauseReconnectCounter) bool {
	return counter.PauseID != uuid.Nil && counter.RosterID != uuid.Nil && counter.ParticipantID != uuid.Nil &&
		counter.Limit >= 1 && counter.Used >= 0 && counter.Used <= counter.Limit && counter.Revision >= 1
}

func validatePausePresence(value PausePresence) error {
	if !validPausePresenceHeader(value) {
		return normalPauseError("invalid Presence identity or revision")
	}
	return validatePausePresenceState(value)
}

func validPausePresenceHeader(value PausePresence) bool {
	return value.ID != uuid.Nil && value.TournamentID != uuid.Nil && value.RosterID != uuid.Nil &&
		value.SeriesID != uuid.Nil && value.ParticipantID != uuid.Nil && value.PresenceEpoch >= 1 &&
		value.Revision >= 1 && validArenaServerTime(value.ConnectedAt) && validArenaServerTime(value.UpdatedAt) &&
		!value.UpdatedAt.Before(value.ConnectedAt)
}

func validatePausePresenceState(value PausePresence) error {
	switch value.State {
	case PresenceStateConnected:
		if value.DisconnectedAt != nil {
			return normalPauseError("connected Presence has disconnect time")
		}
	case PresenceStateDisconnected:
		if value.DisconnectedAt == nil || !validArenaServerTime(*value.DisconnectedAt) || value.DisconnectedAt.Before(value.ConnectedAt) || value.UpdatedAt.Before(*value.DisconnectedAt) {
			return normalPauseError("disconnected Presence lacks valid time")
		}
	default:
		return normalPauseError("unknown Presence state %q", value.State)
	}
	return nil
}

func validatePauseReconnect(value PauseReconnectInterval) error {
	if !validPauseReconnectHeader(value) {
		return normalPauseError("invalid Reconnect identity or interval")
	}
	return validatePauseReconnectState(value)
}

func validPauseReconnectHeader(value PauseReconnectInterval) bool {
	return value.ID != uuid.Nil && value.PauseID != uuid.Nil && value.RosterID != uuid.Nil &&
		value.SeriesID != uuid.Nil && value.GameID != uuid.Nil && value.ParticipantID != uuid.Nil &&
		value.PresenceEpoch >= 1 && value.Number >= 1 && value.ContinuationNumber >= 0 && value.Revision >= 1 &&
		validArenaServerTime(value.OpenedAt) && validArenaServerTime(value.Deadline) &&
		validArenaServerTime(value.UpdatedAt) && value.Deadline.After(value.OpenedAt) &&
		!value.UpdatedAt.Before(value.OpenedAt)
}

func validatePauseReconnectState(value PauseReconnectInterval) error {
	switch value.State {
	case ReconnectStateOpen:
		if value.ClosedAt != nil || value.SuspendedByPauseID != nil {
			return normalPauseError("open Reconnect has close time")
		}
	case ReconnectStateReconnected, ReconnectStateExpired, ReconnectStateCancelled:
		if value.ClosedAt == nil || !validArenaServerTime(*value.ClosedAt) || value.ClosedAt.Before(value.OpenedAt) ||
			value.UpdatedAt.Before(*value.ClosedAt) {
			return normalPauseError("terminal Reconnect lacks close time")
		}
	default:
		return normalPauseError("unknown Reconnect state %q", value.State)
	}
	if value.SuspendedByPauseID != nil {
		if *value.SuspendedByPauseID == uuid.Nil {
			return normalPauseError("Reconnect suspension identity is empty")
		}
		if value.State != ReconnectStateCancelled {
			return normalPauseError("only cancelled Reconnect can carry suspension")
		}
	}
	return nil
}

func pauseTimeCoversGraphHistory(graph PauseGraph, at time.Time) bool {
	return timeCoversPauseRootHistory(at, graph) && timeCoversReadyWindowHistory(at, graph.Wave.Wave.ReadyWindow) &&
		timeCoversPresenceSetHistory(at, graph.Presence) && timeCoversReconnectSetHistory(at, graph.Reconnect) &&
		timeCoversOptionalDraftHistory(at, graph.Draft) && timeCoversFrozenDeadlineHistory(at, graph.FrozenDeadlines)
}

func timeCoversPauseRootHistory(at time.Time, graph PauseGraph) bool {
	return timeAtOrBefore(at, graph.Tournament.CreatedAt) && timeAtOrBefore(at, graph.Tournament.UpdatedAt) &&
		timePointerAtOrBefore(at, graph.Tournament.StartedAt) && timePointerAtOrBefore(at, graph.Tournament.FinishedAt) &&
		timePointerAtOrBefore(at, graph.Wave.Wave.StartedAt) && timePointerAtOrBefore(at, graph.Wave.Wave.PausedAt)
}

func timeCoversReadyWindowHistory(at time.Time, window *domain.ArenaReadyWindow) bool {
	return window == nil || (timeAtOrBefore(at, window.OpenedAt) && timePointerAtOrBefore(at, window.ConsumedAt))
}

func timeCoversPresenceSetHistory(at time.Time, values []PausePresence) bool {
	for _, presence := range values {
		if !timeCoversPresenceHistory(at, presence) {
			return false
		}
	}
	return true
}

func timeCoversReconnectSetHistory(at time.Time, values []PauseReconnectInterval) bool {
	for _, interval := range values {
		if !timeCoversReconnectHistory(at, interval) {
			return false
		}
	}
	return true
}

func timeCoversOptionalDraftHistory(at time.Time, draft *DraftExecution) bool {
	return draft == nil || timeCoversDraftHistory(at, *draft)
}

func timeCoversFrozenDeadlineHistory(at time.Time, values []PauseFrozenDeadline) bool {
	for _, frozen := range values {
		if !timeAtOrBefore(at, frozen.FrozenAt) || !timePointerAtOrBefore(at, frozen.ResumedAt) {
			return false
		}
	}
	return true
}

func timeAtOrBefore(at, event time.Time) bool {
	return event.IsZero() || !at.Before(event)
}

func safePauseTimeAdd(at time.Time, duration time.Duration) (time.Time, bool) {
	if duration <= 0 || !validArenaServerTime(at) {
		return time.Time{}, false
	}
	nanos := at.UnixNano()
	if !time.Unix(0, nanos).UTC().Equal(at) || nanos > math.MaxInt64-int64(duration) {
		return time.Time{}, false
	}
	result := time.Unix(0, nanos+int64(duration)).UTC()
	return result, validArenaServerTime(result) && result.After(at)
}

func timePointerAtOrBefore(at time.Time, event *time.Time) bool {
	return event == nil || timeAtOrBefore(at, *event)
}

func timeCoversDraftHistory(at time.Time, draft DraftExecution) bool {
	if !timeAtOrBefore(at, draft.FirstActorDecision.DecidedAt) {
		return false
	}
	for _, action := range draft.Actions {
		if !timeAtOrBefore(at, action.OccurredAt) ||
			(action.DecisionEvidence != nil && !timeAtOrBefore(at, action.DecisionEvidence.DecidedAt)) {
			return false
		}
	}
	if draft.Recovery != nil && !timeAtOrBefore(at, draft.Recovery.RecordedAt) {
		return false
	}
	return draft.Transition == nil || timeAtOrBefore(at, draft.Transition.OccurredAt)
}

func timeCoversPresenceHistory(at time.Time, presence PausePresence) bool {
	if at.Before(presence.ConnectedAt) || at.Before(presence.UpdatedAt) {
		return false
	}
	return presence.DisconnectedAt == nil || !at.Before(*presence.DisconnectedAt)
}

func timeCoversReconnectHistory(at time.Time, interval PauseReconnectInterval) bool {
	if at.Before(interval.OpenedAt) || at.Before(interval.UpdatedAt) {
		return false
	}
	return interval.ClosedAt == nil || !at.Before(*interval.ClosedAt)
}

func validateFrozenDeadline(value PauseFrozenDeadline, active bool) error {
	if !validFrozenDeadlineHeader(value) {
		return normalPauseError("invalid frozen deadline")
	}
	if !value.Kind.isValid() {
		return normalPauseError("unknown frozen deadline kind")
	}
	if active {
		if value.ResumedAt != nil || value.ResumedDeadline != nil {
			return normalPauseError("active frozen deadline has resume evidence")
		}
		return nil
	}
	expectedDeadline, ok := safePauseTimeAddPointer(value.ResumedAt, value.Remaining)
	if value.ResumedAt == nil || value.ResumedDeadline == nil || !validArenaServerTime(*value.ResumedAt) ||
		value.ResumedAt.Before(value.FrozenAt) || !validArenaServerTime(*value.ResumedDeadline) || !ok ||
		!value.ResumedDeadline.Equal(expectedDeadline) {
		return normalPauseError("resolved deadline lacks shifted evidence")
	}
	return nil
}

func safePauseTimeAddPointer(at *time.Time, duration time.Duration) (time.Time, bool) {
	if at == nil {
		return time.Time{}, false
	}
	return safePauseTimeAdd(*at, duration)
}

func validateFrozenDeadlineSet(graph PauseGraph, paused bool) error {
	if !paused && len(graph.FrozenDeadlines) == 0 {
		return nil
	}
	expected := eligiblePauseDeadlines(graph, paused)
	seen := make(map[pauseDeadlineIdentity]struct{}, len(graph.FrozenDeadlines))
	for _, frozen := range graph.FrozenDeadlines {
		if err := validateFrozenDeadline(frozen, paused); err != nil {
			return err
		}
		key := pauseDeadlineIdentity{Kind: frozen.Kind, OwnerID: frozen.OwnerID}
		deadline, exists := expected[key]
		if !exists {
			return ErrNormalPauseGraphIncomplete
		}
		if _, duplicate := seen[key]; duplicate {
			return normalPauseError("duplicate frozen deadline")
		}
		if paused {
			if graph.PausedAt == nil || !frozen.FrozenAt.Equal(*graph.PausedAt) || (!deadline.IsZero() && !frozen.OriginalDeadline.Equal(deadline)) {
				return ErrNormalPauseGraphIncomplete
			}
		} else if frozen.ResumedDeadline == nil || !deadline.Equal(*frozen.ResumedDeadline) {
			return ErrNormalPauseGraphIncomplete
		}
		seen[key] = struct{}{}
	}
	if len(seen) != len(expected) {
		return ErrNormalPauseGraphIncomplete
	}
	return nil
}

func eligiblePauseDeadlines(graph PauseGraph, paused bool) map[pauseDeadlineIdentity]time.Time {
	expected := make(map[pauseDeadlineIdentity]time.Time)
	addEligibleWaveDeadline(expected, graph)
	addEligibleGameDeadlines(expected, graph.Games, paused)
	addEligibleDraftDeadline(expected, graph.Draft, paused)
	return expected
}

func addEligibleWaveDeadline(expected map[pauseDeadlineIdentity]time.Time, graph PauseGraph) {
	if graph.Wave.Wave.ReadyWindow != nil &&
		(graph.Wave.Wave.State == domain.ArenaWaveStateReadyWindowOpen || graph.Wave.Wave.State == domain.ArenaWaveStateReady) {
		window := graph.Wave.Wave.ReadyWindow
		expected[pauseDeadlineIdentity{Kind: PauseDeadlineReadyWindow, OwnerID: window.ID}] = window.Deadline
	}
}

func addEligibleGameDeadlines(expected map[pauseDeadlineIdentity]time.Time, games []PauseGame, paused bool) {
	for _, game := range games {
		if paused && game.Game.State == domain.ArenaGameStatePaused && game.ResumeState != nil && *game.ResumeState == domain.ArenaGameStateActive {
			expected[pauseDeadlineIdentity{Kind: PauseDeadlineGame, OwnerID: game.Game.ID}] = time.Time{}
		}
		if !paused && game.Game.State == domain.ArenaGameStateActive && game.Deadline != nil {
			expected[pauseDeadlineIdentity{Kind: PauseDeadlineGame, OwnerID: game.Game.ID}] = *game.Deadline
		}
	}
}

func addEligibleDraftDeadline(expected map[pauseDeadlineIdentity]time.Time, draft *DraftExecution, paused bool) {
	if draft != nil {
		if paused && draft.State == DraftExecutionStatePaused && draft.Recovery != nil {
			expected[pauseDeadlineIdentity{Kind: PauseDeadlineDraft, OwnerID: draft.ID}] = draft.Recovery.PreviousDeadline
		}
		if !paused && draft.State == DraftExecutionStateActive && draft.AbsoluteDeadline != nil {
			expected[pauseDeadlineIdentity{Kind: PauseDeadlineDraft, OwnerID: draft.ID}] = *draft.AbsoluteDeadline
		}
	}
}

func validFrozenDeadlineHeader(value PauseFrozenDeadline) bool {
	return value.OwnerID != uuid.Nil && value.Revision >= 1 && validArenaServerTime(value.OriginalDeadline) &&
		validArenaServerTime(value.FrozenAt) && value.Remaining > 0 && value.Remaining <= maxFrozenPauseDuration &&
		value.OriginalDeadline.Equal(value.FrozenAt.Add(value.Remaining))
}

func (kind PauseDeadlineKind) isValid() bool {
	return kind == PauseDeadlineReadyWindow || kind == PauseDeadlineGame || kind == PauseDeadlineDraft
}

func validatePauseGraphRevisions(value PauseGraphRevisions) error {
	if !validPauseRootRevisions(value) {
		return normalPauseError("invalid root revisions")
	}
	if !validUniqueChildRevisions(value.Series) || !validUniqueChildRevisions(value.Games) || !validUniqueChildRevisions(value.Reconnect) {
		return normalPauseError("invalid or duplicate child revision")
	}
	if err := validatePausePresenceRevisions(value.Presence); err != nil {
		return err
	}
	if !validPauseDraftRevisionContract(value.Draft, value.DraftPreviousRevisionID) {
		return normalPauseError("invalid Draft revision")
	}
	if !validCounterRevisions(value.Counters) || !validFrozenDeadlineRevisions(value.FrozenDeadlines) || value.TerminalActionRevision < 0 {
		return normalPauseError("invalid aggregate revision")
	}
	return nil
}

func validPauseRootRevisions(value PauseGraphRevisions) bool {
	return value.GraphRevision >= 1 && normalPauseTournamentState(value.TournamentState) &&
		value.TournamentRevision >= 1 && value.WaveRevision >= 1 && len(value.Series) > 0
}

func normalPauseTournamentState(value domain.ArenaTournamentState) bool {
	return value == domain.ArenaTournamentStateSwiss || value == domain.ArenaTournamentStatePlayoffs
}

func validatePausePresenceRevisions(values []PausePresenceRevision) error {
	seenPresence := make(map[uuid.UUID]struct{}, len(values))
	for _, presence := range values {
		if presence.ID == uuid.Nil || presence.TournamentID == uuid.Nil || presence.RosterID == uuid.Nil ||
			presence.SeriesID == uuid.Nil || presence.ParticipantID == uuid.Nil || presence.PresenceEpoch < 1 || presence.Revision < 1 {
			return normalPauseError("invalid Presence revision")
		}
		if _, duplicate := seenPresence[presence.ParticipantID]; duplicate {
			return normalPauseError("duplicate Presence revision")
		}
		seenPresence[presence.ParticipantID] = struct{}{}
	}
	return nil
}

func validPauseDraftRevision(value DraftRevisionExpectation) bool {
	return value.RevisionID != uuid.Nil && value.Revision >= 1 && value.ServiceEpoch != uuid.Nil
}

func validPauseDraftRevisionContract(expected *DraftRevisionExpectation, previousRevisionID uuid.UUID) bool {
	if expected == nil {
		return previousRevisionID == uuid.Nil
	}
	return validPauseDraftRevision(*expected) && validDraftPreviousRevision(*expected, previousRevisionID)
}

func validDraftPreviousRevision(expected DraftRevisionExpectation, previousRevisionID uuid.UUID) bool {
	if expected.Revision == 1 {
		return previousRevisionID == uuid.Nil
	}
	return previousRevisionID != uuid.Nil && previousRevisionID != expected.RevisionID &&
		previousRevisionID != expected.ServiceEpoch
}

func validUniqueChildRevisions(values []PauseChildRevision) bool {
	seen := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		if value.ID == uuid.Nil || value.Revision < 1 {
			return false
		}
		if _, duplicate := seen[value.ID]; duplicate {
			return false
		}
		seen[value.ID] = struct{}{}
	}
	return true
}

func validCounterRevisions(values []PauseReconnectCounterRevision) bool {
	seen := make(map[[3]uuid.UUID]struct{}, len(values))
	for _, value := range values {
		key := [3]uuid.UUID{value.PauseID, value.RosterID, value.ParticipantID}
		if value.PauseID == uuid.Nil || value.RosterID == uuid.Nil || value.ParticipantID == uuid.Nil || value.Revision < 1 {
			return false
		}
		if _, duplicate := seen[key]; duplicate {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}

func validFrozenDeadlineRevisions(values []PauseFrozenDeadlineRevision) bool {
	seen := make(map[pauseDeadlineIdentity]struct{}, len(values))
	for _, value := range values {
		key := pauseDeadlineIdentity{Kind: value.Kind, OwnerID: value.OwnerID}
		if !value.Kind.isValid() || value.OwnerID == uuid.Nil || value.Revision < 1 {
			return false
		}
		if _, duplicate := seen[key]; duplicate {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}

func pauseGraphRevisionsEqual(first, second PauseGraphRevisions) bool {
	return revisionMapEqual(first.Series, second.Series) && revisionMapEqual(first.Games, second.Games) &&
		revisionMapEqual(first.Reconnect, second.Reconnect) && presenceRevisionMapEqual(first.Presence, second.Presence) &&
		counterRevisionMapEqual(first.Counters, second.Counters) && frozenRevisionMapEqual(first.FrozenDeadlines, second.FrozenDeadlines) &&
		first.GraphRevision == second.GraphRevision && first.TournamentRevision == second.TournamentRevision &&
		first.TournamentState == second.TournamentState && first.WaveRevision == second.WaveRevision &&
		first.DraftPreviousRevisionID == second.DraftPreviousRevisionID &&
		first.TerminalActionRevision == second.TerminalActionRevision && reflect.DeepEqual(first.Draft, second.Draft)
}

func counterRevisionMapEqual(first, second []PauseReconnectCounterRevision) bool {
	if len(first) != len(second) {
		return false
	}
	values := make(map[[3]uuid.UUID]int64, len(first))
	for _, value := range first {
		values[[3]uuid.UUID{value.PauseID, value.RosterID, value.ParticipantID}] = value.Revision
	}
	for _, value := range second {
		if values[[3]uuid.UUID{value.PauseID, value.RosterID, value.ParticipantID}] != value.Revision {
			return false
		}
	}
	return true
}

func frozenRevisionMapEqual(first, second []PauseFrozenDeadlineRevision) bool {
	if len(first) != len(second) {
		return false
	}
	values := make(map[pauseDeadlineIdentity]int64, len(first))
	for _, value := range first {
		values[pauseDeadlineIdentity{Kind: value.Kind, OwnerID: value.OwnerID}] = value.Revision
	}
	for _, value := range second {
		if values[pauseDeadlineIdentity{Kind: value.Kind, OwnerID: value.OwnerID}] != value.Revision {
			return false
		}
	}
	return true
}

func revisionMapEqual(first, second []PauseChildRevision) bool {
	if len(first) != len(second) {
		return false
	}
	values := make(map[uuid.UUID]int64, len(first))
	for _, value := range first {
		values[value.ID] = value.Revision
	}
	for _, value := range second {
		if values[value.ID] != value.Revision {
			return false
		}
	}
	return true
}

func presenceRevisionMapEqual(first, second []PausePresenceRevision) bool {
	if len(first) != len(second) {
		return false
	}
	values := make(map[uuid.UUID]PausePresenceRevision, len(first))
	for _, value := range first {
		values[value.ParticipantID] = value
	}
	for _, value := range second {
		if values[value.ParticipantID] != value {
			return false
		}
	}
	return true
}

func validPauseGraphScope(scope PauseGraphScope) bool {
	return scope.TournamentID != uuid.Nil && scope.RosterID != uuid.Nil && scope.WaveID != uuid.Nil &&
		scope.Authority.Validate() == nil && scope.Authority.TournamentID == scope.TournamentID
}

func (reason PauseReason) allowsNormalPause() bool {
	return reason == PauseReasonOperator || reason == PauseReasonPlatform || reason == PauseReasonExecutionEpoch
}

func replaceSeriesGame(series *domain.ArenaSeries, replacement domain.ArenaGame) bool {
	for slotIndex := range series.Slots {
		for gameIndex := range series.Slots[slotIndex].Attempts {
			if series.Slots[slotIndex].Attempts[gameIndex].ID == replacement.ID {
				series.Slots[slotIndex].Attempts[gameIndex] = cloneArenaGame(replacement)
				return true
			}
		}
	}
	return false
}

func clonePauseGraph(value PauseGraph) PauseGraph {
	clone := value
	clone.Tournament = *cloneArenaTournamentRecord(value.Tournament)
	clone.Wave.Wave = cloneArenaWaveExecution(value.Wave.Wave)
	clone.Series = make([]PauseSeries, len(value.Series))
	for index := range value.Series {
		clone.Series[index] = value.Series[index]
		clone.Series[index].Execution = cloneSeriesExecution(value.Series[index].Execution)
		clone.Series[index].CurrentGameID = cloneUUIDPointer(value.Series[index].CurrentGameID)
	}
	clone.Games = make([]PauseGame, len(value.Games))
	for index := range value.Games {
		clone.Games[index] = value.Games[index]
		clone.Games[index].Game = cloneArenaGame(value.Games[index].Game)
		clone.Games[index].Deadline = cloneTimePointer(value.Games[index].Deadline)
		clone.Games[index].ResumeState = cloneGameStatePointer(value.Games[index].ResumeState)
	}
	if value.Draft != nil {
		draft := cloneDraftExecution(*value.Draft)
		clone.Draft = &draft
	}
	clone.Presence = clonePausePresenceSlice(value.Presence)
	clone.Reconnect = clonePauseReconnectSlice(value.Reconnect)
	clone.Counters = clonePauseSlice(value.Counters)
	clone.FrozenDeadlines = clonePauseFrozenDeadlineSlice(value.FrozenDeadlines)
	clone.PausedAt = cloneTimePointer(value.PausedAt)
	return clone
}

func clonePausePresenceSlice(values []PausePresence) []PausePresence {
	clone := clonePauseSlice(values)
	for index := range clone {
		clone[index].DisconnectedAt = cloneTimePointer(values[index].DisconnectedAt)
	}
	return clone
}

func clonePauseReconnectSlice(values []PauseReconnectInterval) []PauseReconnectInterval {
	clone := clonePauseSlice(values)
	for index := range clone {
		clone[index].ClosedAt = cloneTimePointer(clone[index].ClosedAt)
		clone[index].ContinuedFromID = cloneUUIDPointer(clone[index].ContinuedFromID)
		clone[index].SuspendedByPauseID = cloneUUIDPointer(clone[index].SuspendedByPauseID)
	}
	return clone
}

func clonePauseFrozenDeadlineSlice(values []PauseFrozenDeadline) []PauseFrozenDeadline {
	clone := clonePauseSlice(values)
	for index := range clone {
		clone[index].ResumedAt = cloneTimePointer(values[index].ResumedAt)
		clone[index].ResumedDeadline = cloneTimePointer(values[index].ResumedDeadline)
	}
	return clone
}

func cloneNormalPauseRecord(value NormalPauseRecord) NormalPauseRecord {
	clone := value
	clone.Expected = clonePauseGraphRevisions(value.Expected)
	clone.Graph = clonePauseGraph(value.Graph)
	clone.SuspendedReconnect = clonePauseSlice(value.SuspendedReconnect)
	clone.ResolvedAt = cloneTimePointer(value.ResolvedAt)
	return clone
}

func clonePauseGraphRevisions(value PauseGraphRevisions) PauseGraphRevisions {
	clone := value
	clone.Series = clonePauseSlice(value.Series)
	clone.Games = clonePauseSlice(value.Games)
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

func clonePauseSlice[T any](value []T) []T {
	if value == nil {
		return nil
	}
	return append(make([]T, 0, len(value)), value...)
}

func cloneTournamentStatePointer(value *domain.ArenaTournamentState) *domain.ArenaTournamentState {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneGameStatePointer(value *domain.ArenaGameState) *domain.ArenaGameState {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func normalPauseError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidNormalPauseGraph, fmt.Sprintf(format, arguments...))
}
