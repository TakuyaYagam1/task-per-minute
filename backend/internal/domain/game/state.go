package game

import (
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrInvalidGameAttemptTransition = errors.New("invalid game attempt transition")
	ErrGameAttemptTransition        = errors.New("game attempt transition is not allowed")
	ErrGameAttemptConflict          = errors.New("game attempt authority conflict")
	ErrGameReplayUnavailable        = errors.New("game slot cannot open another attempt")
)

var gameAttemptTransitions = map[domain.GameState]map[domain.GameState]struct{}{
	domain.GameStatePlanned: {
		domain.GameStateReady: {}, domain.GameStateCancelled: {},
		domain.GameStateSuperseded: {},
	},
	domain.GameStateReady: {
		domain.GameStatePlanned: {}, domain.GameStateActive: {}, domain.GameStateVoid: {},
		domain.GameStateCancelled: {}, domain.GameStateSuperseded: {},
	},
	domain.GameStateActive: {
		domain.GameStatePaused: {}, domain.GameStateCompleted: {}, domain.GameStateVoid: {},
		domain.GameStateCancelled: {}, domain.GameStateSuperseded: {},
	},
	domain.GameStatePaused: {
		domain.GameStateActive: {}, domain.GameStateCompleted: {}, domain.GameStateVoid: {},
		domain.GameStateCancelled: {}, domain.GameStateSuperseded: {},
	},
	domain.GameStateCompleted: {domain.GameStateSuperseded: {}},
	domain.GameStateVoid:      {domain.GameStateSuperseded: {}},
	domain.GameStateCancelled: {domain.GameStateSuperseded: {}},
}

type TerminalEvidence struct {
	Reason           domain.GameResultReason
	WinnerID         *uuid.UUID
	ResultRevisionID *domain.OfficialResultRevisionID
}

type TransitionCommand struct {
	GameID            uuid.UUID
	ExpectedAttemptNo int
	ExpectedState     domain.GameState
	NextState         domain.GameState
	Terminal          *TerminalEvidence
}

func OpenSlotAttempt(
	current domain.GameSlot,
	gameID uuid.UUID,
) (domain.GameSlot, bool, error) {
	if gameID == uuid.Nil {
		return domain.GameSlot{}, false, gameAttemptError("missing Game identity")
	}
	if err := current.Validate(); err != nil {
		return domain.GameSlot{}, false, gameAttemptError("slot: %v", err)
	}
	next := cloneGameSlot(current)
	if len(next.Attempts) == 0 {
		next.Attempts = append(next.Attempts, newPlannedGameAttempt(next.ID, gameID, 1))
		return validateOpenedGameSlot(next)
	}

	last := next.Attempts[len(next.Attempts)-1]
	if last.ID == gameID {
		return next, false, nil
	}
	if gameSlotContainsAttempt(next, gameID) || !last.State.IsTerminal() {
		return domain.GameSlot{}, false, ErrGameAttemptConflict
	}
	if last.State != domain.GameStateVoid {
		return domain.GameSlot{}, false, ErrGameReplayUnavailable
	}
	next.Attempts = append(next.Attempts, newPlannedGameAttempt(next.ID, gameID, last.AttemptNo+1))
	return validateOpenedGameSlot(next)
}

func TransitionSlotAttempt(
	current domain.GameSlot,
	command TransitionCommand,
) (domain.GameSlot, bool, error) {
	if err := validateGameAttemptTransitionCommand(command); err != nil {
		return domain.GameSlot{}, false, err
	}
	if err := current.Validate(); err != nil {
		return domain.GameSlot{}, false, gameAttemptError("slot: %v", err)
	}
	if len(current.Attempts) == 0 {
		return domain.GameSlot{}, false, ErrGameAttemptConflict
	}
	currentAttempt := current.Attempts[len(current.Attempts)-1]
	if currentAttempt.ID != command.GameID || currentAttempt.AttemptNo != command.ExpectedAttemptNo ||
		currentAttempt.State != command.ExpectedState {
		return domain.GameSlot{}, false, ErrGameAttemptConflict
	}
	if command.NextState == currentAttempt.State {
		if command.Terminal != nil {
			return domain.GameSlot{}, false, gameAttemptError("no-op transition has result evidence")
		}
		return cloneGameSlot(current), false, nil
	}
	if !canTransitionGameAttempt(currentAttempt.State, command.NextState) {
		return domain.GameSlot{}, false, fmt.Errorf(
			"%w: %s -> %s",
			ErrGameAttemptTransition,
			currentAttempt.State,
			command.NextState,
		)
	}

	next := cloneGameSlot(current)
	nextAttempt := &next.Attempts[len(next.Attempts)-1]
	nextAttempt.State = command.NextState
	if command.NextState.IsTerminal() {
		if command.Terminal == nil {
			return domain.GameSlot{}, false, gameAttemptError("terminal transition requires result evidence")
		}
		applyGameTerminalEvidence(nextAttempt, *command.Terminal)
	} else if command.Terminal != nil {
		return domain.GameSlot{}, false, gameAttemptError("non-terminal transition has result evidence")
	}
	if err := next.Validate(); err != nil {
		return domain.GameSlot{}, false, gameAttemptError("result: %v", err)
	}
	return next, true, nil
}

func validateGameAttemptTransitionCommand(command TransitionCommand) error {
	if command.GameID == uuid.Nil || command.ExpectedAttemptNo < 1 || !command.ExpectedState.IsValid() ||
		!command.NextState.IsValid() {
		return gameAttemptError("missing identity, attempt number, or state")
	}
	return nil
}

func canTransitionGameAttempt(current, next domain.GameState) bool {
	_, allowed := gameAttemptTransitions[current][next]
	return allowed
}

func applyGameTerminalEvidence(attempt *domain.Game, evidence TerminalEvidence) {
	attempt.ResultReason = evidence.Reason
	attempt.WinnerID = cloneUUIDPointer(evidence.WinnerID)
	attempt.ResultRevisionID = cloneOfficialResultRevisionIDPointer(evidence.ResultRevisionID)
}

func newPlannedGameAttempt(slotID, gameID uuid.UUID, attemptNo int) domain.Game {
	return domain.Game{
		ID: gameID, SlotID: slotID, AttemptNo: attemptNo, State: domain.GameStatePlanned,
	}
}

func validateOpenedGameSlot(slot domain.GameSlot) (domain.GameSlot, bool, error) {
	if err := slot.Validate(); err != nil {
		return domain.GameSlot{}, false, gameAttemptError("opened slot: %v", err)
	}
	return slot, true, nil
}

func gameSlotContainsAttempt(slot domain.GameSlot, gameID uuid.UUID) bool {
	for _, attempt := range slot.Attempts {
		if attempt.ID == gameID {
			return true
		}
	}
	return false
}

func cloneGameSlot(slot domain.GameSlot) domain.GameSlot {
	cloned := slot
	cloned.Attempts = make([]domain.Game, len(slot.Attempts))
	for index := range slot.Attempts {
		cloned.Attempts[index] = cloneGame(slot.Attempts[index])
	}
	return cloned
}

func cloneGame(game domain.Game) domain.Game {
	cloned := game
	cloned.WinnerID = cloneUUIDPointer(game.WinnerID)
	cloned.ResultRevisionID = cloneOfficialResultRevisionIDPointer(game.ResultRevisionID)
	return cloned
}

func gameAttemptError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidGameAttemptTransition, fmt.Sprintf(format, args...))
}
