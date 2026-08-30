package arena

import (
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrInvalidSeriesExecution    = errors.New("invalid arena Series execution")
	ErrSeriesExecutionTransition = errors.New("arena Series execution transition is not allowed")
)

var seriesExecutionTransitions = map[domain.ArenaSeriesState]map[domain.ArenaSeriesState]struct{}{
	domain.ArenaSeriesStatePlanned: {
		domain.ArenaSeriesStateLocked: {}, domain.ArenaSeriesStateCancelled: {},
	},
	domain.ArenaSeriesStateLocked: {
		domain.ArenaSeriesStateDraft: {}, domain.ArenaSeriesStateReady: {}, domain.ArenaSeriesStateCancelled: {},
	},
	domain.ArenaSeriesStateDraft: {
		domain.ArenaSeriesStateReady: {}, domain.ArenaSeriesStateTechnicalPause: {},
		domain.ArenaSeriesStateCancelled: {},
	},
	domain.ArenaSeriesStateReady: {
		domain.ArenaSeriesStateLocked: {}, domain.ArenaSeriesStateActive: {},
		domain.ArenaSeriesStateTechnicalPause: {}, domain.ArenaSeriesStateCompleted: {},
		domain.ArenaSeriesStateCancelled: {},
	},
	domain.ArenaSeriesStateActive: {
		domain.ArenaSeriesStateReplayRequired: {}, domain.ArenaSeriesStateTechnicalPause: {},
		domain.ArenaSeriesStateCompleted: {}, domain.ArenaSeriesStateCancelled: {},
	},
	domain.ArenaSeriesStateReplayRequired: {
		domain.ArenaSeriesStateReady: {}, domain.ArenaSeriesStateTechnicalPause: {},
		domain.ArenaSeriesStateCancelled: {},
	},
}

type SeriesExecution struct {
	Series      domain.ArenaSeries
	ResumeState *domain.ArenaSeriesState
}

type SeriesTerminalEvidence struct {
	Score            domain.ArenaSeriesScore
	WinnerID         *uuid.UUID
	ScoreRevisionID  *domain.ArenaSeriesScoreRevisionID
	ResultRevisionID *domain.ArenaOfficialResultRevisionID
}

type SeriesExecutionTransitionCommand struct {
	NextState domain.ArenaSeriesState
	Terminal  *SeriesTerminalEvidence
}

func (e SeriesExecution) Validate() error {
	if err := e.Series.Validate(); err != nil {
		return seriesExecutionError("Series: %v", err)
	}
	if e.Series.State == domain.ArenaSeriesStateTechnicalPause {
		if e.ResumeState == nil || !isSeriesPauseOrigin(*e.ResumeState) {
			return seriesExecutionError("technical pause requires a live resume state")
		}
		return nil
	}
	if e.ResumeState != nil {
		return seriesExecutionError("resume state exists outside technical pause")
	}
	return nil
}

func TransitionSeriesExecution(
	current SeriesExecution,
	command SeriesExecutionTransitionCommand,
) (SeriesExecution, bool, error) {
	if err := current.Validate(); err != nil {
		return SeriesExecution{}, false, err
	}
	if !command.NextState.IsValid() {
		return SeriesExecution{}, false, seriesExecutionError("unknown next state %q", command.NextState)
	}
	if command.NextState == current.Series.State {
		if command.Terminal != nil {
			return SeriesExecution{}, false, seriesExecutionError("no-op transition has terminal evidence")
		}
		return cloneSeriesExecution(current), false, nil
	}
	if !canTransitionSeriesExecution(current, command.NextState) {
		return SeriesExecution{}, false, fmt.Errorf(
			"%w: %s -> %s",
			ErrSeriesExecutionTransition,
			current.Series.State,
			command.NextState,
		)
	}

	next := cloneSeriesExecution(current)
	next.Series.State = command.NextState
	setSeriesResumeState(&next, current, command.NextState)
	if command.NextState.IsTerminal() {
		if command.Terminal == nil {
			return SeriesExecution{}, false, seriesExecutionError("terminal transition requires result evidence")
		}
		applySeriesTerminalEvidence(&next.Series, *command.Terminal)
	} else if command.Terminal != nil {
		return SeriesExecution{}, false, seriesExecutionError("non-terminal transition has result evidence")
	}
	if err := next.Validate(); err != nil {
		return SeriesExecution{}, false, err
	}
	return next, true, nil
}

