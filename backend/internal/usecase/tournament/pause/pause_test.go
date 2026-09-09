package pause_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentpause "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/pause"
	tournamentmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/pause/mocks"
)

func TestTournamentTechnicalPause(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 28, 23, 0, 0, 0, time.UTC)
	t.Run("confirmation and reason are mandatory", func(t *testing.T) {
		t.Parallel()

		id := uuid.MustParse("43000000-0000-0000-0000-000000000001")
		_, pauseRepository := newPauseRepository(
			t,
			pauseLifecycleTournamentRecord(id, domain.TournamentStateSwiss, 3, now),
			completePauseAdmission(id),
			pauseRepositoryCalls{},
		)
		pauseUseCase := tournamentpause.NewTournamentPauseUseCase(
			newDirectTournamentTransactionManager(t, 0),
			pauseRepository,
			pauseNewFixedTournamentClock(t, now, 0),
		)
		command := technicalPauseCommand(id, 3)
		command.Confirmed = false
		if _, changed, err := pauseUseCase.EnterTechnicalPause(t.Context(), command); !errors.Is(
			err, tournamentpause.ErrTournamentPauseNotConfirmed,
		) || changed {
			t.Fatalf("unconfirmed pause error = %v, changed = %v", err, changed)
		}
		command.Confirmed = true
		command.Reason = " "
		if _, changed, err := pauseUseCase.EnterTechnicalPause(t.Context(), command); !errors.Is(
			err, domain.ErrValidation,
		) || changed {
			t.Fatalf("reasonless pause error = %v, changed = %v", err, changed)
		}
	})

	t.Run("complete graph admits pause and retry", func(t *testing.T) {
		t.Parallel()

		for _, origin := range []domain.TournamentState{
			domain.TournamentStateSwiss,
			domain.TournamentStateGolden,
			domain.TournamentStatePlayoffs,
		} {
			t.Run(origin.String(), func(t *testing.T) {
				t.Parallel()

				id := uuid.New()
				_, repository := newPauseRepository(
					t,
					pauseLifecycleTournamentRecord(id, origin, 5, now),
					completePauseAdmission(id),
					pauseRepositoryCalls{get: 2, getPause: 1, inspect: 1, enter: 1},
				)
				useCase := tournamentpause.NewTournamentPauseUseCase(
					newDirectTournamentTransactionManager(t, 1),
					repository,
					pauseNewFixedTournamentClock(t, now, 1),
				)
				command := technicalPauseCommand(id, 5)
				paused, changed, err := useCase.EnterTechnicalPause(t.Context(), command)
				if err != nil || !changed {
					t.Fatalf("EnterTechnicalPause() error = %v, changed = %v", err, changed)
				}
				if paused.Tournament.State != domain.TournamentStateTechnicalPause ||
					paused.Tournament.PausedFromState == nil || *paused.Tournament.PausedFromState != origin {
					t.Fatalf("pause record = %+v", *paused)
				}
				retried, changed, err := useCase.EnterTechnicalPause(t.Context(), command)
				if err != nil || changed || retried == nil || retried.PauseID != paused.PauseID {
					t.Fatalf("pause retry error = %v, changed = %v, record = %+v", err, changed, retried)
				}
			})
		}
	})

	t.Run("active Golden rejects tournament pause", func(t *testing.T) {
		t.Parallel()

		id := uuid.MustParse("43000000-0000-0000-0000-000000000003")
		state, repository := newPauseRepository(
			t,
			pauseLifecycleTournamentRecord(id, domain.TournamentStateGolden, 5, now),
			tournamentpause.TournamentPauseAdmission{
				TournamentID: id, GraphRevision: 1, ExpectedChildren: 4, ObservedChildren: 4,
				ActiveGolden: true, Complete: true,
			},
			pauseRepositoryCalls{get: 1, inspect: 1},
		)
		useCase := tournamentpause.NewTournamentPauseUseCase(
			newDirectTournamentTransactionManager(t, 1),
			repository,
			pauseNewFixedTournamentClock(t, now, 1),
		)
		if _, changed, err := useCase.EnterTechnicalPause(
			t.Context(), technicalPauseCommand(id, 5),
		); !errors.Is(err, tournamentpause.ErrTournamentGoldenActive) || changed || state.pauseCount() != 0 {
			t.Fatalf("Golden pause error = %v, changed = %v, writes = %d", err, changed, state.pauseCount())
		}
	})

	t.Run("active Golden child and partial graph fail closed", func(t *testing.T) {
		t.Parallel()

		for _, tc := range []struct {
			name      string
			admission tournamentpause.TournamentPauseAdmission
			want      error
		}{
			{
				name: "Golden child",
				admission: tournamentpause.TournamentPauseAdmission{
					GraphRevision: 2, ExpectedChildren: 4, ObservedChildren: 4, ActiveGolden: true, Complete: true,
				},
				want: tournamentpause.ErrTournamentGoldenActive,
			},
			{
				name: "partial graph",
				admission: tournamentpause.TournamentPauseAdmission{
					GraphRevision: 2, ExpectedChildren: 4, ObservedChildren: 3, Complete: false,
				},
				want: tournamentpause.ErrTournamentPauseGraphPartial,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				id := uuid.New()
				tc.admission.TournamentID = id
				state, repository := newPauseRepository(
					t,
					pauseLifecycleTournamentRecord(id, domain.TournamentStatePlayoffs, 9, now),
					tc.admission,
					pauseRepositoryCalls{get: 1, inspect: 1},
				)
				useCase := tournamentpause.NewTournamentPauseUseCase(
					newDirectTournamentTransactionManager(t, 1),
					repository,
					pauseNewFixedTournamentClock(t, now, 1),
				)
				if _, changed, err := useCase.EnterTechnicalPause(
					t.Context(), technicalPauseCommand(id, 9),
				); !errors.Is(err, tc.want) || changed || state.pauseCount() != 0 {
					t.Fatalf("pause error = %v, changed = %v, writes = %d", err, changed, state.pauseCount())
				}
			})
		}
	})
}

