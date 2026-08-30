package arena_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestSeriesExecutionTransitions(t *testing.T) {
	t.Parallel()

	states := []domain.ArenaSeriesState{
		domain.ArenaSeriesStatePlanned,
		domain.ArenaSeriesStateLocked,
		domain.ArenaSeriesStateDraft,
		domain.ArenaSeriesStateReady,
		domain.ArenaSeriesStateActive,
		domain.ArenaSeriesStateReplayRequired,
		domain.ArenaSeriesStateTechnicalPause,
		domain.ArenaSeriesStateCompleted,
		domain.ArenaSeriesStateCancelled,
	}
	allowed := map[domain.ArenaSeriesState]map[domain.ArenaSeriesState]bool{
		domain.ArenaSeriesStatePlanned: {
			domain.ArenaSeriesStatePlanned: true, domain.ArenaSeriesStateLocked: true,
			domain.ArenaSeriesStateCancelled: true,
		},
		domain.ArenaSeriesStateLocked: {
			domain.ArenaSeriesStateLocked: true, domain.ArenaSeriesStateDraft: true,
			domain.ArenaSeriesStateReady: true, domain.ArenaSeriesStateCancelled: true,
		},
		domain.ArenaSeriesStateDraft: {
			domain.ArenaSeriesStateDraft: true, domain.ArenaSeriesStateReady: true,
			domain.ArenaSeriesStateTechnicalPause: true, domain.ArenaSeriesStateCancelled: true,
		},
		domain.ArenaSeriesStateReady: {
			domain.ArenaSeriesStateReady: true, domain.ArenaSeriesStateLocked: true,
			domain.ArenaSeriesStateActive: true, domain.ArenaSeriesStateTechnicalPause: true,
			domain.ArenaSeriesStateCompleted: true, domain.ArenaSeriesStateCancelled: true,
		},
		domain.ArenaSeriesStateActive: {
			domain.ArenaSeriesStateActive: true, domain.ArenaSeriesStateReplayRequired: true,
			domain.ArenaSeriesStateTechnicalPause: true, domain.ArenaSeriesStateCompleted: true,
			domain.ArenaSeriesStateCancelled: true,
		},
		domain.ArenaSeriesStateReplayRequired: {
			domain.ArenaSeriesStateReplayRequired: true, domain.ArenaSeriesStateReady: true,
			domain.ArenaSeriesStateTechnicalPause: true, domain.ArenaSeriesStateCancelled: true,
		},
		domain.ArenaSeriesStateTechnicalPause: {
			domain.ArenaSeriesStateTechnicalPause: true, domain.ArenaSeriesStateActive: true,
			domain.ArenaSeriesStateCompleted: true, domain.ArenaSeriesStateCancelled: true,
		},
		domain.ArenaSeriesStateCompleted: {domain.ArenaSeriesStateCompleted: true},
		domain.ArenaSeriesStateCancelled: {domain.ArenaSeriesStateCancelled: true},
	}

	for _, from := range states {
		t.Run(string(from), func(t *testing.T) {
			t.Parallel()

			for _, to := range states {
				t.Run(string(to), func(t *testing.T) {
					t.Parallel()

					current := task035SeriesExecution(from)
					before := task035SeriesExecution(from)
					command := task035SeriesTransitionCommand(current, to)
					next, changed, err := arena.TransitionSeriesExecution(current, command)
					if !allowed[from][to] {
						if !errors.Is(err, arena.ErrSeriesExecutionTransition) || changed {
							t.Fatalf("TransitionSeriesExecution(%s -> %s) error = %v, changed = %v", from, to, err, changed)
						}
						if !reflect.DeepEqual(current, before) {
							t.Fatal("rejected transition mutated the caller snapshot")
						}
						return
					}
					if err != nil {
						t.Fatalf("TransitionSeriesExecution(%s -> %s) error = %v", from, to, err)
					}
					if changed != (from != to) || next.Series.State != to {
						t.Fatalf("TransitionSeriesExecution(%s -> %s) state = %s, changed = %v", from, to, next.Series.State, changed)
					}
					if err := next.Validate(); err != nil {
						t.Fatalf("result Validate() error = %v", err)
					}
					if !reflect.DeepEqual(current, before) {
						t.Fatal("accepted transition mutated the caller snapshot")
					}
				})
			}
		})
	}

	t.Run("pause restores only its recorded resume state", func(t *testing.T) {
		t.Parallel()

		for _, origin := range []domain.ArenaSeriesState{
			domain.ArenaSeriesStateDraft,
			domain.ArenaSeriesStateReady,
			domain.ArenaSeriesStateActive,
			domain.ArenaSeriesStateReplayRequired,
		} {
			current := task035SeriesExecution(origin)
			paused, changed, err := arena.TransitionSeriesExecution(current, arena.SeriesExecutionTransitionCommand{
				NextState: domain.ArenaSeriesStateTechnicalPause,
			})
			if err != nil || !changed || paused.ResumeState == nil || *paused.ResumeState != origin {
				t.Fatalf("pause from %s error = %v, changed = %v, resume = %v", origin, err, changed, paused.ResumeState)
			}

			wrong := domain.ArenaSeriesStateActive
			if wrong == origin {
				wrong = domain.ArenaSeriesStateReady
			}
			if _, changed, err := arena.TransitionSeriesExecution(paused, arena.SeriesExecutionTransitionCommand{
				NextState: wrong,
			}); !errors.Is(err, arena.ErrSeriesExecutionTransition) || changed {
				t.Fatalf("resume %s as %s error = %v, changed = %v", origin, wrong, err, changed)
			}
			resumed, changed, err := arena.TransitionSeriesExecution(paused, arena.SeriesExecutionTransitionCommand{
				NextState: origin,
			})
			if err != nil || !changed || resumed.ResumeState != nil || resumed.Series.State != origin {
				t.Fatalf("resume to %s error = %v, changed = %v, execution = %+v", origin, err, changed, resumed)
			}
		}
	})

	t.Run("terminal evidence is mandatory and compatible", func(t *testing.T) {
		t.Parallel()

		current := task035SeriesExecution(domain.ArenaSeriesStateActive)
		if _, changed, err := arena.TransitionSeriesExecution(current, arena.SeriesExecutionTransitionCommand{
			NextState: domain.ArenaSeriesStateCompleted,
		}); !errors.Is(err, arena.ErrInvalidSeriesExecution) || changed {
			t.Fatalf("missing evidence error = %v, changed = %v", err, changed)
		}

		command := task035SeriesTransitionCommand(current, domain.ArenaSeriesStateCompleted)
		secondID := current.Series.SecondParticipantID
		command.Terminal.WinnerID = &secondID
		if _, changed, err := arena.TransitionSeriesExecution(current, command); !errors.Is(err, arena.ErrInvalidSeriesExecution) || changed {
			t.Fatalf("incompatible winner error = %v, changed = %v", err, changed)
		}
	})

	t.Run("replay cannot reactivate directly", func(t *testing.T) {
		t.Parallel()

		current := task035SeriesExecution(domain.ArenaSeriesStateReplayRequired)
		if _, changed, err := arena.TransitionSeriesExecution(current, arena.SeriesExecutionTransitionCommand{
			NextState: domain.ArenaSeriesStateActive,
		}); !errors.Is(err, arena.ErrSeriesExecutionTransition) || changed {
			t.Fatalf("direct replay activation error = %v, changed = %v", err, changed)
		}
	})
}

