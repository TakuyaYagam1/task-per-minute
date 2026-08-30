package arena

import (
	"errors"
	"fmt"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrInvalidCompetitiveSeriesResolution = errors.New("invalid competitive Series resolution")
	ErrCompetitiveSeriesDraw              = errors.New("competitive Series cannot end in a draw")
)

type CompetitiveSeriesRoute string

const (
	CompetitiveSeriesRouteFairResult             CompetitiveSeriesRoute = "fair_result"
	CompetitiveSeriesRouteSurrender              CompetitiveSeriesRoute = "surrender"
	CompetitiveSeriesRouteOperatorForfeit        CompetitiveSeriesRoute = "operator_forfeit"
	CompetitiveSeriesRouteDeadline               CompetitiveSeriesRoute = "deadline"
	CompetitiveSeriesRouteReplayExhausted        CompetitiveSeriesRoute = "replay_exhausted"
	CompetitiveSeriesRouteRecovery               CompetitiveSeriesRoute = "recovery"
	CompetitiveSeriesRouteCorrection             CompetitiveSeriesRoute = "correction"
	CompetitiveSeriesRouteTournamentCancellation CompetitiveSeriesRoute = "tournament_cancellation"
)

type CompetitiveSeriesResolutionCommand struct {
	Route    CompetitiveSeriesRoute
	Terminal *SeriesTerminalEvidence
}

var competitiveSeriesRouteStates = map[CompetitiveSeriesRoute]map[domain.ArenaSeriesState]struct{}{
	CompetitiveSeriesRouteFairResult: {
		domain.ArenaSeriesStateActive: {}, domain.ArenaSeriesStateTechnicalPause: {},
		domain.ArenaSeriesStateCompleted: {},
	},
	CompetitiveSeriesRouteSurrender: {
		domain.ArenaSeriesStateActive: {}, domain.ArenaSeriesStateTechnicalPause: {},
		domain.ArenaSeriesStateCompleted: {},
	},
	CompetitiveSeriesRouteOperatorForfeit: {
		domain.ArenaSeriesStateReady: {}, domain.ArenaSeriesStateActive: {},
		domain.ArenaSeriesStateTechnicalPause: {}, domain.ArenaSeriesStateCompleted: {},
	},
	CompetitiveSeriesRouteDeadline: {
		domain.ArenaSeriesStateActive: {}, domain.ArenaSeriesStateReplayRequired: {},
	},
	CompetitiveSeriesRouteCorrection: {
		domain.ArenaSeriesStateActive: {}, domain.ArenaSeriesStateReplayRequired: {},
	},
	CompetitiveSeriesRouteReplayExhausted: {
		domain.ArenaSeriesStateReplayRequired: {}, domain.ArenaSeriesStateTechnicalPause: {},
	},
	CompetitiveSeriesRouteRecovery: {
		domain.ArenaSeriesStateActive: {}, domain.ArenaSeriesStateReplayRequired: {},
		domain.ArenaSeriesStateTechnicalPause: {},
	},
	CompetitiveSeriesRouteTournamentCancellation: {
		domain.ArenaSeriesStatePlanned: {}, domain.ArenaSeriesStateLocked: {},
		domain.ArenaSeriesStateDraft: {}, domain.ArenaSeriesStateReady: {},
		domain.ArenaSeriesStateActive: {}, domain.ArenaSeriesStateReplayRequired: {},
		domain.ArenaSeriesStateTechnicalPause: {}, domain.ArenaSeriesStateCancelled: {},
	},
}