type pauseRepositoryState struct {
	mu         sync.Mutex
	tournament tournamentpause.PauseTournamentRecord
	admission  tournamentpause.TournamentPauseAdmission
	pauses     map[uuid.UUID]tournamentpause.TournamentTechnicalPauseRecord
}

type pauseRepositoryCalls struct {
	get      int
	getPause int
	inspect  int
	enter    int
}

func newPauseRepository(
	t *testing.T,
	tournamentRecord tournamentpause.PauseTournamentRecord,
	admission tournamentpause.TournamentPauseAdmission,
	calls pauseRepositoryCalls,
) (*pauseRepositoryState, *tournamentmocks.MockTournamentPauseRepository) {
	t.Helper()
	state := &pauseRepositoryState{
		tournament: *pauseCloneTournamentRecord(tournamentRecord), admission: admission,
		pauses: make(map[uuid.UUID]tournamentpause.TournamentTechnicalPauseRecord),
	}
	repository := tournamentmocks.NewMockTournamentPauseRepository(t)
	if calls.get > 0 {
		repository.EXPECT().
			GetTournament(mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, id uuid.UUID) (*tournamentpause.PauseTournamentRecord, error) {
				state.mu.Lock()
				defer state.mu.Unlock()
				if id != state.tournament.ID {
					return nil, tournamentpause.PauseErrTournamentNotFound
				}
				return pauseCloneTournamentRecord(state.tournament), nil
			}).
			Times(calls.get)
	}
	if calls.getPause > 0 {
		repository.EXPECT().
			GetTournamentTechnicalPause(mock.Anything, mock.Anything, mock.Anything).
			RunAndReturn(func(
				_ context.Context,
				tournamentID uuid.UUID,
				commandID uuid.UUID,
			) (*tournamentpause.TournamentTechnicalPauseRecord, error) {
				state.mu.Lock()
				defer state.mu.Unlock()
				record, ok := state.pauses[commandID]
				if !ok || tournamentID != state.tournament.ID {
					return nil, tournamentpause.PauseErrTournamentNotFound
				}
				return clonePauseTestRecord(record), nil
			}).
			Times(calls.getPause)
	}
	if calls.inspect > 0 {
		repository.EXPECT().
			InspectTournamentPauseAdmission(mock.Anything, mock.Anything).
			RunAndReturn(func(
				_ context.Context,
				tournamentID uuid.UUID,
			) (*tournamentpause.TournamentPauseAdmission, error) {
				state.mu.Lock()
				defer state.mu.Unlock()
				if tournamentID != state.tournament.ID {
					return nil, tournamentpause.PauseErrTournamentNotFound
				}
				admission := state.admission
				return &admission, nil
			}).
			Times(calls.inspect)
	}
	if calls.enter > 0 {
		repository.EXPECT().
			EnterTournamentTechnicalPause(mock.Anything, mock.Anything).
			RunAndReturn(func(
				_ context.Context,
				in tournamentpause.TournamentTechnicalPauseInput,
			) (*tournamentpause.TournamentTechnicalPauseRecord, bool, error) {
				state.mu.Lock()
				defer state.mu.Unlock()
				if existing, ok := state.pauses[in.CommandID]; ok {
					return clonePauseTestRecord(existing), false, nil
				}
				if in.TournamentID != state.tournament.ID ||
					in.GraphRevision != state.admission.GraphRevision || !state.admission.Complete ||
					state.admission.ActiveGolden ||
					state.admission.ExpectedChildren != state.admission.ObservedChildren {
					return nil, false, domain.ErrConflict
				}
				if state.tournament.Revision != in.ExpectedRevision ||
					state.tournament.State != in.ExpectedState {
					return nil, false, domain.ErrConflict
				}
				origin := state.tournament.State
				state.tournament.State = domain.TournamentStateTechnicalPause
				state.tournament.PausedFromState = &origin
				state.tournament.Revision++
				state.tournament.UpdatedAt = in.PausedAt
				record := tournamentpause.TournamentTechnicalPauseRecord{
					Tournament: state.tournament, CommandID: in.CommandID, PauseID: in.PauseID,
					ActorID: in.ActorID, Reason: in.Reason, PausedAt: in.PausedAt,
				}
				state.pauses[in.CommandID] = record
				return clonePauseTestRecord(record), true, nil
			}).
			Times(calls.enter)
	}
	return state, repository
}

func (s *pauseRepositoryState) pauseCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pauses)
}

func completePauseAdmission(tournamentID uuid.UUID) tournamentpause.TournamentPauseAdmission {
	return tournamentpause.TournamentPauseAdmission{
		TournamentID: tournamentID, GraphRevision: 1, ExpectedChildren: 4, ObservedChildren: 4, Complete: true,
	}
}

func technicalPauseCommand(tournamentID uuid.UUID, revision int64) tournamentpause.TournamentTechnicalPauseCommand {
	return tournamentpause.TournamentTechnicalPauseCommand{
		TournamentID: tournamentID, ExpectedRevision: revision, CommandID: uuid.New(), PauseID: uuid.New(),
		ActorID: uuid.New(), Reason: "platform maintenance", Confirmed: true,
	}
}

func clonePauseTestRecord(record tournamentpause.TournamentTechnicalPauseRecord) *tournamentpause.TournamentTechnicalPauseRecord {
	cloned := record
	cloned.Tournament = *pauseCloneTournamentRecord(record.Tournament)
	return &cloned
}
