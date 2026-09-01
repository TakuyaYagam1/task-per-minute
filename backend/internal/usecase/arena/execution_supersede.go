package arena

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var ErrInvalidExecutionSupersession = errors.New("invalid Arena execution supersession")

type ExecutionSupersessionAction string

const (
	ExecutionSupersedeGames ExecutionSupersessionAction = "supersede_games"
	ExecutionCancelSeries   ExecutionSupersessionAction = "cancel_series"
)

type ExecutionGameTerminalIntent struct {
	GameID           uuid.UUID
	ResultRevisionID domain.ArenaOfficialResultRevisionID
	SourceProjection domain.ArenaDerivedRevision
	AttemptRevision  ArenaAttemptRowRevision
}

type ExecutionSeriesTerminalIntent struct {
	ResultRevisionID domain.ArenaOfficialResultRevisionID
	ScoreRevisionID  domain.ArenaSeriesScoreRevisionID
	SourceProjection domain.ArenaDerivedRevision
	SeriesRevision   ArenaSeriesRowRevision
}

type ExecutionSupersessionCommand struct {
	CommandID      uuid.UUID
	Action         ExecutionSupersessionAction
	Series         domain.ArenaSeries
	SeriesRevision ArenaSeriesRowRevision
	GameIntents    []ExecutionGameTerminalIntent
	SeriesIntent   *ExecutionSeriesTerminalIntent
	Replacement    *domain.ArenaSeries
	RecordedAt     time.Time
}

type ExecutionSupersessionPlan struct {
	action       ExecutionSupersessionAction
	original     domain.ArenaSeries
	series       domain.ArenaSeries
	gameResults  []OfficialResultRevisionPlan
	seriesResult *OfficialResultRevisionPlan
	replacement  *domain.ArenaSeries
}

