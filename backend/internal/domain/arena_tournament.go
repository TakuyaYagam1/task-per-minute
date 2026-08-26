package domain

import (
	"errors"
	"fmt"
)

type ArenaTournamentState string

const (
	ArenaTournamentStateDraft          ArenaTournamentState = "draft"
	ArenaTournamentStateRegistration   ArenaTournamentState = "registration"
	ArenaTournamentStateRosterLocked   ArenaTournamentState = "roster_locked"
	ArenaTournamentStateSwiss          ArenaTournamentState = "swiss"
	ArenaTournamentStateGolden         ArenaTournamentState = "golden"
	ArenaTournamentStatePlayoffs       ArenaTournamentState = "playoffs"
	ArenaTournamentStateTechnicalPause ArenaTournamentState = "technical_pause"
	ArenaTournamentStateCompleted      ArenaTournamentState = "completed"
	ArenaTournamentStateCancelled      ArenaTournamentState = "cancelled"
)

var (
	ErrInvalidArenaTournamentState = errors.New("invalid Arena tournament state")
	ErrArenaTournamentTransition   = errors.New("arena tournament transition is not allowed")
)

var arenaTournamentTransitions = map[ArenaTournamentState]map[ArenaTournamentState]struct{}{
	ArenaTournamentStateDraft: {
		ArenaTournamentStateRegistration: {},
		ArenaTournamentStateCancelled:    {},
	},
	ArenaTournamentStateRegistration: {
		ArenaTournamentStateRosterLocked: {},
		ArenaTournamentStateCancelled:    {},
	},
	ArenaTournamentStateRosterLocked: {
		ArenaTournamentStateRegistration: {},
		ArenaTournamentStateSwiss:        {},
		ArenaTournamentStateCancelled:    {},
	},
	ArenaTournamentStateSwiss: {
		ArenaTournamentStateGolden:         {},
		ArenaTournamentStatePlayoffs:       {},
		ArenaTournamentStateTechnicalPause: {},
		ArenaTournamentStateCancelled:      {},
	},
	ArenaTournamentStateGolden: {
		ArenaTournamentStatePlayoffs:       {},
		ArenaTournamentStateTechnicalPause: {},
		ArenaTournamentStateCancelled:      {},
	},
	ArenaTournamentStatePlayoffs: {
		ArenaTournamentStateTechnicalPause: {},
		ArenaTournamentStateCompleted:      {},
		ArenaTournamentStateCancelled:      {},
	},
	ArenaTournamentStateTechnicalPause: {
		ArenaTournamentStateCancelled: {},
	},
}

type ArenaTournament struct {
	State           ArenaTournamentState
	PausedFromState *ArenaTournamentState
}

func (s ArenaTournamentState) IsValid() bool {
	switch s {
	case ArenaTournamentStateDraft,
		ArenaTournamentStateRegistration,
		ArenaTournamentStateRosterLocked,
		ArenaTournamentStateSwiss,
		ArenaTournamentStateGolden,
		ArenaTournamentStatePlayoffs,
		ArenaTournamentStateTechnicalPause,
		ArenaTournamentStateCompleted,
		ArenaTournamentStateCancelled:
		return true
	}
	return false
}

func (s ArenaTournamentState) IsTerminal() bool {
	return s == ArenaTournamentStateCompleted || s == ArenaTournamentStateCancelled
}

func (s ArenaTournamentState) String() string {
	return string(s)
}

func (t ArenaTournament) Validate() error {
	if !t.State.IsValid() {
		return fmt.Errorf("%w: %q", ErrInvalidArenaTournamentState, t.State)
	}
	if t.State == ArenaTournamentStateTechnicalPause {
		if t.PausedFromState == nil || !isArenaTournamentPauseOrigin(*t.PausedFromState) {
			return fmt.Errorf("%w: technical pause requires a live origin", ErrInvalidArenaTournamentState)
		}
		return nil
	}
	if t.PausedFromState != nil {
		return fmt.Errorf("%w: pause origin outside technical pause", ErrInvalidArenaTournamentState)
	}
	return nil
}

func (t ArenaTournament) CanTransitionTo(next ArenaTournamentState) bool {
	if t.Validate() != nil || !next.IsValid() {
		return false
	}
	if next == t.State {
		return true
	}
	if t.State == ArenaTournamentStateTechnicalPause && t.PausedFromState != nil && next == *t.PausedFromState {
		return true
	}
	_, allowed := arenaTournamentTransitions[t.State][next]
	return allowed
}

func (t *ArenaTournament) TransitionTo(next ArenaTournamentState) (bool, error) {
	if t == nil {
		return false, fmt.Errorf("%w: nil tournament", ErrInvalidArenaTournamentState)
	}
	if err := t.Validate(); err != nil {
		return false, err
	}
	if !next.IsValid() {
		return false, fmt.Errorf("%w: %q", ErrInvalidArenaTournamentState, next)
	}
	if next == t.State {
		return false, nil
	}
	if !t.CanTransitionTo(next) {
		return false, fmt.Errorf("%w: %s -> %s", ErrArenaTournamentTransition, t.State, next)
	}

	if next == ArenaTournamentStateTechnicalPause {
		origin := t.State
		t.PausedFromState = &origin
	} else {
		t.PausedFromState = nil
	}
	t.State = next
	return true, nil
}

func isArenaTournamentPauseOrigin(state ArenaTournamentState) bool {
	switch state {
	case ArenaTournamentStateSwiss, ArenaTournamentStateGolden, ArenaTournamentStatePlayoffs:
		return true
	}
	return false
}
