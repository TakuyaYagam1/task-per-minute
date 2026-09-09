package series

import (
	"errors"
	"fmt"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrInvalidCompetitiveSeriesResolution = errors.New("invalid competitive Series resolution")
	ErrCompetitiveSeriesDraw              = errors.New("competitive Series cannot end in a draw")
)

type ResolutionRoute string

const (
	CompetitiveSeriesRouteFairResult             ResolutionRoute = "fair_result"
	CompetitiveSeriesRouteSurrender              ResolutionRoute = "surrender"
	CompetitiveSeriesRouteOperatorForfeit        ResolutionRoute = "operator_forfeit"
	CompetitiveSeriesRouteDeadline               ResolutionRoute = "deadline"
	CompetitiveSeriesRouteReplayExhausted        ResolutionRoute = "replay_exhausted"
	CompetitiveSeriesRouteRecovery               ResolutionRoute = "recovery"
	CompetitiveSeriesRouteCorrection             ResolutionRoute = "correction"
	CompetitiveSeriesRouteTournamentCancellation ResolutionRoute = "tournament_cancellation"
)

type ResolutionCommand struct {
	Route    ResolutionRoute
	Terminal *TerminalEvidence
}

var competitiveSeriesRouteStates = map[ResolutionRoute]map[domain.SeriesState]struct{}{
	CompetitiveSeriesRouteFairResult: {
		domain.SeriesStateActive: {}, domain.SeriesStateTechnicalPause: {},
		domain.SeriesStateCompleted: {},
	},
	CompetitiveSeriesRouteSurrender: {
		domain.SeriesStateActive: {}, domain.SeriesStateTechnicalPause: {},
		domain.SeriesStateCompleted: {},
	},
	CompetitiveSeriesRouteOperatorForfeit: {
		domain.SeriesStateReady: {}, domain.SeriesStateActive: {},
		domain.SeriesStateTechnicalPause: {}, domain.SeriesStateCompleted: {},
	},
	CompetitiveSeriesRouteDeadline: {
		domain.SeriesStateActive: {}, domain.SeriesStateReplayRequired: {},
	},
	CompetitiveSeriesRouteCorrection: {
		domain.SeriesStateActive: {}, domain.SeriesStateReplayRequired: {},
	},
	CompetitiveSeriesRouteReplayExhausted: {
		domain.SeriesStateReplayRequired: {}, domain.SeriesStateTechnicalPause: {},
	},
	CompetitiveSeriesRouteRecovery: {
		domain.SeriesStateActive: {}, domain.SeriesStateReplayRequired: {},
		domain.SeriesStateTechnicalPause: {},
	},
	CompetitiveSeriesRouteTournamentCancellation: {
		domain.SeriesStatePlanned: {}, domain.SeriesStateLocked: {},
		domain.SeriesStateDraft: {}, domain.SeriesStateReady: {},
		domain.SeriesStateActive: {}, domain.SeriesStateReplayRequired: {},
		domain.SeriesStateTechnicalPause: {}, domain.SeriesStateCancelled: {},
	},
}

func ResolveCompetitive(
	current Execution,
	command ResolutionCommand,
) (Execution, bool, error) {
	if err := current.Validate(); err != nil {
		return Execution{}, false, competitiveSeriesResolutionError("Series: %v", err)
	}
	if err := validateCompetitiveSeriesCommand(current, command); err != nil {
		return Execution{}, false, err
	}
	if current.Series.State.IsTerminal() {
		if competitiveTerminalResolutionMatches(current, command) {
			clone := cloneSeriesExecution(current)
			return clone, false, ValidateResolution(command.Route, clone)
		}
		return Execution{}, false, competitiveSeriesResolutionError("terminal Series differs from command")
	}

	nextState := competitiveSeriesNextState(command.Route)
	resolved, changed, err := Transition(current, TransitionCommand{
		NextState: nextState,
		Terminal:  cloneSeriesTerminalEvidence(command.Terminal),
	})
	if err != nil {
		return Execution{}, false, competitiveSeriesResolutionError("transition: %v", err)
	}
	if err := ValidateResolution(command.Route, resolved); err != nil {
		return Execution{}, false, err
	}
	return resolved, changed, nil
}