func PlanExecutionSupersession(command ExecutionSupersessionCommand) (ExecutionSupersessionPlan, error) {
	command = cloneExecutionSupersessionCommand(command)
	if err := validateExecutionSupersessionCommand(command); err != nil {
		return ExecutionSupersessionPlan{}, err
	}

	current := cloneRevisionArenaSeries(command.Series)
	gameResults := make([]OfficialResultRevisionPlan, 0, len(command.GameIntents))
	for _, intent := range command.GameIntents {
		persisted := cloneRevisionArenaSeries(current)
		projected := cloneRevisionArenaSeries(current)
		game, found := findArenaSeriesGamePointer(&projected, intent.GameID)
		if !found {
			return ExecutionSupersessionPlan{}, invalidExecutionSupersession("Game is missing", nil)
		}
		game.State, game.ResultReason = executionGameTerminal(command.Action)
		game.WinnerID = nil
		game.ResultRevisionID = cloneOfficialResultRevisionIDPointer(&intent.ResultRevisionID)

		scope := OfficialResultScope{
			TournamentID: command.Series.TournamentID,
			SeriesID:     command.Series.ID,
			GameID:       intent.GameID,
			Kind:         OfficialResultSubjectGame,
		}
		outcome := OfficialResultOutcome{GameState: game.State, GameReason: game.ResultReason}
		result, err := PlanOfficialResultRevision(OfficialResultRevisionCommand{
			Scope: scope, CommandID: command.CommandID, RevisionID: intent.ResultRevisionID,
			Actor:                    ArenaResultActor{Kind: ArenaResultActorServer},
			ExpectedSourceProjection: intent.SourceProjection, Outcome: outcome,
		}, OfficialResultRevisionAuthority{
			Scope: scope, PersistedSeries: persisted, ProjectedSeries: projected,
			SourceProjection: intent.SourceProjection,
			SeriesRevision:   command.SeriesRevision, AttemptRevision: intent.AttemptRevision,
		}, command.RecordedAt)
		if err != nil {
			return ExecutionSupersessionPlan{}, invalidExecutionSupersession("plan initial Game result", err)
		}
		gameResults = append(gameResults, result)
		current = projected
	}

	var seriesResult *OfficialResultRevisionPlan
	if command.Action == ExecutionCancelSeries {
		persisted := cloneRevisionArenaSeries(current)
		projected := cloneRevisionArenaSeries(current)
		projected.State = domain.ArenaSeriesStateCancelled
		projected.Score = domain.ArenaSeriesScore{}
		projected.WinnerID = nil
		projected.CurrentScoreRevisionID = cloneSeriesScoreRevisionIDPointer(
			&command.SeriesIntent.ScoreRevisionID,
		)
		projected.CurrentResultRevisionID = cloneOfficialResultRevisionIDPointer(
			&command.SeriesIntent.ResultRevisionID,
		)
		scope := OfficialResultScope{
			TournamentID: command.Series.TournamentID,
			SeriesID:     command.Series.ID,
			Kind:         OfficialResultSubjectSeries,
		}
		outcome := OfficialResultOutcome{
			SeriesState:     domain.ArenaSeriesStateCancelled,
			SeriesReason:    ArenaSeriesResultReasonSeriesCancelled,
			ScoreRevisionID: cloneSeriesScoreRevisionIDPointer(&command.SeriesIntent.ScoreRevisionID),
		}
		result, err := PlanOfficialResultRevision(OfficialResultRevisionCommand{
			Scope: scope, CommandID: command.CommandID,
			RevisionID:               command.SeriesIntent.ResultRevisionID,
			Actor:                    ArenaResultActor{Kind: ArenaResultActorServer},
			ExpectedSourceProjection: command.SeriesIntent.SourceProjection, Outcome: outcome,
		}, OfficialResultRevisionAuthority{
			Scope: scope, PersistedSeries: persisted, ProjectedSeries: projected,
			ProjectedSeriesReason: ArenaSeriesResultReasonSeriesCancelled,
			SourceProjection:      command.SeriesIntent.SourceProjection,
			SeriesRevision:        command.SeriesIntent.SeriesRevision,
		}, command.RecordedAt)
		if err != nil {
			return ExecutionSupersessionPlan{}, invalidExecutionSupersession("plan initial Series result", err)
		}
		seriesResult = &result
		current = projected
	}

	plan := ExecutionSupersessionPlan{
		action: command.Action, original: cloneRevisionArenaSeries(command.Series),
		series: current, gameResults: gameResults,
		seriesResult: seriesResult, replacement: cloneExecutionSeriesPointer(command.Replacement),
	}
	if err := plan.Validate(); err != nil {
		return ExecutionSupersessionPlan{}, err
	}
	return plan, nil
}

func (p ExecutionSupersessionPlan) Validate() error {
	if !validExecutionSupersessionPlanBase(p) {
		return invalidExecutionSupersession("invalid planned execution", nil)
	}
	if !validInitialExecutionGameResults(p.gameResults) {
		return invalidExecutionSupersession("invalid initial Game result revision", nil)
	}
	if err := validateExecutionSeriesResult(p); err != nil {
		return err
	}
	if p.replacement != nil {
		if err := validateExecutionReplacement(p.original, *p.replacement); err != nil {
			return err
		}
	}
	return nil
}

func validExecutionSupersessionPlanBase(plan ExecutionSupersessionPlan) bool {
	return plan.original.Validate() == nil && plan.series.Validate() == nil &&
		(plan.action == ExecutionSupersedeGames || plan.action == ExecutionCancelSeries) &&
		len(plan.gameResults) > 0
}

func validInitialExecutionGameResults(results []OfficialResultRevisionPlan) bool {
	for _, result := range results {
		if result.Validate() != nil || result.Revision().Ordinal() != 1 ||
			result.Revision().PreviousRevisionID() != nil {
			return false
		}
	}
	return true
}

