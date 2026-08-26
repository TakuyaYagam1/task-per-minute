package domain

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

type ArenaGameState string

const (
	ArenaGameStatePlanned    ArenaGameState = "planned"
	ArenaGameStateReady      ArenaGameState = "ready"
	ArenaGameStateActive     ArenaGameState = "active"
	ArenaGameStatePaused     ArenaGameState = "paused"
	ArenaGameStateCompleted  ArenaGameState = "completed"
	ArenaGameStateVoid       ArenaGameState = "void"
	ArenaGameStateCancelled  ArenaGameState = "cancelled"
	ArenaGameStateSuperseded ArenaGameState = "superseded"
)

type ArenaGameResultReason string

const (
	ArenaGameResultReasonSolved                    ArenaGameResultReason = "solved"
	ArenaGameResultReasonSurrender                 ArenaGameResultReason = "surrender"
	ArenaGameResultReasonOperatorForfeit           ArenaGameResultReason = "operator_forfeit"
	ArenaGameResultReasonNoSolve                   ArenaGameResultReason = "no_solve"
	ArenaGameResultReasonTaskFailure               ArenaGameResultReason = "task_failure"
	ArenaGameResultReasonCommonPlatformFailure     ArenaGameResultReason = "common_platform_failure"
	ArenaGameResultReasonDisconnect                ArenaGameResultReason = "disconnect"
	ArenaGameResultReasonExecutionEpochBreak       ArenaGameResultReason = "execution_epoch_break"
	ArenaGameResultReasonNoShow                    ArenaGameResultReason = "no_show"
	ArenaGameResultReasonSeriesCancelled           ArenaGameResultReason = "series_cancelled"
	ArenaGameResultReasonTournamentCancelled       ArenaGameResultReason = "tournament_cancelled"
	ArenaGameResultReasonDerivedRevisionSuperseded ArenaGameResultReason = "derived_revision_superseded"
)

var (
	ErrInvalidArenaGame     = errors.New("invalid arena game")
	ErrInvalidArenaGameSlot = errors.New("invalid arena game slot")
)

type ArenaGame struct {
	ID               uuid.UUID
	SlotID           uuid.UUID
	AttemptNo        int
	State            ArenaGameState
	ResultReason     ArenaGameResultReason
	WinnerID         *uuid.UUID
	ResultRevisionID *ArenaOfficialResultRevisionID
}

type ArenaGameSlot struct {
	ID          uuid.UUID
	SeriesID    uuid.UUID
	Position    int
	Category    Category
	ScoreBefore ArenaSeriesScore
	Attempts    []ArenaGame
}

func (s ArenaGameState) IsValid() bool {
	switch s {
	case ArenaGameStatePlanned,
		ArenaGameStateReady,
		ArenaGameStateActive,
		ArenaGameStatePaused,
		ArenaGameStateCompleted,
		ArenaGameStateVoid,
		ArenaGameStateCancelled,
		ArenaGameStateSuperseded:
		return true
	}
	return false
}

func (s ArenaGameState) IsTerminal() bool {
	switch s {
	case ArenaGameStateCompleted, ArenaGameStateVoid, ArenaGameStateCancelled, ArenaGameStateSuperseded:
		return true
	}
	return false
}

func (r ArenaGameResultReason) IsValid() bool {
	return r.IsLegalFor(ArenaGameStateCompleted) ||
		r.IsLegalFor(ArenaGameStateVoid) ||
		r.IsLegalFor(ArenaGameStateCancelled) ||
		r.IsLegalFor(ArenaGameStateSuperseded)
}

