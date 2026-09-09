package domain

import (
	"errors"
	"fmt"
	"time"
)

type TournamentState string

const (
	ErrorCodeTournamentNotFound           ErrorCode = "tournament.not_found"
	ErrorCodeTournamentProjectionNotFound ErrorCode = "tournament.projection_not_found"
)

const (
	TournamentStateDraft          TournamentState = "draft"
	TournamentStateRegistration   TournamentState = "registration"
	TournamentStateRosterLocked   TournamentState = "roster_locked"
	TournamentStateSwiss          TournamentState = "swiss"
	TournamentStateGolden         TournamentState = "golden"
	TournamentStatePlayoffs       TournamentState = "playoffs"
	TournamentStateTechnicalPause TournamentState = "technical_pause"
	TournamentStateCompleted      TournamentState = "completed"
	TournamentStateCancelled      TournamentState = "cancelled"
)

var (
	ErrTournamentNotFound           = &Error{Code: ErrorCodeTournamentNotFound, Message: "tournament not found"}
	ErrTournamentProjectionNotFound = &Error{Code: ErrorCodeTournamentProjectionNotFound, Message: "tournament projection not found"}
	ErrInvalidTournamentState       = errors.New("invalid tournament state")
	ErrTournamentTransition         = errors.New("tournament transition is not allowed")
)

var tournamentTransitions = map[TournamentState]map[TournamentState]struct{}{
	TournamentStateDraft: {
		TournamentStateRegistration: {},
		TournamentStateCancelled:    {},
	},
	TournamentStateRegistration: {
		TournamentStateRosterLocked: {},
		TournamentStateCancelled:    {},
	},
	TournamentStateRosterLocked: {
		TournamentStateRegistration: {},
		TournamentStateSwiss:        {},
		TournamentStateCancelled:    {},
	},
	TournamentStateSwiss: {
		TournamentStateGolden:         {},
		TournamentStatePlayoffs:       {},
		TournamentStateTechnicalPause: {},
		TournamentStateCancelled:      {},
	},
	TournamentStateGolden: {
		TournamentStatePlayoffs:       {},
		TournamentStateTechnicalPause: {},
		TournamentStateCancelled:      {},
	},
	TournamentStatePlayoffs: {
		TournamentStateTechnicalPause: {},
		TournamentStateCompleted:      {},
		TournamentStateCancelled:      {},
	},
	TournamentStateTechnicalPause: {
		TournamentStateCancelled: {},
	},
}

type Tournament struct {
	State           TournamentState
	PausedFromState *TournamentState
}

func (s TournamentState) IsValid() bool {
	switch s {
	case TournamentStateDraft,
		TournamentStateRegistration,
		TournamentStateRosterLocked,
		TournamentStateSwiss,
		TournamentStateGolden,
		TournamentStatePlayoffs,
		TournamentStateTechnicalPause,
		TournamentStateCompleted,
		TournamentStateCancelled:
		return true
	}
	return false
}

func (s TournamentState) IsTerminal() bool {
	return s == TournamentStateCompleted || s == TournamentStateCancelled
}

func (s TournamentState) String() string {
	return string(s)
}

func (t Tournament) Validate() error {
	if !t.State.IsValid() {
		return fmt.Errorf("%w: %q", ErrInvalidTournamentState, t.State)
	}
	if t.State == TournamentStateTechnicalPause {
		if t.PausedFromState == nil || !isTournamentPauseOrigin(*t.PausedFromState) {
			return fmt.Errorf("%w: technical pause requires a live origin", ErrInvalidTournamentState)
		}
		return nil
	}
	if t.PausedFromState != nil {
		return fmt.Errorf("%w: pause origin outside technical pause", ErrInvalidTournamentState)
	}
	return nil
}

func (t Tournament) CanTransitionTo(next TournamentState) bool {
	if t.Validate() != nil || !next.IsValid() {
		return false
	}
	if next == t.State {
		return true
	}
	if t.State == TournamentStateTechnicalPause && t.PausedFromState != nil && next == *t.PausedFromState {
		return true
	}
	_, allowed := tournamentTransitions[t.State][next]
	return allowed
}

func (t *Tournament) TransitionTo(next TournamentState) (bool, error) {
	if t == nil {
		return false, fmt.Errorf("%w: nil tournament", ErrInvalidTournamentState)
	}
	if err := t.Validate(); err != nil {
		return false, err
	}
	if !next.IsValid() {
		return false, fmt.Errorf("%w: %q", ErrInvalidTournamentState, next)
	}
	if next == t.State {
		return false, nil
	}
	if !t.CanTransitionTo(next) {
		return false, fmt.Errorf("%w: %s -> %s", ErrTournamentTransition, t.State, next)
	}

	if next == TournamentStateTechnicalPause {
		origin := t.State
		t.PausedFromState = &origin
	} else {
		t.PausedFromState = nil
	}
	t.State = next
	return true, nil
}

func isTournamentPauseOrigin(state TournamentState) bool {
	switch state {
	case TournamentStateSwiss, TournamentStateGolden, TournamentStatePlayoffs:
		return true
	case TournamentStateDraft,
		TournamentStateRegistration,
		TournamentStateRosterLocked,
		TournamentStateTechnicalPause,
		TournamentStateCompleted,
		TournamentStateCancelled:
		return false
	}
	return false
}

func IsValidServerTime(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC
}
