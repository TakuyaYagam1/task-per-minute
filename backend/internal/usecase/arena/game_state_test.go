package arena_test

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestGameSlotAttemptTransitions(t *testing.T) {
	t.Parallel()

	t.Run("opens the first attempt and replays only a void attempt", func(t *testing.T) {
		t.Parallel()

		slot := task035GameSlot()
		first, changed, err := arena.OpenGameSlotAttempt(slot, task035ID(20))
		if err != nil || !changed || len(first.Attempts) != 1 || first.Attempts[0].AttemptNo != 1 {
			t.Fatalf("OpenGameSlotAttempt(first) error = %v, changed = %v, slot = %+v", err, changed, first)
		}
		if len(slot.Attempts) != 0 {
			t.Fatal("opening an attempt mutated the caller slot")
		}
		retried, changed, err := arena.OpenGameSlotAttempt(first, task035ID(20))
		if err != nil || changed || !reflect.DeepEqual(retried, first) {
			t.Fatalf("OpenGameSlotAttempt(retry) error = %v, changed = %v", err, changed)
		}
		if _, changed, err := arena.OpenGameSlotAttempt(first, task035ID(21)); !errors.Is(err, arena.ErrGameAttemptConflict) || changed {
			t.Fatalf("second live attempt error = %v, changed = %v", err, changed)
		}

		ready, _, err := arena.TransitionGameSlotAttempt(first, task035GameTransitionCommand(
			first.Attempts[0], domain.ArenaGameStateReady, "",
		))
		if err != nil {
			t.Fatalf("ready transition error = %v", err)
		}
		voided, _, err := arena.TransitionGameSlotAttempt(ready, task035GameTransitionCommand(
			ready.Attempts[0], domain.ArenaGameStateVoid, domain.ArenaGameResultReasonNoSolve,
		))
		if err != nil {
			t.Fatalf("void transition error = %v", err)
		}
		second, changed, err := arena.OpenGameSlotAttempt(voided, task035ID(21))
		if err != nil || !changed || len(second.Attempts) != 2 || second.Attempts[1].AttemptNo != 2 {
			t.Fatalf("OpenGameSlotAttempt(replay) error = %v, changed = %v, slot = %+v", err, changed, second)
		}
		if err := second.Validate(); err != nil {
			t.Fatalf("replay slot Validate() error = %v", err)
		}
	})

	t.Run("rejects replay after every other terminal state", func(t *testing.T) {
		t.Parallel()

		for _, state := range []domain.ArenaGameState{
			domain.ArenaGameStateCompleted,
			domain.ArenaGameStateCancelled,
			domain.ArenaGameStateSuperseded,
		} {
			slot := task035GameSlotWithState(state)
			if _, changed, err := arena.OpenGameSlotAttempt(slot, task035ID(22)); !errors.Is(err, arena.ErrGameReplayUnavailable) || changed {
				t.Fatalf("replay after %s error = %v, changed = %v", state, err, changed)
			}
		}
	})

	t.Run("applies the full state matrix", func(t *testing.T) {
		t.Parallel()

		states := []domain.ArenaGameState{
			domain.ArenaGameStatePlanned,
			domain.ArenaGameStateReady,
			domain.ArenaGameStateActive,
			domain.ArenaGameStatePaused,
			domain.ArenaGameStateCompleted,
			domain.ArenaGameStateVoid,
			domain.ArenaGameStateCancelled,
			domain.ArenaGameStateSuperseded,
		}
		allowed := map[domain.ArenaGameState]map[domain.ArenaGameState]bool{
			domain.ArenaGameStatePlanned: {
				domain.ArenaGameStatePlanned: true, domain.ArenaGameStateReady: true,
				domain.ArenaGameStateCancelled: true, domain.ArenaGameStateSuperseded: true,
			},
			domain.ArenaGameStateReady: {
				domain.ArenaGameStateReady: true, domain.ArenaGameStatePlanned: true,
				domain.ArenaGameStateActive: true, domain.ArenaGameStateVoid: true,
				domain.ArenaGameStateCancelled: true, domain.ArenaGameStateSuperseded: true,
			},
			domain.ArenaGameStateActive: {
				domain.ArenaGameStateActive: true, domain.ArenaGameStatePaused: true,
				domain.ArenaGameStateCompleted: true, domain.ArenaGameStateVoid: true,
				domain.ArenaGameStateCancelled: true, domain.ArenaGameStateSuperseded: true,
			},
			domain.ArenaGameStatePaused: {
				domain.ArenaGameStatePaused: true, domain.ArenaGameStateActive: true,
				domain.ArenaGameStateCompleted: true, domain.ArenaGameStateVoid: true,
				domain.ArenaGameStateCancelled: true, domain.ArenaGameStateSuperseded: true,
			},
			domain.ArenaGameStateCompleted: {
				domain.ArenaGameStateCompleted: true, domain.ArenaGameStateSuperseded: true,
			},
			domain.ArenaGameStateVoid: {
				domain.ArenaGameStateVoid: true, domain.ArenaGameStateSuperseded: true,
			},
			domain.ArenaGameStateCancelled: {
				domain.ArenaGameStateCancelled: true, domain.ArenaGameStateSuperseded: true,
			},
			domain.ArenaGameStateSuperseded: {domain.ArenaGameStateSuperseded: true},
		}

		for _, from := range states {
			t.Run(string(from), func(t *testing.T) {
				t.Parallel()

				for _, to := range states {
					t.Run(string(to), func(t *testing.T) {
						t.Parallel()

						slot := task035GameSlotWithState(from)
						before := task035GameSlotWithState(from)
						command := task035GameTransitionCommand(slot.Attempts[0], to, task035ReasonForState(to))
						next, changed, err := arena.TransitionGameSlotAttempt(slot, command)
						if !allowed[from][to] {
							if !errors.Is(err, arena.ErrGameAttemptTransition) || changed {
								t.Fatalf("TransitionGameSlotAttempt(%s -> %s) error = %v, changed = %v", from, to, err, changed)
							}
							if !reflect.DeepEqual(slot, before) {
								t.Fatal("rejected transition mutated the caller slot")
							}
							return
						}
						if err != nil {
							t.Fatalf("TransitionGameSlotAttempt(%s -> %s) error = %v", from, to, err)
						}
						if changed != (from != to) || next.Attempts[0].State != to {
							t.Fatalf("TransitionGameSlotAttempt(%s -> %s) state = %s, changed = %v", from, to, next.Attempts[0].State, changed)
						}
						if err := next.Validate(); err != nil {
							t.Fatalf("result Validate() error = %v", err)
						}
						if !reflect.DeepEqual(slot, before) {
							t.Fatal("accepted transition mutated the caller slot")
						}
					})
				}
			})
		}
	})

	t.Run("uses only reasons legal for the terminal entity state", func(t *testing.T) {
		t.Parallel()

		for _, terminal := range []domain.ArenaGameState{
			domain.ArenaGameStateCompleted,
			domain.ArenaGameStateVoid,
			domain.ArenaGameStateCancelled,
			domain.ArenaGameStateSuperseded,
		} {
			currentState := domain.ArenaGameStateActive
			if terminal == domain.ArenaGameStateSuperseded {
				currentState = domain.ArenaGameStateCompleted
			}
			slot := task035GameSlotWithState(currentState)
			for _, reason := range task035AllGameReasons() {
				command := task035GameTransitionCommand(slot.Attempts[0], terminal, reason)
				_, changed, err := arena.TransitionGameSlotAttempt(slot, command)
				if reason.IsLegalFor(terminal) {
					if err != nil || !changed {
						t.Errorf("%s reason %s error = %v, changed = %v", terminal, reason, err, changed)
					}
					continue
				}
				if !errors.Is(err, arena.ErrInvalidGameAttemptTransition) || changed {
					t.Errorf("illegal %s reason %s error = %v, changed = %v", terminal, reason, err, changed)
				}
			}
		}
	})

	t.Run("requires exact current attempt authority", func(t *testing.T) {
		t.Parallel()

		slot := task035GameSlotWithState(domain.ArenaGameStateActive)
		command := task035GameTransitionCommand(slot.Attempts[0], domain.ArenaGameStateCompleted, domain.ArenaGameResultReasonSolved)
		command.ExpectedAttemptNo++
		if _, changed, err := arena.TransitionGameSlotAttempt(slot, command); !errors.Is(err, arena.ErrGameAttemptConflict) || changed {
			t.Fatalf("stale attempt number error = %v, changed = %v", err, changed)
		}
		command.ExpectedAttemptNo--
		command.ExpectedState = domain.ArenaGameStatePaused
		if _, changed, err := arena.TransitionGameSlotAttempt(slot, command); !errors.Is(err, arena.ErrGameAttemptConflict) || changed {
			t.Fatalf("stale state error = %v, changed = %v", err, changed)
		}
	})

	t.Run("parallel transitions do not mutate shared input", func(t *testing.T) {
		t.Parallel()

		slot := task035GameSlotWithState(domain.ArenaGameStateReady)
		command := task035GameTransitionCommand(slot.Attempts[0], domain.ArenaGameStateActive, "")
		const workers = 32
		var wait sync.WaitGroup
		errorsFound := make(chan error, workers)
		for range workers {
			wait.Add(1)
			go func() {
				defer wait.Done()
				next, changed, err := arena.TransitionGameSlotAttempt(slot, command)
				if err != nil || !changed || next.Attempts[0].State != domain.ArenaGameStateActive {
					errorsFound <- fmt.Errorf("transition error = %w, changed = %v", err, changed)
				}
			}()
		}
		wait.Wait()
		close(errorsFound)
		for err := range errorsFound {
			t.Error(err)
		}
		if slot.Attempts[0].State != domain.ArenaGameStateReady {
			t.Fatalf("shared input state = %s, want ready", slot.Attempts[0].State)
		}
	})
}