func ResolveCompetitiveSeries(
	current SeriesExecution,
	command CompetitiveSeriesResolutionCommand,
) (SeriesExecution, bool, error) {
	if err := current.Validate(); err != nil {
		return SeriesExecution{}, false, competitiveSeriesResolutionError("Series: %v", err)
	}
	if err := validateCompetitiveSeriesCommand(current, command); err != nil {
		return SeriesExecution{}, false, err
	}
	if current.Series.State.IsTerminal() {
		if competitiveTerminalResolutionMatches(current, command) {
			clone := cloneSeriesExecution(current)
			return clone, false, ValidateCompetitiveSeriesResolution(command.Route, clone)
		}
		return SeriesExecution{}, false, competitiveSeriesResolutionError("terminal Series differs from command")
	}

	nextState := competitiveSeriesNextState(command.Route)
	resolved, changed, err := TransitionSeriesExecution(current, SeriesExecutionTransitionCommand{
		NextState: nextState,
		Terminal:  cloneSeriesTerminalEvidence(command.Terminal),
	})
	if err != nil {
		return SeriesExecution{}, false, competitiveSeriesResolutionError("transition: %v", err)
	}
	if err := ValidateCompetitiveSeriesResolution(command.Route, resolved); err != nil {
		return SeriesExecution{}, false, err
	}
	return resolved, changed, nil
}

func ValidateCompetitiveSeriesResolution(
	route CompetitiveSeriesRoute,
	resolved SeriesExecution,
) error {
	if !route.IsValid() {
		return competitiveSeriesResolutionError("unknown route %q", route)
	}
	if err := resolved.Validate(); err != nil {
		return competitiveSeriesResolutionError("Series: %v", err)
	}

	validator, exists := competitiveSeriesResolutionValidator(resolved.Series.State)
	if !exists {
		return competitiveSeriesResolutionError("route left Series unresolved in %s", resolved.Series.State)
	}
	return validator(route, resolved)
}

func (r CompetitiveSeriesRoute) IsValid() bool {
	switch r {
	case CompetitiveSeriesRouteFairResult,
		CompetitiveSeriesRouteSurrender,
		CompetitiveSeriesRouteOperatorForfeit,
		CompetitiveSeriesRouteDeadline,
		CompetitiveSeriesRouteReplayExhausted,
		CompetitiveSeriesRouteRecovery,
		CompetitiveSeriesRouteCorrection,
		CompetitiveSeriesRouteTournamentCancellation:
		return true
	}
	return false
}

func (r CompetitiveSeriesRoute) hasWinner() bool {
	return r == CompetitiveSeriesRouteFairResult ||
		r == CompetitiveSeriesRouteSurrender ||
		r == CompetitiveSeriesRouteOperatorForfeit
}

func validateCompetitiveSeriesCommand(
	current SeriesExecution,
	command CompetitiveSeriesResolutionCommand,
) error {
	if !command.Route.IsValid() {
		return competitiveSeriesResolutionError("unknown route %q", command.Route)
	}
	if !competitiveSeriesRouteAllowsState(command.Route, current.Series.State) {
		return competitiveSeriesResolutionError("route %s is unavailable from %s", command.Route, current.Series.State)
	}
	if command.Route.hasWinner() {
		return validateCompetitiveWinnerEvidence(current.Series, command.Terminal)
	}
	if command.Route == CompetitiveSeriesRouteTournamentCancellation {
		return validateCompetitiveCancellationEvidence(current.Series, command.Terminal)
	}
	if command.Terminal != nil {
		return competitiveSeriesResolutionError("unresolved route has terminal evidence")
	}
	return nil
}

func competitiveSeriesRouteAllowsState(
	route CompetitiveSeriesRoute,
	state domain.ArenaSeriesState,
) bool {
	_, allowed := competitiveSeriesRouteStates[route][state]
	return allowed
}

type competitiveSeriesValidator func(CompetitiveSeriesRoute, SeriesExecution) error

func competitiveSeriesResolutionValidator(
	state domain.ArenaSeriesState,
) (competitiveSeriesValidator, bool) {
	validators := map[domain.ArenaSeriesState]competitiveSeriesValidator{
		domain.ArenaSeriesStateCompleted:      validateCompletedCompetitiveSeries,
		domain.ArenaSeriesStateReplayRequired: validateReplayRequiredCompetitiveSeries,
		domain.ArenaSeriesStateTechnicalPause: validatePausedCompetitiveSeries,
		domain.ArenaSeriesStateCancelled:      validateCancelledCompetitiveSeries,
	}
	validator, exists := validators[state]
	return validator, exists
}

