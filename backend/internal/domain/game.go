package domain

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

type GameState string

const (
	GameStatePlanned    GameState = "planned"
	GameStateReady      GameState = "ready"
	GameStateActive     GameState = "active"
	GameStatePaused     GameState = "paused"
	GameStateCompleted  GameState = "completed"
	GameStateVoid       GameState = "void"
	GameStateCancelled  GameState = "cancelled"
	GameStateSuperseded GameState = "superseded"
)

type GameResultReason string

const (
	GameResultReasonSolved                    GameResultReason = "solved"
	GameResultReasonSurrender                 GameResultReason = "surrender"
	GameResultReasonOperatorForfeit           GameResultReason = "operator_forfeit"
	GameResultReasonNoSolve                   GameResultReason = "no_solve"
	GameResultReasonTaskFailure               GameResultReason = "task_failure"
	GameResultReasonCommonPlatformFailure     GameResultReason = "common_platform_failure"
	GameResultReasonDisconnect                GameResultReason = "disconnect"
	GameResultReasonExecutionEpochBreak       GameResultReason = "execution_epoch_break"
	GameResultReasonNoShow                    GameResultReason = "no_show"
	GameResultReasonSeriesCancelled           GameResultReason = "series_cancelled"
	GameResultReasonTournamentCancelled       GameResultReason = "tournament_cancelled"
	GameResultReasonDerivedRevisionSuperseded GameResultReason = "derived_revision_superseded"
)

var (
	ErrInvalidGame     = errors.New("invalid game")
	ErrInvalidGameSlot = errors.New("invalid game slot")
)

type Game struct {
	ID               uuid.UUID
	SlotID           uuid.UUID
	AttemptNo        int
	State            GameState
	ResultReason     GameResultReason
	WinnerID         *uuid.UUID
	ResultRevisionID *OfficialResultRevisionID
}

type GameSlot struct {
	ID          uuid.UUID
	SeriesID    uuid.UUID
	Position    int
	Category    Category
	ScoreBefore SeriesScore
	Attempts    []Game
}

func (s GameState) IsValid() bool {
	switch s {
	case GameStatePlanned,
		GameStateReady,
		GameStateActive,
		GameStatePaused,
		GameStateCompleted,
		GameStateVoid,
		GameStateCancelled,
		GameStateSuperseded:
		return true
	}
	return false
}

func (s GameState) IsTerminal() bool {
	switch s {
	case GameStateCompleted, GameStateVoid, GameStateCancelled, GameStateSuperseded:
		return true
	case GameStatePlanned, GameStateReady, GameStateActive, GameStatePaused:
		return false
	}
	return false
}

func (r GameResultReason) IsValid() bool {
	return r.IsLegalFor(GameStateCompleted) ||
		r.IsLegalFor(GameStateVoid) ||
		r.IsLegalFor(GameStateCancelled) ||
		r.IsLegalFor(GameStateSuperseded)
}

func (r GameResultReason) IsLegalFor(state GameState) bool {
	switch state {
	case GameStateCompleted:
		return r == GameResultReasonSolved ||
			r == GameResultReasonSurrender ||
			r == GameResultReasonOperatorForfeit
	case GameStateVoid:
		return r == GameResultReasonNoSolve ||
			r == GameResultReasonTaskFailure ||
			r == GameResultReasonCommonPlatformFailure ||
			r == GameResultReasonDisconnect ||
			r == GameResultReasonExecutionEpochBreak
	case GameStateCancelled:
		return r == GameResultReasonNoShow ||
			r == GameResultReasonSeriesCancelled ||
			r == GameResultReasonTournamentCancelled
	case GameStateSuperseded:
		return r == GameResultReasonDerivedRevisionSuperseded
	case GameStatePlanned, GameStateReady, GameStateActive, GameStatePaused:
		return false
	default:
		return false
	}
}

func (g Game) Validate() error {
	if g.ID == uuid.Nil || g.SlotID == uuid.Nil || g.AttemptNo < 1 {
		return fmt.Errorf("%w: missing identity or attempt number", ErrInvalidGame)
	}
	if !g.State.IsValid() {
		return fmt.Errorf("%w: unknown state %q", ErrInvalidGame, g.State)
	}
	if !g.State.IsTerminal() {
		return g.validateNonTerminalEvidence()
	}
	return g.validateTerminalEvidence()
}

func (g Game) validateNonTerminalEvidence() error {
	if g.ResultReason != "" || g.WinnerID != nil || g.ResultRevisionID != nil {
		return fmt.Errorf("%w: non-terminal game has result evidence", ErrInvalidGame)
	}
	return nil
}

func (g Game) validateTerminalEvidence() error {
	if !g.ResultReason.IsLegalFor(g.State) {
		return fmt.Errorf("%w: reason %q is illegal for %s", ErrInvalidGame, g.ResultReason, g.State)
	}
	if g.ResultRevisionID == nil || g.ResultRevisionID.IsZero() {
		return fmt.Errorf("%w: terminal game requires result revision", ErrInvalidGame)
	}
	if g.State == GameStateCompleted {
		if g.WinnerID == nil || *g.WinnerID == uuid.Nil {
			return fmt.Errorf("%w: completed game requires winner", ErrInvalidGame)
		}
		return nil
	}
	if g.WinnerID != nil {
		return fmt.Errorf("%w: non-completed terminal game has winner", ErrInvalidGame)
	}
	return nil
}

func (s GameSlot) Validate() error {
	if s.ID == uuid.Nil || s.SeriesID == uuid.Nil || s.Position < 1 {
		return fmt.Errorf("%w: missing identity or position", ErrInvalidGameSlot)
	}
	if !s.Category.IsValid() {
		return fmt.Errorf("%w: invalid category", ErrInvalidGameSlot)
	}
	if err := s.ScoreBefore.Validate(SeriesFormatBO3); err != nil {
		return fmt.Errorf("%w: invalid score position", ErrInvalidGameSlot)
	}
	return s.validateAttempts()
}

func (s GameSlot) AttemptCount() int {
	return len(s.Attempts)
}

func (s GameSlot) NextAttemptNo() int {
	return len(s.Attempts) + 1
}

func (s GameSlot) validateAttempts() error {
	ids := make(map[uuid.UUID]struct{}, len(s.Attempts))
	for i, attempt := range s.Attempts {
		if err := attempt.Validate(); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidGameSlot, err)
		}
		if attempt.SlotID != s.ID || attempt.AttemptNo != i+1 {
			return fmt.Errorf("%w: attempts are not ordered", ErrInvalidGameSlot)
		}
		if _, exists := ids[attempt.ID]; exists {
			return fmt.Errorf("%w: duplicate attempt identity", ErrInvalidGameSlot)
		}
		if i < len(s.Attempts)-1 && attempt.State != GameStateVoid {
			return fmt.Errorf("%w: only a void attempt may be replayed", ErrInvalidGameSlot)
		}
		ids[attempt.ID] = struct{}{}
	}
	return nil
}