func task035SeriesExecution(state domain.ArenaSeriesState) arena.SeriesExecution {
	firstID := task035ID(3)
	secondID := task035ID(4)
	series := domain.ArenaSeries{
		ID: task035ID(1), TournamentID: task035ID(2), FirstParticipantID: firstID,
		SecondParticipantID: secondID, Format: domain.ArenaSeriesFormatBO1, State: state,
	}
	execution := arena.SeriesExecution{Series: series}
	switch state {
	case domain.ArenaSeriesStatePlanned,
		domain.ArenaSeriesStateLocked,
		domain.ArenaSeriesStateDraft,
		domain.ArenaSeriesStateReady,
		domain.ArenaSeriesStateActive,
		domain.ArenaSeriesStateReplayRequired:
	case domain.ArenaSeriesStateTechnicalPause:
		resume := domain.ArenaSeriesStateActive
		execution.ResumeState = &resume
	case domain.ArenaSeriesStateCompleted:
		task035ApplySeriesTerminal(&execution.Series, true)
	case domain.ArenaSeriesStateCancelled:
		task035ApplySeriesTerminal(&execution.Series, false)
	}
	return execution
}

func task035SeriesTransitionCommand(
	current arena.SeriesExecution,
	next domain.ArenaSeriesState,
) arena.SeriesExecutionTransitionCommand {
	command := arena.SeriesExecutionTransitionCommand{NextState: next}
	if next == current.Series.State {
		return command
	}
	if next == domain.ArenaSeriesStateCompleted || next == domain.ArenaSeriesStateCancelled {
		series := current.Series
		task035ApplySeriesTerminal(&series, next == domain.ArenaSeriesStateCompleted)
		command.Terminal = &arena.SeriesTerminalEvidence{
			Score: series.Score, WinnerID: series.WinnerID,
			ScoreRevisionID:  series.CurrentScoreRevisionID,
			ResultRevisionID: series.CurrentResultRevisionID,
		}
	}
	return command
}

func task035ApplySeriesTerminal(series *domain.ArenaSeries, completed bool) {
	scoreRevisionID := domain.ArenaSeriesScoreRevisionID(task035ID(5))
	resultRevisionID := domain.ArenaOfficialResultRevisionID(task035ID(6))
	series.CurrentScoreRevisionID = &scoreRevisionID
	series.CurrentResultRevisionID = &resultRevisionID
	if completed {
		winnerID := series.FirstParticipantID
		series.Score = domain.ArenaSeriesScore{FirstParticipantWins: 1}
		series.WinnerID = &winnerID
	}
}

func task035ID(number int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("35000000-0000-0000-0000-%012d", number))
}
