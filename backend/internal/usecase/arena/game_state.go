package arena

import (
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrInvalidGameAttemptTransition = errors.New("invalid Arena Game attempt transition")
	ErrGameAttemptTransition        = errors.New("arena Game attempt transition is not allowed")
	ErrGameAttemptConflict          = errors.New("arena Game attempt authority conflict")
	ErrGameReplayUnavailable        = errors.New("arena Game slot cannot open another attempt")
)

var gameAttemptTransitions = map[domain.ArenaGameState]map[domain.ArenaGameState]struct{}{
	domain.ArenaGameStatePlanned: {
		domain.ArenaGameStateReady: {}, domain.ArenaGameStateCancelled: {},
		domain.ArenaGameStateSuperseded: {},
	},
	domain.ArenaGameStateReady: {
		domain.ArenaGameStatePlanned: {}, domain.ArenaGameStateActive: {}, domain.ArenaGameStateVoid: {},
		domain.ArenaGameStateCancelled: {}, domain.ArenaGameStateSuperseded: {},
	},
	domain.ArenaGameStateActive: {
		domain.ArenaGameStatePaused: {}, domain.ArenaGameStateCompleted: {}, domain.ArenaGameStateVoid: {},
		domain.ArenaGameStateCancelled: {}, domain.ArenaGameStateSuperseded: {},
	},
	domain.ArenaGameStatePaused: {
		domain.ArenaGameStateActive: {}, domain.ArenaGameStateCompleted: {}, domain.ArenaGameStateVoid: {},
		domain.ArenaGameStateCancelled: {}, domain.ArenaGameStateSuperseded: {},
	},
	domain.ArenaGameStateCompleted: {domain.ArenaGameStateSuperseded: {}},
	domain.ArenaGameStateVoid:      {domain.ArenaGameStateSuperseded: {}},
	domain.ArenaGameStateCancelled: {domain.ArenaGameStateSuperseded: {}},
}

type GameTerminalEvidence struct {
	Reason           domain.ArenaGameResultReason
	WinnerID         *uuid.UUID
	ResultRevisionID *domain.ArenaOfficialResultRevisionID
}

type GameAttemptTransitionCommand struct {
	GameID            uuid.UUID
	ExpectedAttemptNo int
	ExpectedState     domain.ArenaGameState
	NextState         domain.ArenaGameState
	Terminal          *GameTerminalEvidence
}

func OpenGameSlotAttempt(
	current domain.ArenaGameSlot,
	gameID uuid.UUID,
) (domain.ArenaGameSlot, bool, error) {
	if gameID == uuid.Nil {
		return domain.ArenaGameSlot{}, false, gameAttemptError("missing Game identity")
	}
	if err := current.Validate(); err != nil {
		return domain.ArenaGameSlot{}, false, gameAttemptError("slot: %v", err)
	}
	next := cloneArenaGameSlot(current)
	if len(next.Attempts) == 0 {
		next.Attempts = append(next.Attempts, newPlannedGameAttempt(next.ID, gameID, 1))
		return validateOpenedGameSlot(next)
	}

	last := next.Attempts[len(next.Attempts)-1]
	if last.ID == gameID {
		return next, false, nil
	}
	if gameSlotContainsAttempt(next, gameID) || !last.State.IsTerminal() {
		return domain.ArenaGameSlot{}, false, ErrGameAttemptConflict
	}
	if last.State != domain.ArenaGameStateVoid {
		return domain.ArenaGameSlot{}, false, ErrGameReplayUnavailable
	}
	next.Attempts = append(next.Attempts, newPlannedGameAttempt(next.ID, gameID, last.AttemptNo+1))
	return validateOpenedGameSlot(next)
}