func task035GameSlot() domain.ArenaGameSlot {
	return domain.ArenaGameSlot{
		ID: task035ID(10), SeriesID: task035ID(1), Position: 1,
		Category: domain.CategoryWeb, ScoreBefore: domain.ArenaSeriesScore{},
	}
}

func task035GameSlotWithState(state domain.ArenaGameState) domain.ArenaGameSlot {
	slot := task035GameSlot()
	game := domain.ArenaGame{ID: task035ID(20), SlotID: slot.ID, AttemptNo: 1, State: state}
	if state.IsTerminal() {
		reason := task035ReasonForState(state)
		resultRevisionID := domain.ArenaOfficialResultRevisionID(task035ID(30))
		game.ResultReason = reason
		game.ResultRevisionID = &resultRevisionID
		if state == domain.ArenaGameStateCompleted {
			winnerID := task035ID(3)
			game.WinnerID = &winnerID
		}
	}
	slot.Attempts = []domain.ArenaGame{game}
	return slot
}

func task035GameTransitionCommand(
	current domain.ArenaGame,
	next domain.ArenaGameState,
	reason domain.ArenaGameResultReason,
) arena.GameAttemptTransitionCommand {
	command := arena.GameAttemptTransitionCommand{
		GameID: current.ID, ExpectedAttemptNo: current.AttemptNo,
		ExpectedState: current.State, NextState: next,
	}
	if next == current.State {
		return command
	}
	if next.IsTerminal() {
		resultRevisionID := domain.ArenaOfficialResultRevisionID(task035ID(31))
		command.Terminal = &arena.GameTerminalEvidence{Reason: reason, ResultRevisionID: &resultRevisionID}
		if next == domain.ArenaGameStateCompleted {
			winnerID := task035ID(3)
			command.Terminal.WinnerID = &winnerID
		}
	}
	return command
}