func canTransitionSeriesExecution(current SeriesExecution, next domain.ArenaSeriesState) bool {
	if current.Series.State == domain.ArenaSeriesStateTechnicalPause {
		return next == domain.ArenaSeriesStateCompleted ||
			next == domain.ArenaSeriesStateCancelled ||
			(current.ResumeState != nil && next == *current.ResumeState)
	}
	_, allowed := seriesExecutionTransitions[current.Series.State][next]
	return allowed
}

func isSeriesPauseOrigin(state domain.ArenaSeriesState) bool {
	switch state {
	case domain.ArenaSeriesStateDraft,
		domain.ArenaSeriesStateReady,
		domain.ArenaSeriesStateActive,
		domain.ArenaSeriesStateReplayRequired:
		return true
	case domain.ArenaSeriesStatePlanned,
		domain.ArenaSeriesStateLocked,
		domain.ArenaSeriesStateTechnicalPause,
		domain.ArenaSeriesStateCompleted,
		domain.ArenaSeriesStateCancelled:
		return false
	}
	return false
}

func setSeriesResumeState(
	next *SeriesExecution,
	current SeriesExecution,
	nextState domain.ArenaSeriesState,
) {
	if nextState == domain.ArenaSeriesStateTechnicalPause {
		resumeState := current.Series.State
		next.ResumeState = &resumeState
		return
	}
	next.ResumeState = nil
}

func applySeriesTerminalEvidence(series *domain.ArenaSeries, evidence SeriesTerminalEvidence) {
	series.Score = evidence.Score
	series.WinnerID = cloneUUIDPointer(evidence.WinnerID)
	series.CurrentScoreRevisionID = cloneSeriesScoreRevisionIDPointer(evidence.ScoreRevisionID)
	series.CurrentResultRevisionID = cloneOfficialResultRevisionIDPointer(evidence.ResultRevisionID)
}

func cloneSeriesExecution(execution SeriesExecution) SeriesExecution {
	cloned := execution
	cloned.ResumeState = cloneSeriesStatePointer(execution.ResumeState)
	cloned.Series = cloneArenaSeries(execution.Series)
	return cloned
}

func cloneArenaSeries(series domain.ArenaSeries) domain.ArenaSeries {
	cloned := series
	cloned.WinnerID = cloneUUIDPointer(series.WinnerID)
	cloned.CurrentScoreRevisionID = cloneSeriesScoreRevisionIDPointer(series.CurrentScoreRevisionID)
	cloned.CurrentResultRevisionID = cloneOfficialResultRevisionIDPointer(series.CurrentResultRevisionID)
	cloned.Slots = make([]domain.ArenaGameSlot, len(series.Slots))
	for index := range series.Slots {
		cloned.Slots[index] = cloneArenaGameSlot(series.Slots[index])
	}
	return cloned
}

func cloneSeriesStatePointer(value *domain.ArenaSeriesState) *domain.ArenaSeriesState {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneSeriesScoreRevisionIDPointer(
	value *domain.ArenaSeriesScoreRevisionID,
) *domain.ArenaSeriesScoreRevisionID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneOfficialResultRevisionIDPointer(
	value *domain.ArenaOfficialResultRevisionID,
) *domain.ArenaOfficialResultRevisionID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func seriesExecutionError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidSeriesExecution, fmt.Sprintf(format, args...))
}