func validateExecutionSeriesResult(plan ExecutionSupersessionPlan) error {
	if plan.action != ExecutionCancelSeries {
		if plan.seriesResult != nil {
			return invalidExecutionSupersession("Game supersession created a Series result", nil)
		}
		return nil
	}
	if plan.seriesResult == nil || plan.seriesResult.Validate() != nil ||
		plan.seriesResult.Revision().Ordinal() != 1 ||
		plan.seriesResult.Revision().PreviousRevisionID() != nil ||
		plan.series.State != domain.ArenaSeriesStateCancelled {
		return invalidExecutionSupersession("invalid initial Series result revision", nil)
	}
	return nil
}

func (p ExecutionSupersessionPlan) OriginalSeries() domain.ArenaSeries {
	return cloneRevisionArenaSeries(p.original)
}

func (p ExecutionSupersessionPlan) Series() domain.ArenaSeries {
	return cloneRevisionArenaSeries(p.series)
}

func (p ExecutionSupersessionPlan) GameResultRevisions() []OfficialResultRevisionPlan {
	return append([]OfficialResultRevisionPlan(nil), p.gameResults...)
}

func (p ExecutionSupersessionPlan) SeriesResultRevision() *OfficialResultRevisionPlan {
	if p.seriesResult == nil {
		return nil
	}
	clone := *p.seriesResult
	return &clone
}

func (p ExecutionSupersessionPlan) Replacement() *domain.ArenaSeries {
	return cloneExecutionSeriesPointer(p.replacement)
}

func validateExecutionSupersessionCommand(command ExecutionSupersessionCommand) error {
	if !validExecutionSupersessionAuthority(command) {
		return invalidExecutionSupersession("invalid command authority", nil)
	}
	if command.Action != ExecutionSupersedeGames && command.Action != ExecutionCancelSeries {
		return invalidExecutionSupersession("unknown terminal action", nil)
	}
	games := executionSeriesGames(command.Series)
	seen, err := validateExecutionGameIntents(command.GameIntents, games)
	if err != nil {
		return err
	}
	if err := validateExecutionSupersessionAction(command, games, seen); err != nil {
		return err
	}
	if command.Replacement != nil {
		return validateExecutionReplacement(command.Series, *command.Replacement)
	}
	return nil
}

func validExecutionSupersessionAuthority(command ExecutionSupersessionCommand) bool {
	return command.CommandID != uuid.Nil && command.SeriesRevision > 0 &&
		validArenaServerTime(command.RecordedAt) && command.Series.Validate() == nil &&
		len(command.GameIntents) > 0
}

func validateExecutionGameIntents(
	intents []ExecutionGameTerminalIntent,
	games map[uuid.UUID]domain.ArenaGame,
) (map[uuid.UUID]struct{}, error) {
	seen := make(map[uuid.UUID]struct{}, len(intents))
	for _, intent := range intents {
		game, found := games[intent.GameID]
		if !found || (game.State != domain.ArenaGameStatePlanned && game.State != domain.ArenaGameStateReady) ||
			intent.ResultRevisionID.IsZero() || intent.AttemptRevision <= 0 ||
			intent.SourceProjection.Validate() != nil {
			return nil, invalidExecutionSupersession("terminal Game is not unstarted or has invalid evidence", nil)
		}
		if _, duplicate := seen[intent.GameID]; duplicate {
			return nil, invalidExecutionSupersession("terminal Game is duplicated", nil)
		}
		seen[intent.GameID] = struct{}{}
	}
	return seen, nil
}

func validateExecutionSupersessionAction(
	command ExecutionSupersessionCommand,
	games map[uuid.UUID]domain.ArenaGame,
	seen map[uuid.UUID]struct{},
) error {
	switch command.Action {
	case ExecutionSupersedeGames:
		if command.SeriesIntent != nil {
			return invalidExecutionSupersession("Game supersession has Series result evidence", nil)
		}
	case ExecutionCancelSeries:
		if !executionSeriesCanBeWithdrawn(command.Series) ||
			command.SeriesIntent == nil || len(seen) != len(games) ||
			command.SeriesIntent.ResultRevisionID.IsZero() || command.SeriesIntent.ScoreRevisionID.IsZero() ||
			command.SeriesIntent.SeriesRevision <= 0 || command.SeriesIntent.SourceProjection.Validate() != nil {
			return invalidExecutionSupersession("Series cancellation coverage is incomplete", nil)
		}
	}
	return nil
}