func task035ReasonForState(state domain.ArenaGameState) domain.ArenaGameResultReason {
	switch state {
	case domain.ArenaGameStateCompleted:
		return domain.ArenaGameResultReasonSolved
	case domain.ArenaGameStateVoid:
		return domain.ArenaGameResultReasonNoSolve
	case domain.ArenaGameStateCancelled:
		return domain.ArenaGameResultReasonSeriesCancelled
	case domain.ArenaGameStateSuperseded:
		return domain.ArenaGameResultReasonDerivedRevisionSuperseded
	case domain.ArenaGameStatePlanned, domain.ArenaGameStateReady,
		domain.ArenaGameStateActive, domain.ArenaGameStatePaused:
		return ""
	}
	return ""
}

func task035AllGameReasons() []domain.ArenaGameResultReason {
	return []domain.ArenaGameResultReason{
		domain.ArenaGameResultReasonSolved,
		domain.ArenaGameResultReasonSurrender,
		domain.ArenaGameResultReasonOperatorForfeit,
		domain.ArenaGameResultReasonNoSolve,
		domain.ArenaGameResultReasonTaskFailure,
		domain.ArenaGameResultReasonCommonPlatformFailure,
		domain.ArenaGameResultReasonDisconnect,
		domain.ArenaGameResultReasonExecutionEpochBreak,
		domain.ArenaGameResultReasonNoShow,
		domain.ArenaGameResultReasonSeriesCancelled,
		domain.ArenaGameResultReasonTournamentCancelled,
		domain.ArenaGameResultReasonDerivedRevisionSuperseded,
	}
}
