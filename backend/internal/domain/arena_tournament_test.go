package domain_test

import (
	"errors"
	"testing"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestArenaTournamentTransitions(t *testing.T) {
	t.Parallel()

	states := []domain.ArenaTournamentState{
		domain.ArenaTournamentStateDraft,
		domain.ArenaTournamentStateRegistration,
		domain.ArenaTournamentStateRosterLocked,
		domain.ArenaTournamentStateSwiss,
		domain.ArenaTournamentStateGolden,
		domain.ArenaTournamentStatePlayoffs,
		domain.ArenaTournamentStateTechnicalPause,
		domain.ArenaTournamentStateCompleted,
		domain.ArenaTournamentStateCancelled,
	}
	allowed := map[domain.ArenaTournamentState]map[domain.ArenaTournamentState]bool{
		domain.ArenaTournamentStateDraft: {
			domain.ArenaTournamentStateRegistration: true,
			domain.ArenaTournamentStateCancelled:    true,
		},
		domain.ArenaTournamentStateRegistration: {
			domain.ArenaTournamentStateRosterLocked: true,
			domain.ArenaTournamentStateCancelled:    true,
		},
		domain.ArenaTournamentStateRosterLocked: {
			domain.ArenaTournamentStateRegistration: true,
			domain.ArenaTournamentStateSwiss:        true,
			domain.ArenaTournamentStateCancelled:    true,
		},
		domain.ArenaTournamentStateSwiss: {
			domain.ArenaTournamentStateGolden:         true,
			domain.ArenaTournamentStatePlayoffs:       true,
			domain.ArenaTournamentStateTechnicalPause: true,
			domain.ArenaTournamentStateCancelled:      true,
		},
		domain.ArenaTournamentStateGolden: {
			domain.ArenaTournamentStatePlayoffs:       true,
			domain.ArenaTournamentStateTechnicalPause: true,
			domain.ArenaTournamentStateCancelled:      true,
		},
		domain.ArenaTournamentStatePlayoffs: {
			domain.ArenaTournamentStateTechnicalPause: true,
			domain.ArenaTournamentStateCompleted:      true,
			domain.ArenaTournamentStateCancelled:      true,
		},
		domain.ArenaTournamentStateTechnicalPause: {
			domain.ArenaTournamentStateSwiss:     true,
			domain.ArenaTournamentStateCancelled: true,
		},
	}

	for _, from := range states {
		for _, to := range states {
			t.Run(from.String()+"_to_"+to.String(), func(t *testing.T) {
				t.Parallel()

				tournament := tournamentInState(from)
				wantAllowed := from == to || allowed[from][to]
				if got := tournament.CanTransitionTo(to); got != wantAllowed {
					t.Fatalf("CanTransitionTo(%s) from %s = %v, want %v", to, from, got, wantAllowed)
				}

				changed, err := tournament.TransitionTo(to)
				if !wantAllowed {
					if !errors.Is(err, domain.ErrArenaTournamentTransition) {
						t.Fatalf("TransitionTo(%s) from %s error = %v, want ErrArenaTournamentTransition", to, from, err)
					}
					if changed {
						t.Fatal("rejected transition changed tournament")
					}
					return
				}
				if err != nil {
					t.Fatalf("TransitionTo(%s) from %s error = %v", to, from, err)
				}
				if changed != (from != to) {
					t.Errorf("TransitionTo(%s) from %s changed = %v, want %v", to, from, changed, from != to)
				}
			})
		}
	}
}

func TestArenaTournamentTransitionsRetainPauseOrigin(t *testing.T) {
	t.Parallel()

	for _, origin := range []domain.ArenaTournamentState{
		domain.ArenaTournamentStateSwiss,
		domain.ArenaTournamentStateGolden,
		domain.ArenaTournamentStatePlayoffs,
	} {
		t.Run(origin.String(), func(t *testing.T) {
			t.Parallel()

			tournament := domain.ArenaTournament{State: origin}
			changed, err := tournament.TransitionTo(domain.ArenaTournamentStateTechnicalPause)
			if err != nil {
				t.Fatalf("pause from %s: %v", origin, err)
			}
			if !changed {
				t.Fatal("pause transition changed = false, want true")
			}
			if tournament.PausedFromState == nil || *tournament.PausedFromState != origin {
				t.Fatalf("PausedFromState = %v, want %s", tournament.PausedFromState, origin)
			}

			changed, err = tournament.TransitionTo(origin)
			if err != nil {
				t.Fatalf("resume to %s: %v", origin, err)
			}
			if !changed || tournament.State != origin || tournament.PausedFromState != nil {
				t.Fatalf("resume result = %+v, changed %v", tournament, changed)
			}
		})
	}
}

func TestArenaTournamentTransitionsTerminalStatesAreIdempotent(t *testing.T) {
	t.Parallel()

	for _, terminal := range []domain.ArenaTournamentState{
		domain.ArenaTournamentStateCompleted,
		domain.ArenaTournamentStateCancelled,
	} {
		t.Run(terminal.String(), func(t *testing.T) {
			t.Parallel()

			tournament := domain.ArenaTournament{State: terminal}
			changed, err := tournament.TransitionTo(terminal)
			if err != nil || changed {
				t.Fatalf("terminal retry error = %v, changed = %v", err, changed)
			}

			changed, err = tournament.TransitionTo(domain.ArenaTournamentStateDraft)
			if !errors.Is(err, domain.ErrArenaTournamentTransition) || changed {
				t.Fatalf("terminal rewrite error = %v, changed = %v", err, changed)
			}
			if tournament.State != terminal {
				t.Fatalf("terminal rewrite state = %s, want %s", tournament.State, terminal)
			}
		})
	}
}

func TestArenaTournamentTransitionsRejectInvalidState(t *testing.T) {
	t.Parallel()

	tournament := domain.ArenaTournament{State: domain.ArenaTournamentState("unknown")}
	if changed, err := tournament.TransitionTo(domain.ArenaTournamentStateDraft); !errors.Is(err, domain.ErrInvalidArenaTournamentState) || changed {
		t.Fatalf("invalid current state error = %v, changed = %v", err, changed)
	}

	tournament = domain.ArenaTournament{State: domain.ArenaTournamentStateDraft}
	if changed, err := tournament.TransitionTo(domain.ArenaTournamentState("unknown")); !errors.Is(err, domain.ErrInvalidArenaTournamentState) || changed {
		t.Fatalf("invalid next state error = %v, changed = %v", err, changed)
	}

	if changed, err := (*domain.ArenaTournament)(nil).TransitionTo(domain.ArenaTournamentStateDraft); !errors.Is(err, domain.ErrInvalidArenaTournamentState) || changed {
		t.Fatalf("nil tournament error = %v, changed = %v", err, changed)
	}
}

func tournamentInState(state domain.ArenaTournamentState) domain.ArenaTournament {
	tournament := domain.ArenaTournament{State: state}
	if state == domain.ArenaTournamentStateTechnicalPause {
		origin := domain.ArenaTournamentStateSwiss
		tournament.PausedFromState = &origin
	}
	return tournament
}
