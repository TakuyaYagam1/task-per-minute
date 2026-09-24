package resumepresence

import (
	"reflect"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause/model"
)

// Source adoption is an independent disconnect root, never a normal Series
// child. Its complete immutable evidence remains in the normal pause graph.
func validSourceResumeCommand(command PauseResumePresenceCommand) bool {
	game := command.GameExpected
	if command.SeriesDecisionID != uuid.Nil || command.SeriesExpected != (PauseResumeDecisionExpectation{}) ||
		!validSourceGameExpectation(game) {
		return false
	}
	for _, source := range command.Resume.Expected.SourcePauses {
		if source.GameID == game.GameID {
			return source.PauseID == game.PauseID && source.CurrentRevisionID == game.CurrentRevisionID &&
				source.Revision == game.Revision && source.DecisionNumber == game.DecisionNumber &&
				source.ClockRevision == game.GameClock.Revision && source.Remaining == game.GameClock.Remaining
		}
	}
	return false
}

func validSourceGameExpectation(game PauseResumeDecisionExpectation) bool {
	return validPauseResumeDecisionExpectationHeader(game, PauseResumeDecisionScopeGameAttempt) &&
		game.GameID != uuid.Nil && game.ParentPauseID == nil && game.Depth == 0 && game.GameClock != nil &&
		validatePauseResumeGameClock(*game.GameClock, true) == nil &&
		game.GameClock.PauseID == game.PauseID && game.GameClock.GameID == game.GameID
}

func validateSourceResumeAuthority(authority PauseResumePresenceAuthority) error {
	game := pauseGameByID(authority.Resume.Pause.Graph.Games, authority.GameDecision.GameID)
	series := pauseSeriesByID(authority.Resume.Pause.Graph.Series, authority.GameDecision.SeriesID)
	if authority.SeriesDecision != (PauseResumeDecisionAuthority{}) || !validSourceResumeTopology(game, series) {
		return pauseResumePresenceError("missing independent disconnect source")
	}
	source := game.SourcePause
	clock := PauseResumeGameClock{
		PauseID: source.PauseID, GameID: source.GameID, OriginalDeadline: source.Clock.OriginalDeadline,
		FrozenAt: source.Clock.FrozenAt, Remaining: source.Clock.Remaining, Revision: source.Clock.Revision,
	}
	expected := PauseResumeDecisionAuthority{
		PauseID: source.PauseID, ScopeKind: PauseResumeDecisionScopeGameAttempt,
		CurrentRevisionID: source.CurrentRevisionID, State: source.State, Revision: source.Revision,
		SeriesID: source.SeriesID, GameID: source.GameID, DecisionNumber: source.DecisionNumber,
		StartedAt: source.StartedAt, GameClock: &clock,
	}
	if source.Reason != model.PauseReasonDisconnect || source.State != PauseStateActive ||
		source.ParentPauseID != nil || source.Depth != 0 || source.StartedAt.After(authority.Resume.Pause.PausedAt) ||
		!reflect.DeepEqual(expected, authority.GameDecision) {
		return pauseResumePresenceError("mismatched independent disconnect source")
	}
	return nil
}

func validSourceResumeTopology(game *PauseGame, series *PauseSeries) bool {
	return game != nil && series != nil && game.SourcePause != nil && game.Game.State == domain.GameStatePaused &&
		series.Execution.Series.State == domain.SeriesStateActive && series.Execution.ResumeState == nil &&
		series.CurrentGameID != nil && *series.CurrentGameID == game.Game.ID && game.SeriesID == series.Execution.Series.ID
}
