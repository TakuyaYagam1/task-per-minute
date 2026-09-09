package series_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	game "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
)

func TestSeriesExecutionTransitions(t *testing.T) {
	t.Parallel()

	states := []domain.SeriesState{
		domain.SeriesStatePlanned,
		domain.SeriesStateLocked,
		domain.SeriesStateDraft,
		domain.SeriesStateReady,
		domain.SeriesStateActive,
		domain.SeriesStateReplayRequired,
		domain.SeriesStateTechnicalPause,
		domain.SeriesStateCompleted,
		domain.SeriesStateCancelled,
	}
	allowed := map[domain.SeriesState]map[domain.SeriesState]bool{
		domain.SeriesStatePlanned: {
			domain.SeriesStatePlanned: true, domain.SeriesStateLocked: true,
			domain.SeriesStateCancelled: true,
		},
		domain.SeriesStateLocked: {
			domain.SeriesStateLocked: true, domain.SeriesStateDraft: true,
			domain.SeriesStateReady: true, domain.SeriesStateCancelled: true,
		},
		domain.SeriesStateDraft: {
			domain.SeriesStateDraft: true, domain.SeriesStateReady: true,
			domain.SeriesStateTechnicalPause: true, domain.SeriesStateCancelled: true,
		},
		domain.SeriesStateReady: {
			domain.SeriesStateReady: true, domain.SeriesStateLocked: true,
			domain.SeriesStateActive: true, domain.SeriesStateTechnicalPause: true,
			domain.SeriesStateCompleted: true, domain.SeriesStateCancelled: true,
		},
		domain.SeriesStateActive: {
			domain.SeriesStateActive: true, domain.SeriesStateReplayRequired: true,
			domain.SeriesStateTechnicalPause: true, domain.SeriesStateCompleted: true,
			domain.SeriesStateCancelled: true,
		},
		domain.SeriesStateReplayRequired: {
			domain.SeriesStateReplayRequired: true, domain.SeriesStateReady: true,
			domain.SeriesStateTechnicalPause: true, domain.SeriesStateCancelled: true,
		},
		domain.SeriesStateTechnicalPause: {
			domain.SeriesStateTechnicalPause: true, domain.SeriesStateActive: true,
			domain.SeriesStateCompleted: true, domain.SeriesStateCancelled: true,
		},
		domain.SeriesStateCompleted: {domain.SeriesStateCompleted: true},
		domain.SeriesStateCancelled: {domain.SeriesStateCancelled: true},
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
					next, changed, err := game.Transition(current, command)
					if !allowed[from][to] {
						if !errors.Is(err, game.ErrSeriesExecutionTransition) || changed {
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

		for _, origin := range []domain.SeriesState{
			domain.SeriesStateDraft,
			domain.SeriesStateReady,
			domain.SeriesStateActive,
			domain.SeriesStateReplayRequired,
		} {
			current := task035SeriesExecution(origin)
			paused, changed, err := game.Transition(current, game.TransitionCommand{
				NextState: domain.SeriesStateTechnicalPause,
			})
			if err != nil || !changed || paused.ResumeState == nil || *paused.ResumeState != origin {
				t.Fatalf("pause from %s error = %v, changed = %v, resume = %v", origin, err, changed, paused.ResumeState)
			}

			wrong := domain.SeriesStateActive
			if wrong == origin {
				wrong = domain.SeriesStateReady
			}
			if _, changed, err := game.Transition(paused, game.TransitionCommand{
				NextState: wrong,
			}); !errors.Is(err, game.ErrSeriesExecutionTransition) || changed {
				t.Fatalf("resume %s as %s error = %v, changed = %v", origin, wrong, err, changed)
			}
			resumed, changed, err := game.Transition(paused, game.TransitionCommand{
				NextState: origin,
			})
			if err != nil || !changed || resumed.ResumeState != nil || resumed.Series.State != origin {
				t.Fatalf("resume to %s error = %v, changed = %v, execution = %+v", origin, err, changed, resumed)
			}
		}
	})

	t.Run("terminal evidence is mandatory and compatible", func(t *testing.T) {
		t.Parallel()

		current := task035SeriesExecution(domain.SeriesStateActive)
		if _, changed, err := game.Transition(current, game.TransitionCommand{
			NextState: domain.SeriesStateCompleted,
		}); !errors.Is(err, game.ErrInvalidSeriesExecution) || changed {
			t.Fatalf("missing evidence error = %v, changed = %v", err, changed)
		}

		command := task035SeriesTransitionCommand(current, domain.SeriesStateCompleted)
		secondID := current.Series.SecondParticipantID
		command.Terminal.WinnerID = &secondID
		if _, changed, err := game.Transition(current, command); !errors.Is(err, game.ErrInvalidSeriesExecution) || changed {
			t.Fatalf("incompatible winner error = %v, changed = %v", err, changed)
		}
	})

	t.Run("replay cannot reactivate directly", func(t *testing.T) {
		t.Parallel()

		current := task035SeriesExecution(domain.SeriesStateReplayRequired)
		if _, changed, err := game.Transition(current, game.TransitionCommand{
			NextState: domain.SeriesStateActive,
		}); !errors.Is(err, game.ErrSeriesExecutionTransition) || changed {
			t.Fatalf("direct replay activation error = %v, changed = %v", err, changed)
		}
	})
}

func task035SeriesExecution(state domain.SeriesState) game.Execution {
	firstID := task035ID(3)
	secondID := task035ID(4)
	series := domain.Series{
		ID: task035ID(1), TournamentID: task035ID(2), FirstParticipantID: firstID,
		SecondParticipantID: secondID, Format: domain.SeriesFormatBO1, State: state,
	}
	execution := game.Execution{Series: series}
	switch state {
	case domain.SeriesStatePlanned,
		domain.SeriesStateLocked,
		domain.SeriesStateDraft,
		domain.SeriesStateReady,
		domain.SeriesStateActive,
		domain.SeriesStateReplayRequired:
	case domain.SeriesStateTechnicalPause:
		resume := domain.SeriesStateActive
		execution.ResumeState = &resume
	case domain.SeriesStateCompleted:
		task035ApplySeriesTerminal(&execution.Series, true)
	case domain.SeriesStateCancelled:
		task035ApplySeriesTerminal(&execution.Series, false)
	}
	return execution
}

func task035SeriesTransitionCommand(
	current game.Execution,
	next domain.SeriesState,
) game.TransitionCommand {
	command := game.TransitionCommand{NextState: next}
	if next == current.Series.State {
		return command
	}
	if next == domain.SeriesStateCompleted || next == domain.SeriesStateCancelled {
		series := current.Series
		task035ApplySeriesTerminal(&series, next == domain.SeriesStateCompleted)
		command.Terminal = &game.TerminalEvidence{
			Score: series.Score, WinnerID: series.WinnerID,
			ScoreRevisionID:  series.CurrentScoreRevisionID,
			ResultRevisionID: series.CurrentResultRevisionID,
		}
	}
	return command
}

func task035ApplySeriesTerminal(series *domain.Series, completed bool) {
	scoreRevisionID := domain.SeriesScoreRevisionID(task035ID(5))
	resultRevisionID := domain.OfficialResultRevisionID(task035ID(6))
	series.CurrentScoreRevisionID = &scoreRevisionID
	series.CurrentResultRevisionID = &resultRevisionID
	if completed {
		winnerID := series.FirstParticipantID
		series.Score = domain.SeriesScore{FirstParticipantWins: 1}
		series.WinnerID = &winnerID
	}
}

func task035ID(number int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("35000000-0000-0000-0000-%012d", number))
}
