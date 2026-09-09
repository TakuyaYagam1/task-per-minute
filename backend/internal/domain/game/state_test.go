package game_test

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	game "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
)

func task035ID(number int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("00000000-0000-0000-0000-%012d", number))
}

func TestGameSlotAttemptTransitions(t *testing.T) {
	t.Parallel()

	t.Run("opens the first attempt and replays only a void attempt", func(t *testing.T) {
		t.Parallel()

		slot := task035GameSlot()
		first, changed, err := game.OpenSlotAttempt(slot, task035ID(20))
		if err != nil || !changed || len(first.Attempts) != 1 || first.Attempts[0].AttemptNo != 1 {
			t.Fatalf("OpenGameSlotAttempt(first) error = %v, changed = %v, slot = %+v", err, changed, first)
		}
		if len(slot.Attempts) != 0 {
			t.Fatal("opening an attempt mutated the caller slot")
		}
		retried, changed, err := game.OpenSlotAttempt(first, task035ID(20))
		if err != nil || changed || !reflect.DeepEqual(retried, first) {
			t.Fatalf("OpenGameSlotAttempt(retry) error = %v, changed = %v", err, changed)
		}
		if _, changed, err := game.OpenSlotAttempt(first, task035ID(21)); !errors.Is(err, game.ErrGameAttemptConflict) || changed {
			t.Fatalf("second live attempt error = %v, changed = %v", err, changed)
		}

		ready, _, err := game.TransitionSlotAttempt(first, task035GameTransitionCommand(
			first.Attempts[0], domain.GameStateReady, "",
		))
		if err != nil {
			t.Fatalf("ready transition error = %v", err)
		}
		voided, _, err := game.TransitionSlotAttempt(ready, task035GameTransitionCommand(
			ready.Attempts[0], domain.GameStateVoid, domain.GameResultReasonNoSolve,
		))
		if err != nil {
			t.Fatalf("void transition error = %v", err)
		}
		second, changed, err := game.OpenSlotAttempt(voided, task035ID(21))
		if err != nil || !changed || len(second.Attempts) != 2 || second.Attempts[1].AttemptNo != 2 {
			t.Fatalf("OpenGameSlotAttempt(replay) error = %v, changed = %v, slot = %+v", err, changed, second)
		}
		if err := second.Validate(); err != nil {
			t.Fatalf("replay slot Validate() error = %v", err)
		}
	})

	t.Run("rejects replay after every other terminal state", func(t *testing.T) {
		t.Parallel()

		for _, state := range []domain.GameState{
			domain.GameStateCompleted,
			domain.GameStateCancelled,
			domain.GameStateSuperseded,
		} {
			slot := task035GameSlotWithState(state)
			if _, changed, err := game.OpenSlotAttempt(slot, task035ID(22)); !errors.Is(err, game.ErrGameReplayUnavailable) || changed {
				t.Fatalf("replay after %s error = %v, changed = %v", state, err, changed)
			}
		}
	})

	t.Run("applies the full state matrix", func(t *testing.T) {
		t.Parallel()

		states := []domain.GameState{
			domain.GameStatePlanned,
			domain.GameStateReady,
			domain.GameStateActive,
			domain.GameStatePaused,
			domain.GameStateCompleted,
			domain.GameStateVoid,
			domain.GameStateCancelled,
			domain.GameStateSuperseded,
		}
		allowed := map[domain.GameState]map[domain.GameState]bool{
			domain.GameStatePlanned: {
				domain.GameStatePlanned: true, domain.GameStateReady: true,
				domain.GameStateCancelled: true, domain.GameStateSuperseded: true,
			},
			domain.GameStateReady: {
				domain.GameStateReady: true, domain.GameStatePlanned: true,
				domain.GameStateActive: true, domain.GameStateVoid: true,
				domain.GameStateCancelled: true, domain.GameStateSuperseded: true,
			},
			domain.GameStateActive: {
				domain.GameStateActive: true, domain.GameStatePaused: true,
				domain.GameStateCompleted: true, domain.GameStateVoid: true,
				domain.GameStateCancelled: true, domain.GameStateSuperseded: true,
			},
			domain.GameStatePaused: {
				domain.GameStatePaused: true, domain.GameStateActive: true,
				domain.GameStateCompleted: true, domain.GameStateVoid: true,
				domain.GameStateCancelled: true, domain.GameStateSuperseded: true,
			},
			domain.GameStateCompleted: {
				domain.GameStateCompleted: true, domain.GameStateSuperseded: true,
			},
			domain.GameStateVoid: {
				domain.GameStateVoid: true, domain.GameStateSuperseded: true,
			},
			domain.GameStateCancelled: {
				domain.GameStateCancelled: true, domain.GameStateSuperseded: true,
			},
			domain.GameStateSuperseded: {domain.GameStateSuperseded: true},
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
						next, changed, err := game.TransitionSlotAttempt(slot, command)
						if !allowed[from][to] {
							if !errors.Is(err, game.ErrGameAttemptTransition) || changed {
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

		for _, terminal := range []domain.GameState{
			domain.GameStateCompleted,
			domain.GameStateVoid,
			domain.GameStateCancelled,
			domain.GameStateSuperseded,
		} {
			currentState := domain.GameStateActive
			if terminal == domain.GameStateSuperseded {
				currentState = domain.GameStateCompleted
			}
			slot := task035GameSlotWithState(currentState)
			for _, reason := range task035AllGameReasons() {
				command := task035GameTransitionCommand(slot.Attempts[0], terminal, reason)
				_, changed, err := game.TransitionSlotAttempt(slot, command)
				if reason.IsLegalFor(terminal) {
					if err != nil || !changed {
						t.Errorf("%s reason %s error = %v, changed = %v", terminal, reason, err, changed)
					}
					continue
				}
				if !errors.Is(err, game.ErrInvalidGameAttemptTransition) || changed {
					t.Errorf("illegal %s reason %s error = %v, changed = %v", terminal, reason, err, changed)
				}
			}
		}
	})

	t.Run("requires exact current attempt authority", func(t *testing.T) {
		t.Parallel()

		slot := task035GameSlotWithState(domain.GameStateActive)
		command := task035GameTransitionCommand(slot.Attempts[0], domain.GameStateCompleted, domain.GameResultReasonSolved)
		command.ExpectedAttemptNo++
		if _, changed, err := game.TransitionSlotAttempt(slot, command); !errors.Is(err, game.ErrGameAttemptConflict) || changed {
			t.Fatalf("stale attempt number error = %v, changed = %v", err, changed)
		}
		command.ExpectedAttemptNo--
		command.ExpectedState = domain.GameStatePaused
		if _, changed, err := game.TransitionSlotAttempt(slot, command); !errors.Is(err, game.ErrGameAttemptConflict) || changed {
			t.Fatalf("stale state error = %v, changed = %v", err, changed)
		}
	})

	t.Run("parallel transitions do not mutate shared input", func(t *testing.T) {
		t.Parallel()

		slot := task035GameSlotWithState(domain.GameStateReady)
		command := task035GameTransitionCommand(slot.Attempts[0], domain.GameStateActive, "")
		const workers = 32
		var wait sync.WaitGroup
		errorsFound := make(chan error, workers)
		for range workers {
			wait.Add(1)
			go func() {
				defer wait.Done()
				next, changed, err := game.TransitionSlotAttempt(slot, command)
				if err != nil || !changed || next.Attempts[0].State != domain.GameStateActive {
					errorsFound <- fmt.Errorf("transition error = %w, changed = %v", err, changed)
				}
			}()
		}
		wait.Wait()
		close(errorsFound)
		for err := range errorsFound {
			t.Error(err)
		}
		if slot.Attempts[0].State != domain.GameStateReady {
			t.Fatalf("shared input state = %s, want ready", slot.Attempts[0].State)
		}
	})
}

func task035GameSlot() domain.GameSlot {
	return domain.GameSlot{
		ID: task035ID(10), SeriesID: task035ID(1), Position: 1,
		Category: domain.CategoryWeb, ScoreBefore: domain.SeriesScore{},
	}
}

func task035GameSlotWithState(state domain.GameState) domain.GameSlot {
	slot := task035GameSlot()
	game := domain.Game{ID: task035ID(20), SlotID: slot.ID, AttemptNo: 1, State: state}
	if state.IsTerminal() {
		reason := task035ReasonForState(state)
		resultRevisionID := domain.OfficialResultRevisionID(task035ID(30))
		game.ResultReason = reason
		game.ResultRevisionID = &resultRevisionID
		if state == domain.GameStateCompleted {
			winnerID := task035ID(3)
			game.WinnerID = &winnerID
		}
	}
	slot.Attempts = []domain.Game{game}
	return slot
}

func task035GameTransitionCommand(
	current domain.Game,
	next domain.GameState,
	reason domain.GameResultReason,
) game.TransitionCommand {
	command := game.TransitionCommand{
		GameID: current.ID, ExpectedAttemptNo: current.AttemptNo,
		ExpectedState: current.State, NextState: next,
	}
	if next == current.State {
		return command
	}
	if next.IsTerminal() {
		resultRevisionID := domain.OfficialResultRevisionID(task035ID(31))
		command.Terminal = &game.TerminalEvidence{Reason: reason, ResultRevisionID: &resultRevisionID}
		if next == domain.GameStateCompleted {
			winnerID := task035ID(3)
			command.Terminal.WinnerID = &winnerID
		}
	}
	return command
}

func task035ReasonForState(state domain.GameState) domain.GameResultReason {
	switch state {
	case domain.GameStateCompleted:
		return domain.GameResultReasonSolved
	case domain.GameStateVoid:
		return domain.GameResultReasonNoSolve
	case domain.GameStateCancelled:
		return domain.GameResultReasonSeriesCancelled
	case domain.GameStateSuperseded:
		return domain.GameResultReasonDerivedRevisionSuperseded
	case domain.GameStatePlanned, domain.GameStateReady,
		domain.GameStateActive, domain.GameStatePaused:
		return ""
	}
	return ""
}

func task035AllGameReasons() []domain.GameResultReason {
	return []domain.GameResultReason{
		domain.GameResultReasonSolved,
		domain.GameResultReasonSurrender,
		domain.GameResultReasonOperatorForfeit,
		domain.GameResultReasonNoSolve,
		domain.GameResultReasonTaskFailure,
		domain.GameResultReasonCommonPlatformFailure,
		domain.GameResultReasonDisconnect,
		domain.GameResultReasonExecutionEpochBreak,
		domain.GameResultReasonNoShow,
		domain.GameResultReasonSeriesCancelled,
		domain.GameResultReasonTournamentCancelled,
		domain.GameResultReasonDerivedRevisionSuperseded,
	}
}