func validateCompletedCompetitiveSeries(
	route CompetitiveSeriesRoute,
	resolved SeriesExecution,
) error {
	if !route.hasWinner() || !competitiveSeriesHasOneWinner(resolved.Series) {
		return ErrCompetitiveSeriesDraw
	}
	if !competitiveSeriesGameMatchesRoute(route, resolved.Series) {
		return competitiveSeriesResolutionError("completed Series has unresolved Game evidence")
	}
	return nil
}

func competitiveSeriesGameMatchesRoute(
	route CompetitiveSeriesRoute,
	series domain.ArenaSeries,
) bool {
	game, _, found := currentForfeitGame(series)
	if !found {
		return route == CompetitiveSeriesRouteOperatorForfeit
	}
	if route == CompetitiveSeriesRouteOperatorForfeit &&
		(game.State == domain.ArenaGameStatePlanned || game.State == domain.ArenaGameStateReady) {
		return true
	}
	if game.State != domain.ArenaGameStateCompleted || game.WinnerID == nil ||
		series.WinnerID == nil || *game.WinnerID != *series.WinnerID {
		return false
	}
	switch route {
	case CompetitiveSeriesRouteFairResult:
		return game.ResultReason == domain.ArenaGameResultReasonSolved
	case CompetitiveSeriesRouteSurrender:
		return game.ResultReason == domain.ArenaGameResultReasonSurrender
	case CompetitiveSeriesRouteOperatorForfeit:
		return game.ResultReason == domain.ArenaGameResultReasonOperatorForfeit
	case CompetitiveSeriesRouteDeadline,
		CompetitiveSeriesRouteReplayExhausted,
		CompetitiveSeriesRouteRecovery,
		CompetitiveSeriesRouteCorrection,
		CompetitiveSeriesRouteTournamentCancellation:
		return false
	default:
		return false
	}
}

func validateReplayRequiredCompetitiveSeries(
	route CompetitiveSeriesRoute,
	resolved SeriesExecution,
) error {
	if route != CompetitiveSeriesRouteDeadline && route != CompetitiveSeriesRouteCorrection {
		return competitiveSeriesResolutionError("route cannot require replay")
	}
	if competitiveSeriesHasTerminalWinnerEvidence(resolved) {
		return competitiveSeriesResolutionError("replay-required Series has terminal evidence")
	}
	return nil
}

func validatePausedCompetitiveSeries(
	route CompetitiveSeriesRoute,
	resolved SeriesExecution,
) error {
	if route != CompetitiveSeriesRouteReplayExhausted && route != CompetitiveSeriesRouteRecovery {
		return competitiveSeriesResolutionError("route cannot enter technical pause")
	}
	if competitiveSeriesHasTerminalWinnerEvidence(resolved) {
		return competitiveSeriesResolutionError("technical-pause Series has terminal evidence")
	}
	if route == CompetitiveSeriesRouteReplayExhausted &&
		(resolved.ResumeState == nil || *resolved.ResumeState != domain.ArenaSeriesStateReplayRequired) {
		return competitiveSeriesResolutionError("replay exhaustion lost its resume state")
	}
	return nil
}

func validateCancelledCompetitiveSeries(
	route CompetitiveSeriesRoute,
	resolved SeriesExecution,
) error {
	if route != CompetitiveSeriesRouteTournamentCancellation || resolved.Series.WinnerID != nil {
		return competitiveSeriesResolutionError("no-winner terminal Series requires tournament cancellation")
	}
	return nil
}

