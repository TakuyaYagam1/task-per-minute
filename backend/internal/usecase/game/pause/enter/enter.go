package enter

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause/model"
)

type NormalPauseGraphUseCase struct {
	transactions TransactionManager
	repository   NormalPauseRepository
	clock        PauseClock
}

func NewNormalPauseGraphUseCase(transactions TransactionManager, repository NormalPauseRepository, clock PauseClock) *NormalPauseGraphUseCase {
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
	if !pauseValidServerTime(pausedAt) {
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
		command.ActorID == uuid.Nil || !model.AllowsNormalPause(command.Reason) ||
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
	expected *draftusecase.RevisionExpectation,
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
	if authority.ActiveGolden || authority.Graph.Tournament.State == domain.TournamentStateGolden {
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
	if authority.Graph.Tournament.State != domain.TournamentStateSwiss &&
		authority.Graph.Tournament.State != domain.TournamentStatePlayoffs {
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
	tournament := domain.Tournament{State: graph.Tournament.State, PausedFromState: graph.Tournament.PausedFromState}
	changed, err := tournament.TransitionTo(domain.TournamentStateTechnicalPause)
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
	graph.PausedAt = pauseCloneTimePointer(&pausedAt)
	graph.DeadlinesSuppressed = true
	record := NormalPauseRecord{
		Scope: command.Scope, ScopeKind: pausedomain.ScopeWave, ScopeID: command.Scope.WaveID,
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
		if interval.State != pausedomain.ReconnectStateOpen {
			continue
		}
		if interval.Revision == math.MaxInt64 {
			return nil, ErrNormalPauseOverflow
		}
		if !interval.OpenedAt.Before(pausedAt) || !interval.Deadline.After(pausedAt) || interval.SuspendedByPauseID != nil {
			return nil, ErrNormalPauseDeadline
		}
		suspended = append(suspended, PauseChildRevision{ID: interval.ID, Revision: interval.Revision})
		interval.State = pausedomain.ReconnectStateCancelled
		interval.ClosedAt = pauseCloneTimePointer(&pausedAt)
		interval.Revision++
		interval.UpdatedAt = pausedAt
		interval.SuspendedByPauseID = pauseCloneUUIDPointer(&normalPauseID)
	}
	return suspended, nil
}

func pauseWaveInGraph(graph *PauseGraph, pausedAt time.Time) error {
	wave := &graph.Wave
	switch wave.Wave.State {
	case domain.WaveStateActive:
		if wave.Revision == math.MaxInt64 {
			return ErrNormalPauseOverflow
		}
		if err := wave.Wave.Pause(pausedAt); err != nil {
			return normalPauseError("pause Wave: %v", err)
		}
		wave.Revision++
	case domain.WaveStateReadyWindowOpen, domain.WaveStateReady:
		if wave.Revision == math.MaxInt64 || wave.Wave.ReadyWindow == nil {
			return ErrNormalPauseOverflow
		}
		frozen, err := newFrozenDeadline(PauseDeadlineReadyWindow, wave.Wave.ReadyWindow.ID, wave.Wave.ReadyWindow.Deadline, pausedAt)
		if err != nil {
			return err
		}
		graph.FrozenDeadlines = append(graph.FrozenDeadlines, frozen)
		wave.Revision++
	case domain.WaveStatePlanned, domain.WaveStateCompleted,
		domain.WaveStateReadyWindowExpired, domain.WaveStateSuperseded:
		return nil
	case domain.WaveStatePaused:
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
		if hasAdoptedSourceCurrentGame(graph, series, gameIndexes) {
			continue
		}
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

func hasAdoptedSourceCurrentGame(graph *PauseGraph, series *PauseSeries, gameIndexes map[uuid.UUID]int) bool {
	if graph == nil || series == nil || series.Execution.Series.State != domain.SeriesStateActive || series.CurrentGameID == nil {
		return false
	}
	gameIndex, exists := gameIndexes[*series.CurrentGameID]
	if !exists {
		return false
	}
	game := graph.Games[gameIndex]
	return game.SeriesID == series.Execution.Series.ID && game.Game.ID == *series.CurrentGameID &&
		isAdoptedSourceGame(game)
}

func isAdoptedSourceGame(game PauseGame) bool {
	source := game.SourcePause
	return game.Game.State == domain.GameStatePaused && game.ResumeState == nil && game.Deadline == nil && source != nil &&
		source.PauseID != uuid.Nil && source.GameID == game.Game.ID && source.SeriesID == game.SeriesID &&
		source.State == PauseStateActive && source.Reason == PauseReasonDisconnect &&
		source.ParentPauseID == nil && source.Depth == 0
}

func pauseSeriesRecord(series *PauseSeries) (bool, error) {
	switch series.Execution.Series.State {
	case domain.SeriesStateDraft, domain.SeriesStateReady,
		domain.SeriesStateActive, domain.SeriesStateReplayRequired:
		if series.Revision == math.MaxInt64 {
			return false, ErrNormalPauseOverflow
		}
		next, changed, err := seriesdomain.Transition(series.Execution, seriesdomain.TransitionCommand{
			NextState: domain.SeriesStateTechnicalPause,
		})
		if err != nil || !changed {
			return false, normalPauseError("pause Series: %v", err)
		}
		series.Execution = next
		series.Revision++
		return true, nil
	case domain.SeriesStatePlanned, domain.SeriesStateLocked,
		domain.SeriesStateCompleted, domain.SeriesStateCancelled:
		return false, nil
	case domain.SeriesStateTechnicalPause:
		return false, normalPauseError("Series is already paused")
	default:
		return false, normalPauseError("unknown Series state %q", series.Execution.Series.State)
	}
}

func pauseSeriesCurrentGame(
	graph *PauseGraph,
	series *PauseSeries,
	origin domain.SeriesState,
	gameIndexes map[uuid.UUID]int,
	pausedAt time.Time,
) error {
	if series.CurrentGameID == nil {
		if origin == domain.SeriesStateActive {
			return ErrNormalPauseGraphIncomplete
		}
		return nil
	}
	gameIndex, ok := gameIndexes[*series.CurrentGameID]
	if !ok {
		return ErrNormalPauseGraphIncomplete
	}
	game := &graph.Games[gameIndex]
	if game.Game.State != domain.GameStateActive {
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
	game.Game.State = domain.GameStatePaused
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
	if graph.Draft == nil || graph.Draft.State != draftusecase.ExecutionStateActive {
		return nil
	}
	draft := draftusecase.CloneExecution(*graph.Draft)
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
	draftusecase.AdvanceRevision(&draft, command.DraftResultRevisionID, command.CommandID, draft.ServiceEpoch)
	draft.State = draftusecase.ExecutionStatePaused
	draft.AbsoluteDeadline = nil
	draft.PausedRemaining = frozen.Remaining
	draft.Recovery = &draftusecase.RecoveryEvidence{
		Policy: draftusecase.RecoveryPolicyShiftRemaining, Reason: draftusecase.RecoveryReasonOperatorPause,
		PreviousState: draftusecase.ExecutionStateActive, PreviousServiceEpoch: draft.ServiceEpoch,
		CurrentServiceEpoch: draft.ServiceEpoch, PreviousDeadline: frozen.OriginalDeadline,
		RecordedAt: pausedAt, ActorID: command.ActorID, Note: string(command.Reason),
	}
	draft.Transition = &draftusecase.TransitionEvidence{Operation: draftusecase.TransitionPause, ActorID: command.ActorID, Reason: string(command.Reason), OccurredAt: pausedAt}
	if err := draft.Validate(); err != nil {
		return normalPauseError("pause Draft: %v", err)
	}
	graph.Draft = &draft
	graph.FrozenDeadlines = append(graph.FrozenDeadlines, frozen)
	return nil
}

func newFrozenDeadline(kind PauseDeadlineKind, ownerID uuid.UUID, deadline, frozenAt time.Time) (PauseFrozenDeadline, error) {
	if ownerID == uuid.Nil || !pauseValidServerTime(deadline) || !deadline.After(frozenAt) {
		return PauseFrozenDeadline{}, ErrNormalPauseDeadline
	}
	remaining := deadline.Sub(frozenAt)
	if remaining <= 0 || remaining > maxFrozenPauseDuration {
		return PauseFrozenDeadline{}, ErrNormalPauseDeadline
	}
	return PauseFrozenDeadline{Kind: kind, OwnerID: ownerID, OriginalDeadline: deadline, FrozenAt: frozenAt, Remaining: remaining, Revision: 1}, nil
}
