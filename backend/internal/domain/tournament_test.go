package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestIsValidServerTime(t *testing.T) {
	t.Parallel()

	utc := time.Date(2026, time.September, 3, 12, 0, 0, 0, time.UTC)
	if !domain.IsValidServerTime(utc) {
		t.Fatal("UTC server time is invalid")
	}
	if domain.IsValidServerTime(time.Time{}) {
		t.Fatal("zero server time is valid")
	}
	if domain.IsValidServerTime(utc.In(time.FixedZone("offset", 3*60*60))) {
		t.Fatal("non-UTC server time is valid")
	}
}

func TestTournamentTransitions(t *testing.T) {
	t.Parallel()

	states := []domain.TournamentState{
		domain.TournamentStateDraft,
		domain.TournamentStateRegistration,
		domain.TournamentStateRosterLocked,
		domain.TournamentStateSwiss,
		domain.TournamentStateGolden,
		domain.TournamentStatePlayoffs,
		domain.TournamentStateTechnicalPause,
		domain.TournamentStateCompleted,
		domain.TournamentStateCancelled,
	}
	allowed := map[domain.TournamentState]map[domain.TournamentState]bool{
		domain.TournamentStateDraft: {
			domain.TournamentStateRegistration: true,
			domain.TournamentStateCancelled:    true,
		},
		domain.TournamentStateRegistration: {
			domain.TournamentStateRosterLocked: true,
			domain.TournamentStateCancelled:    true,
		},
		domain.TournamentStateRosterLocked: {
			domain.TournamentStateRegistration: true,
			domain.TournamentStateSwiss:        true,
			domain.TournamentStateCancelled:    true,
		},
		domain.TournamentStateSwiss: {
			domain.TournamentStateGolden:         true,
			domain.TournamentStatePlayoffs:       true,
			domain.TournamentStateTechnicalPause: true,
			domain.TournamentStateCancelled:      true,
		},
		domain.TournamentStateGolden: {
			domain.TournamentStatePlayoffs:       true,
			domain.TournamentStateTechnicalPause: true,
			domain.TournamentStateCancelled:      true,
		},
		domain.TournamentStatePlayoffs: {
			domain.TournamentStateTechnicalPause: true,
			domain.TournamentStateCompleted:      true,
			domain.TournamentStateCancelled:      true,
		},
		domain.TournamentStateTechnicalPause: {
			domain.TournamentStateSwiss:     true,
			domain.TournamentStateCancelled: true,
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
					if !errors.Is(err, domain.ErrTournamentTransition) {
						t.Fatalf("TransitionTo(%s) from %s error = %v, want ErrTournamentTournamentTransition", to, from, err)
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

func TestTournamentTransitionsRetainPauseOrigin(t *testing.T) {
	t.Parallel()

	for _, origin := range []domain.TournamentState{
		domain.TournamentStateSwiss,
		domain.TournamentStateGolden,
		domain.TournamentStatePlayoffs,
	} {
		t.Run(origin.String(), func(t *testing.T) {
			t.Parallel()

			tournament := domain.Tournament{State: origin}
			changed, err := tournament.TransitionTo(domain.TournamentStateTechnicalPause)
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

func TestTournamentTransitionsTerminalStatesAreIdempotent(t *testing.T) {
	t.Parallel()

	for _, terminal := range []domain.TournamentState{
		domain.TournamentStateCompleted,
		domain.TournamentStateCancelled,
	} {
		t.Run(terminal.String(), func(t *testing.T) {
			t.Parallel()

			tournament := domain.Tournament{State: terminal}
			changed, err := tournament.TransitionTo(terminal)
			if err != nil || changed {
				t.Fatalf("terminal retry error = %v, changed = %v", err, changed)
			}

			changed, err = tournament.TransitionTo(domain.TournamentStateDraft)
			if !errors.Is(err, domain.ErrTournamentTransition) || changed {
				t.Fatalf("terminal rewrite error = %v, changed = %v", err, changed)
			}
			if tournament.State != terminal {
				t.Fatalf("terminal rewrite state = %s, want %s", tournament.State, terminal)
			}
		})
	}
}

func TestTournamentTransitionsRejectInvalidState(t *testing.T) {
	t.Parallel()

	tournament := domain.Tournament{State: domain.TournamentState("unknown")}
	if changed, err := tournament.TransitionTo(domain.TournamentStateDraft); !errors.Is(err, domain.ErrInvalidTournamentState) || changed {
		t.Fatalf("invalid current state error = %v, changed = %v", err, changed)
	}

	tournament = domain.Tournament{State: domain.TournamentStateDraft}
	if changed, err := tournament.TransitionTo(domain.TournamentState("unknown")); !errors.Is(err, domain.ErrInvalidTournamentState) || changed {
		t.Fatalf("invalid next state error = %v, changed = %v", err, changed)
	}

	if changed, err := (*domain.Tournament)(nil).TransitionTo(domain.TournamentStateDraft); !errors.Is(err, domain.ErrInvalidTournamentState) || changed {
		t.Fatalf("nil tournament error = %v, changed = %v", err, changed)
	}
}

func tournamentInState(state domain.TournamentState) domain.Tournament {
	tournament := domain.Tournament{State: state}
	if state == domain.TournamentStateTechnicalPause {
		origin := domain.TournamentStateSwiss
		tournament.PausedFromState = &origin
	}
	return tournament
}
