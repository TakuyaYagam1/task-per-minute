package series

import (
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrInvalidSeriesExecution    = errors.New("invalid series execution")
	ErrSeriesExecutionTransition = errors.New("series execution transition is not allowed")
)

var seriesExecutionTransitions = map[domain.SeriesState]map[domain.SeriesState]struct{}{
	domain.SeriesStatePlanned: {
		domain.SeriesStateLocked: {}, domain.SeriesStateCancelled: {},
	},
	domain.SeriesStateLocked: {
		domain.SeriesStateDraft: {}, domain.SeriesStateReady: {}, domain.SeriesStateCancelled: {},
	},
	domain.SeriesStateDraft: {
		domain.SeriesStateReady: {}, domain.SeriesStateTechnicalPause: {},
		domain.SeriesStateCancelled: {},
	},
	domain.SeriesStateReady: {
		domain.SeriesStateLocked: {}, domain.SeriesStateActive: {},
		domain.SeriesStateTechnicalPause: {}, domain.SeriesStateCompleted: {},
		domain.SeriesStateCancelled: {},
	},
	domain.SeriesStateActive: {
		domain.SeriesStateReplayRequired: {}, domain.SeriesStateTechnicalPause: {},
		domain.SeriesStateCompleted: {}, domain.SeriesStateCancelled: {},
	},
	domain.SeriesStateReplayRequired: {
		domain.SeriesStateReady: {}, domain.SeriesStateTechnicalPause: {},
		domain.SeriesStateCancelled: {},
	},
}

type Execution struct {
	Series      domain.Series
	ResumeState *domain.SeriesState
}

type TerminalEvidence struct {
	Score            domain.SeriesScore
	WinnerID         *uuid.UUID
	ScoreRevisionID  *domain.SeriesScoreRevisionID
	ResultRevisionID *domain.OfficialResultRevisionID
}

type TransitionCommand struct {
	NextState domain.SeriesState
	Terminal  *TerminalEvidence
}

func (e Execution) Validate() error {
	if err := e.Series.Validate(); err != nil {
		return seriesExecutionError("Series: %v", err)
	}
	if e.Series.State == domain.SeriesStateTechnicalPause {
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

func Transition(
	current Execution,
	command TransitionCommand,
) (Execution, bool, error) {
	if err := current.Validate(); err != nil {
		return Execution{}, false, err
	}
	if !command.NextState.IsValid() {
		return Execution{}, false, seriesExecutionError("unknown next state %q", command.NextState)
	}
	if command.NextState == current.Series.State {
		if command.Terminal != nil {
			return Execution{}, false, seriesExecutionError("no-op transition has terminal evidence")
		}
		return cloneSeriesExecution(current), false, nil
	}
	if !canTransitionSeriesExecution(current, command.NextState) {
		return Execution{}, false, fmt.Errorf(
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
			return Execution{}, false, seriesExecutionError("terminal transition requires result evidence")
		}
		applySeriesTerminalEvidence(&next.Series, *command.Terminal)
	} else if command.Terminal != nil {
		return Execution{}, false, seriesExecutionError("non-terminal transition has result evidence")
	}
	if err := next.Validate(); err != nil {
		return Execution{}, false, err
	}
	return next, true, nil
}

func canTransitionSeriesExecution(current Execution, next domain.SeriesState) bool {
	if current.Series.State == domain.SeriesStateTechnicalPause {
		return next == domain.SeriesStateCompleted ||
			next == domain.SeriesStateCancelled ||
			(current.ResumeState != nil && next == *current.ResumeState)
	}
	_, allowed := seriesExecutionTransitions[current.Series.State][next]
	return allowed
}

func isSeriesPauseOrigin(state domain.SeriesState) bool {
	switch state {
	case domain.SeriesStateDraft,
		domain.SeriesStateReady,
		domain.SeriesStateActive,
		domain.SeriesStateReplayRequired:
		return true
	case domain.SeriesStatePlanned,
		domain.SeriesStateLocked,
		domain.SeriesStateTechnicalPause,
		domain.SeriesStateCompleted,
		domain.SeriesStateCancelled:
		return false
	}
	return false
}

func setSeriesResumeState(
	next *Execution,
	current Execution,
	nextState domain.SeriesState,
) {
	if nextState == domain.SeriesStateTechnicalPause {
		resumeState := current.Series.State
		next.ResumeState = &resumeState
		return
	}
	next.ResumeState = nil
}

func applySeriesTerminalEvidence(series *domain.Series, evidence TerminalEvidence) {
	series.Score = evidence.Score
	series.WinnerID = cloneUUIDPointer(evidence.WinnerID)
	series.CurrentScoreRevisionID = cloneSeriesScoreRevisionIDPointer(evidence.ScoreRevisionID)
	series.CurrentResultRevisionID = cloneOfficialResultRevisionIDPointer(evidence.ResultRevisionID)
}

func cloneSeriesExecution(execution Execution) Execution {
	cloned := execution
	cloned.ResumeState = cloneSeriesStatePointer(execution.ResumeState)
	cloned.Series = cloneSeries(execution.Series)
	return cloned
}

func CloneExecution(execution Execution) Execution {
	return cloneSeriesExecution(execution)
}

func cloneSeries(series domain.Series) domain.Series {
	cloned := series
	cloned.WinnerID = cloneUUIDPointer(series.WinnerID)
	cloned.CurrentScoreRevisionID = cloneSeriesScoreRevisionIDPointer(series.CurrentScoreRevisionID)
	cloned.CurrentResultRevisionID = cloneOfficialResultRevisionIDPointer(series.CurrentResultRevisionID)
	cloned.Slots = make([]domain.GameSlot, len(series.Slots))
	for index := range series.Slots {
		cloned.Slots[index] = cloneGameSlot(series.Slots[index])
	}
	return cloned
}

func cloneSeriesStatePointer(value *domain.SeriesState) *domain.SeriesState {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneSeriesScoreRevisionIDPointer(
	value *domain.SeriesScoreRevisionID,
) *domain.SeriesScoreRevisionID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneOfficialResultRevisionIDPointer(
	value *domain.OfficialResultRevisionID,
) *domain.OfficialResultRevisionID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func seriesExecutionError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidSeriesExecution, fmt.Sprintf(format, args...))
}