func executionSeriesCanBeWithdrawn(series domain.ArenaSeries) bool {
	switch series.State {
	case domain.ArenaSeriesStatePlanned,
		domain.ArenaSeriesStateLocked,
		domain.ArenaSeriesStateDraft,
		domain.ArenaSeriesStateReady,
		domain.ArenaSeriesStateTechnicalPause:
		return series.Score == (domain.ArenaSeriesScore{}) && series.WinnerID == nil &&
			series.CurrentResultRevisionID == nil
	case domain.ArenaSeriesStateActive,
		domain.ArenaSeriesStateReplayRequired,
		domain.ArenaSeriesStateCompleted,
		domain.ArenaSeriesStateCancelled:
		return false
	default:
		return false
	}
}

func validateExecutionReplacement(original, replacement domain.ArenaSeries) error {
	if replacement.Validate() != nil || replacement.TournamentID != original.TournamentID ||
		(replacement.State != domain.ArenaSeriesStatePlanned && replacement.State != domain.ArenaSeriesStateReady) {
		return invalidExecutionSupersession("invalid replacement Series", nil)
	}
	reserved := executionSeriesIdentitySet(original)
	for id := range executionSeriesIdentitySet(replacement) {
		if _, reused := reserved[id]; reused {
			return invalidExecutionSupersession("replacement reused Series Slot or Game identity", nil)
		}
	}
	return nil
}

func executionSeriesGames(series domain.ArenaSeries) map[uuid.UUID]domain.ArenaGame {
	games := make(map[uuid.UUID]domain.ArenaGame)
	for _, slot := range series.Slots {
		for _, game := range slot.Attempts {
			games[game.ID] = game
		}
	}
	return games
}

func executionSeriesIdentitySet(series domain.ArenaSeries) map[uuid.UUID]struct{} {
	ids := map[uuid.UUID]struct{}{series.ID: {}}
	for _, slot := range series.Slots {
		ids[slot.ID] = struct{}{}
		for _, game := range slot.Attempts {
			ids[game.ID] = struct{}{}
		}
	}
	return ids
}

func executionGameTerminal(action ExecutionSupersessionAction) (domain.ArenaGameState, domain.ArenaGameResultReason) {
	if action == ExecutionCancelSeries {
		return domain.ArenaGameStateCancelled, domain.ArenaGameResultReasonSeriesCancelled
	}
	return domain.ArenaGameStateSuperseded, domain.ArenaGameResultReasonDerivedRevisionSuperseded
}

func cloneExecutionSupersessionCommand(command ExecutionSupersessionCommand) ExecutionSupersessionCommand {
	clone := command
	clone.Series = cloneRevisionArenaSeries(command.Series)
	clone.GameIntents = append([]ExecutionGameTerminalIntent(nil), command.GameIntents...)
	if command.SeriesIntent != nil {
		intent := *command.SeriesIntent
		clone.SeriesIntent = &intent
	}
	clone.Replacement = cloneExecutionSeriesPointer(command.Replacement)
	return clone
}

func cloneExecutionSeriesPointer(series *domain.ArenaSeries) *domain.ArenaSeries {
	if series == nil {
		return nil
	}
	clone := cloneRevisionArenaSeries(*series)
	return &clone
}

func invalidExecutionSupersession(detail string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s", ErrInvalidExecutionSupersession, detail)
	}
	return fmt.Errorf("%w: %s: %w", ErrInvalidExecutionSupersession, detail, cause)
}