func ValidateResolution(
	route ResolutionRoute,
	resolved Execution,
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

func (r ResolutionRoute) IsValid() bool {
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

func (r ResolutionRoute) hasWinner() bool {
	return r == CompetitiveSeriesRouteFairResult ||
		r == CompetitiveSeriesRouteSurrender ||
		r == CompetitiveSeriesRouteOperatorForfeit
}

func validateCompetitiveSeriesCommand(
	current Execution,
	command ResolutionCommand,
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
	route ResolutionRoute,
	state domain.SeriesState,
) bool {
	_, allowed := competitiveSeriesRouteStates[route][state]
	return allowed
}

type competitiveSeriesValidator func(ResolutionRoute, Execution) error

func competitiveSeriesResolutionValidator(
	state domain.SeriesState,
) (competitiveSeriesValidator, bool) {
	validators := map[domain.SeriesState]competitiveSeriesValidator{
		domain.SeriesStateCompleted:      validateCompletedCompetitiveSeries,
		domain.SeriesStateReplayRequired: validateReplayRequiredCompetitiveSeries,
		domain.SeriesStateTechnicalPause: validatePausedCompetitiveSeries,
		domain.SeriesStateCancelled:      validateCancelledCompetitiveSeries,
	}
	validator, exists := validators[state]
	return validator, exists
}

func validateCompletedCompetitiveSeries(
	route ResolutionRoute,
	resolved Execution,
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
	route ResolutionRoute,
	series domain.Series,
) bool {
	game, found := currentSeriesGame(series)
	if !found {
		return route == CompetitiveSeriesRouteOperatorForfeit
	}
	if route == CompetitiveSeriesRouteOperatorForfeit &&
		(game.State == domain.GameStatePlanned || game.State == domain.GameStateReady) {
		return true
	}
	if game.State != domain.GameStateCompleted || game.WinnerID == nil ||
		series.WinnerID == nil || *game.WinnerID != *series.WinnerID {
		return false
	}
	switch route {
	case CompetitiveSeriesRouteFairResult:
		return game.ResultReason == domain.GameResultReasonSolved
	case CompetitiveSeriesRouteSurrender:
		return game.ResultReason == domain.GameResultReasonSurrender
	case CompetitiveSeriesRouteOperatorForfeit:
		return game.ResultReason == domain.GameResultReasonOperatorForfeit
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
	route ResolutionRoute,
	resolved Execution,
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
	route ResolutionRoute,
	resolved Execution,
) error {
	if route != CompetitiveSeriesRouteReplayExhausted && route != CompetitiveSeriesRouteRecovery {
		return competitiveSeriesResolutionError("route cannot enter technical pause")
	}
	if competitiveSeriesHasTerminalWinnerEvidence(resolved) {
		return competitiveSeriesResolutionError("technical-pause Series has terminal evidence")
	}
	if route == CompetitiveSeriesRouteReplayExhausted &&
		(resolved.ResumeState == nil || *resolved.ResumeState != domain.SeriesStateReplayRequired) {
		return competitiveSeriesResolutionError("replay exhaustion lost its resume state")
	}
	return nil
}

func validateCancelledCompetitiveSeries(
	route ResolutionRoute,
	resolved Execution,
) error {
	if route != CompetitiveSeriesRouteTournamentCancellation || resolved.Series.WinnerID != nil {
		return competitiveSeriesResolutionError("no-winner terminal Series requires tournament cancellation")
	}
	return nil
}

func validateCompetitiveWinnerEvidence(
	series domain.Series,
	evidence *TerminalEvidence,
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
	series domain.Series,
	evidence *TerminalEvidence,
) error {
	if evidence == nil || evidence.WinnerID != nil || evidence.Score != series.Score ||
		evidence.ScoreRevisionID == nil || evidence.ScoreRevisionID.IsZero() ||
		evidence.ResultRevisionID == nil || evidence.ResultRevisionID.IsZero() {
		return competitiveSeriesResolutionError("tournament cancellation has invalid terminal evidence")
	}
	return nil
}

func competitiveSeriesNextState(route ResolutionRoute) domain.SeriesState {
	switch route {
	case CompetitiveSeriesRouteFairResult,
		CompetitiveSeriesRouteSurrender,
		CompetitiveSeriesRouteOperatorForfeit:
		return domain.SeriesStateCompleted
	case CompetitiveSeriesRouteDeadline, CompetitiveSeriesRouteCorrection:
		return domain.SeriesStateReplayRequired
	case CompetitiveSeriesRouteReplayExhausted, CompetitiveSeriesRouteRecovery:
		return domain.SeriesStateTechnicalPause
	case CompetitiveSeriesRouteTournamentCancellation:
		return domain.SeriesStateCancelled
	default:
		return ""
	}
}

func competitiveTerminalResolutionMatches(
	current Execution,
	command ResolutionCommand,
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

func competitiveSeriesHasOneWinner(series domain.Series) bool {
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

func competitiveSeriesHasTerminalWinnerEvidence(resolved Execution) bool {
	return resolved.Series.WinnerID != nil || resolved.Series.CurrentResultRevisionID != nil
}

func cloneSeriesTerminalEvidence(evidence *TerminalEvidence) *TerminalEvidence {
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