func TransitionGameSlotAttempt(
	current domain.ArenaGameSlot,
	command GameAttemptTransitionCommand,
) (domain.ArenaGameSlot, bool, error) {
	if err := validateGameAttemptTransitionCommand(command); err != nil {
		return domain.ArenaGameSlot{}, false, err
	}
	if err := current.Validate(); err != nil {
		return domain.ArenaGameSlot{}, false, gameAttemptError("slot: %v", err)
	}
	if len(current.Attempts) == 0 {
		return domain.ArenaGameSlot{}, false, ErrGameAttemptConflict
	}
	currentAttempt := current.Attempts[len(current.Attempts)-1]
	if currentAttempt.ID != command.GameID || currentAttempt.AttemptNo != command.ExpectedAttemptNo ||
		currentAttempt.State != command.ExpectedState {
		return domain.ArenaGameSlot{}, false, ErrGameAttemptConflict
	}
	if command.NextState == currentAttempt.State {
		if command.Terminal != nil {
			return domain.ArenaGameSlot{}, false, gameAttemptError("no-op transition has result evidence")
		}
		return cloneArenaGameSlot(current), false, nil
	}
	if !canTransitionGameAttempt(currentAttempt.State, command.NextState) {
		return domain.ArenaGameSlot{}, false, fmt.Errorf(
			"%w: %s -> %s",
			ErrGameAttemptTransition,
			currentAttempt.State,
			command.NextState,
		)
	}

	next := cloneArenaGameSlot(current)
	nextAttempt := &next.Attempts[len(next.Attempts)-1]
	nextAttempt.State = command.NextState
	if command.NextState.IsTerminal() {
		if command.Terminal == nil {
			return domain.ArenaGameSlot{}, false, gameAttemptError("terminal transition requires result evidence")
		}
		applyGameTerminalEvidence(nextAttempt, *command.Terminal)
	} else if command.Terminal != nil {
		return domain.ArenaGameSlot{}, false, gameAttemptError("non-terminal transition has result evidence")
	}
	if err := next.Validate(); err != nil {
		return domain.ArenaGameSlot{}, false, gameAttemptError("result: %v", err)
	}
	return next, true, nil
}

func validateGameAttemptTransitionCommand(command GameAttemptTransitionCommand) error {
	if command.GameID == uuid.Nil || command.ExpectedAttemptNo < 1 || !command.ExpectedState.IsValid() ||
		!command.NextState.IsValid() {
		return gameAttemptError("missing identity, attempt number, or state")
	}
	return nil
}

func canTransitionGameAttempt(current, next domain.ArenaGameState) bool {
	_, allowed := gameAttemptTransitions[current][next]
	return allowed
}

func applyGameTerminalEvidence(attempt *domain.ArenaGame, evidence GameTerminalEvidence) {
	attempt.ResultReason = evidence.Reason
	attempt.WinnerID = cloneUUIDPointer(evidence.WinnerID)
	attempt.ResultRevisionID = cloneOfficialResultRevisionIDPointer(evidence.ResultRevisionID)
}

func newPlannedGameAttempt(slotID, gameID uuid.UUID, attemptNo int) domain.ArenaGame {
	return domain.ArenaGame{
		ID: gameID, SlotID: slotID, AttemptNo: attemptNo, State: domain.ArenaGameStatePlanned,
	}
}

func validateOpenedGameSlot(slot domain.ArenaGameSlot) (domain.ArenaGameSlot, bool, error) {
	if err := slot.Validate(); err != nil {
		return domain.ArenaGameSlot{}, false, gameAttemptError("opened slot: %v", err)
	}
	return slot, true, nil
}

func gameSlotContainsAttempt(slot domain.ArenaGameSlot, gameID uuid.UUID) bool {
	for _, attempt := range slot.Attempts {
		if attempt.ID == gameID {
			return true
		}
	}
	return false
}

func cloneArenaGameSlot(slot domain.ArenaGameSlot) domain.ArenaGameSlot {
	cloned := slot
	cloned.Attempts = make([]domain.ArenaGame, len(slot.Attempts))
	for index := range slot.Attempts {
		cloned.Attempts[index] = cloneArenaGame(slot.Attempts[index])
	}
	return cloned
}

func cloneArenaGame(game domain.ArenaGame) domain.ArenaGame {
	cloned := game
	cloned.WinnerID = cloneUUIDPointer(game.WinnerID)
	cloned.ResultRevisionID = cloneOfficialResultRevisionIDPointer(game.ResultRevisionID)
	return cloned
}

func gameAttemptError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidGameAttemptTransition, fmt.Sprintf(format, args...))
}