func (r ArenaGameResultReason) IsLegalFor(state ArenaGameState) bool {
	switch state {
	case ArenaGameStateCompleted:
		return r == ArenaGameResultReasonSolved ||
			r == ArenaGameResultReasonSurrender ||
			r == ArenaGameResultReasonOperatorForfeit
	case ArenaGameStateVoid:
		return r == ArenaGameResultReasonNoSolve ||
			r == ArenaGameResultReasonTaskFailure ||
			r == ArenaGameResultReasonCommonPlatformFailure ||
			r == ArenaGameResultReasonDisconnect ||
			r == ArenaGameResultReasonExecutionEpochBreak
	case ArenaGameStateCancelled:
		return r == ArenaGameResultReasonNoShow ||
			r == ArenaGameResultReasonSeriesCancelled ||
			r == ArenaGameResultReasonTournamentCancelled
	case ArenaGameStateSuperseded:
		return r == ArenaGameResultReasonDerivedRevisionSuperseded
	default:
		return false
	}
}

func (g ArenaGame) Validate() error {
	if g.ID == uuid.Nil || g.SlotID == uuid.Nil || g.AttemptNo < 1 {
		return fmt.Errorf("%w: missing identity or attempt number", ErrInvalidArenaGame)
	}
	if !g.State.IsValid() {
		return fmt.Errorf("%w: unknown state %q", ErrInvalidArenaGame, g.State)
	}
	if !g.State.IsTerminal() {
		return g.validateNonTerminalEvidence()
	}
	return g.validateTerminalEvidence()
}

func (g ArenaGame) validateNonTerminalEvidence() error {
	if g.ResultReason != "" || g.WinnerID != nil || g.ResultRevisionID != nil {
		return fmt.Errorf("%w: non-terminal game has result evidence", ErrInvalidArenaGame)
	}
	return nil
}

func (g ArenaGame) validateTerminalEvidence() error {
	if !g.ResultReason.IsLegalFor(g.State) {
		return fmt.Errorf("%w: reason %q is illegal for %s", ErrInvalidArenaGame, g.ResultReason, g.State)
	}
	if g.ResultRevisionID == nil || g.ResultRevisionID.IsZero() {
		return fmt.Errorf("%w: terminal game requires result revision", ErrInvalidArenaGame)
	}
	if g.State == ArenaGameStateCompleted {
		if g.WinnerID == nil || *g.WinnerID == uuid.Nil {
			return fmt.Errorf("%w: completed game requires winner", ErrInvalidArenaGame)
		}
		return nil
	}
	if g.WinnerID != nil {
		return fmt.Errorf("%w: non-completed terminal game has winner", ErrInvalidArenaGame)
	}
	return nil
}

func (s ArenaGameSlot) Validate() error {
	if s.ID == uuid.Nil || s.SeriesID == uuid.Nil || s.Position < 1 {
		return fmt.Errorf("%w: missing identity or position", ErrInvalidArenaGameSlot)
	}
	if !s.Category.IsValid() {
		return fmt.Errorf("%w: invalid category", ErrInvalidArenaGameSlot)
	}
	if err := s.ScoreBefore.Validate(ArenaSeriesFormatBO3); err != nil {
		return fmt.Errorf("%w: invalid score position", ErrInvalidArenaGameSlot)
	}
	return s.validateAttempts()
}

func (s ArenaGameSlot) AttemptCount() int {
	return len(s.Attempts)
}

func (s ArenaGameSlot) NextAttemptNo() int {
	return len(s.Attempts) + 1
}

func (s ArenaGameSlot) validateAttempts() error {
	ids := make(map[uuid.UUID]struct{}, len(s.Attempts))
	for i, attempt := range s.Attempts {
		if err := attempt.Validate(); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidArenaGameSlot, err)
		}
		if attempt.SlotID != s.ID || attempt.AttemptNo != i+1 {
			return fmt.Errorf("%w: attempts are not ordered", ErrInvalidArenaGameSlot)
		}
		if _, exists := ids[attempt.ID]; exists {
			return fmt.Errorf("%w: duplicate attempt identity", ErrInvalidArenaGameSlot)
		}
		if i < len(s.Attempts)-1 && attempt.State != ArenaGameStateVoid {
			return fmt.Errorf("%w: only a void attempt may be replayed", ErrInvalidArenaGameSlot)
		}
		ids[attempt.ID] = struct{}{}
	}
	return nil
}