func validateCompetitiveWinnerEvidence(
	series domain.ArenaSeries,
	evidence *SeriesTerminalEvidence,
) error {
	if evidence == nil || evidence.WinnerID == nil ||
		evidence.ScoreRevisionID == nil || evidence.ScoreRevisionID.IsZero() ||
		evidence.ResultRevisionID == nil || evidence.ResultRevisionID.IsZero() {
		return ErrCompetitiveSeriesDraw
	}
	winnerID := evidence.Score.Winner(
		series.FirstParticipantID,
		series.SecondParticipantID,
		series.Format,
	)
	if winnerID == nil || *winnerID != *evidence.WinnerID {
		return ErrCompetitiveSeriesDraw
	}
	return nil
}

func validateCompetitiveCancellationEvidence(
	series domain.ArenaSeries,
	evidence *SeriesTerminalEvidence,
) error {
	if evidence == nil || evidence.WinnerID != nil || evidence.Score != series.Score ||
		evidence.ScoreRevisionID == nil || evidence.ScoreRevisionID.IsZero() ||
		evidence.ResultRevisionID == nil || evidence.ResultRevisionID.IsZero() {
		return competitiveSeriesResolutionError("tournament cancellation has invalid terminal evidence")
	}
	return nil
}

func competitiveSeriesNextState(route CompetitiveSeriesRoute) domain.ArenaSeriesState {
	switch route {
	case CompetitiveSeriesRouteFairResult,
		CompetitiveSeriesRouteSurrender,
		CompetitiveSeriesRouteOperatorForfeit:
		return domain.ArenaSeriesStateCompleted
	case CompetitiveSeriesRouteDeadline, CompetitiveSeriesRouteCorrection:
		return domain.ArenaSeriesStateReplayRequired
	case CompetitiveSeriesRouteReplayExhausted, CompetitiveSeriesRouteRecovery:
		return domain.ArenaSeriesStateTechnicalPause
	case CompetitiveSeriesRouteTournamentCancellation:
		return domain.ArenaSeriesStateCancelled
	default:
		return ""
	}
}

func competitiveTerminalResolutionMatches(
	current SeriesExecution,
	command CompetitiveSeriesResolutionCommand,
) bool {
	if command.Terminal == nil {
		return false
	}
	wantState := competitiveSeriesNextState(command.Route)
	series := current.Series
	return series.State == wantState && series.Score == command.Terminal.Score &&
		uuidPointersEqual(series.WinnerID, command.Terminal.WinnerID) &&
		seriesScoreRevisionPointersEqual(series.CurrentScoreRevisionID, command.Terminal.ScoreRevisionID) &&
		officialResultRevisionPointersEqual(series.CurrentResultRevisionID, command.Terminal.ResultRevisionID)
}

func competitiveSeriesHasOneWinner(series domain.ArenaSeries) bool {
	if series.WinnerID == nil {
		return false
	}
	winnerID := series.Score.Winner(
		series.FirstParticipantID,
		series.SecondParticipantID,
		series.Format,
	)
	return winnerID != nil && *winnerID == *series.WinnerID
}

func competitiveSeriesHasTerminalWinnerEvidence(resolved SeriesExecution) bool {
	return resolved.Series.WinnerID != nil || resolved.Series.CurrentResultRevisionID != nil
}

func cloneSeriesTerminalEvidence(evidence *SeriesTerminalEvidence) *SeriesTerminalEvidence {
	if evidence == nil {
		return nil
	}
	clone := *evidence
	clone.WinnerID = cloneUUIDPointer(evidence.WinnerID)
	clone.ScoreRevisionID = cloneSeriesScoreRevisionIDPointer(evidence.ScoreRevisionID)
	clone.ResultRevisionID = cloneOfficialResultRevisionIDPointer(evidence.ResultRevisionID)
	return &clone
}

func competitiveSeriesResolutionError(format string, arguments ...any) error {
	return fmt.Errorf(
		"%w: %s",
		ErrInvalidCompetitiveSeriesResolution,
		fmt.Sprintf(format, arguments...),
	)
}
