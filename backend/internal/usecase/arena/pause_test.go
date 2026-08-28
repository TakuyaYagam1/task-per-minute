package arena_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestCancelAndTechnicalPause(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 28, 23, 0, 0, 0, time.UTC)
	t.Run("confirmation and reason are mandatory", func(t *testing.T) {
		t.Parallel()

		id := uuid.MustParse("43000000-0000-0000-0000-000000000001")
		cancelRepo := newCancellationRepositoryFake(
			lifecycleTournamentRecord(id, domain.ArenaTournamentStateSwiss, 3, now),
			nil,
		)
		cancelUseCase := arena.NewTournamentCancellationUseCase(cancelRepo, fixedArenaClock{now: now})
		if _, changed, err := cancelUseCase.Cancel(t.Context(), arena.TournamentCancellationCommand{
			TournamentID: id, ExpectedRevision: 3, CommandID: uuid.New(), ActorID: uuid.New(),
			Reason: "operator request",
		}); !errors.Is(err, arena.ErrTournamentCancellationNotConfirmed) || changed {
			t.Fatalf("unconfirmed cancellation error = %v, changed = %v", err, changed)
		}

		pauseRepo := newPauseRepositoryFake(
			lifecycleTournamentRecord(id, domain.ArenaTournamentStateSwiss, 3, now),
			completePauseAdmission(id),
		)
		pauseUseCase := arena.NewTournamentPauseUseCase(
			directArenaTransactionManager{}, pauseRepo, fixedArenaClock{now: now},
		)
		command := technicalPauseCommand(id, 3)
		command.Confirmed = false
		if _, changed, err := pauseUseCase.EnterTechnicalPause(t.Context(), command); !errors.Is(
			err, arena.ErrTournamentPauseNotConfirmed,
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

		for _, origin := range []domain.ArenaTournamentState{
			domain.ArenaTournamentStateSwiss,
			domain.ArenaTournamentStateGolden,
			domain.ArenaTournamentStatePlayoffs,
		} {
			t.Run(origin.String(), func(t *testing.T) {
				t.Parallel()

				id := uuid.New()
				repo := newPauseRepositoryFake(
					lifecycleTournamentRecord(id, origin, 5, now),
					completePauseAdmission(id),
				)
				useCase := arena.NewTournamentPauseUseCase(
					directArenaTransactionManager{}, repo, fixedArenaClock{now: now},
				)
				command := technicalPauseCommand(id, 5)
				paused, changed, err := useCase.EnterTechnicalPause(t.Context(), command)
				if err != nil || !changed {
					t.Fatalf("EnterTechnicalPause() error = %v, changed = %v", err, changed)
				}
				if paused.Tournament.State != domain.ArenaTournamentStateTechnicalPause ||
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
		repo := newPauseRepositoryFake(
			lifecycleTournamentRecord(id, domain.ArenaTournamentStateGolden, 5, now),
			arena.TournamentPauseAdmission{
				TournamentID: id, GraphRevision: 1, ExpectedChildren: 4, ObservedChildren: 4,
				ActiveGolden: true, Complete: true,
			},
		)
		useCase := arena.NewTournamentPauseUseCase(
			directArenaTransactionManager{}, repo, fixedArenaClock{now: now},
		)
		if _, changed, err := useCase.EnterTechnicalPause(
			t.Context(), technicalPauseCommand(id, 5),
		); !errors.Is(err, arena.ErrTournamentGoldenActive) || changed || repo.pauseCount() != 0 {
			t.Fatalf("Golden pause error = %v, changed = %v, writes = %d", err, changed, repo.pauseCount())
		}
	})

	t.Run("active Golden child and partial graph fail closed", func(t *testing.T) {
		t.Parallel()

		for _, tc := range []struct {
			name      string
			admission arena.TournamentPauseAdmission
			want      error
		}{
			{
				name: "Golden child",
				admission: arena.TournamentPauseAdmission{
					GraphRevision: 2, ExpectedChildren: 4, ObservedChildren: 4, ActiveGolden: true, Complete: true,
				},
				want: arena.ErrTournamentGoldenActive,
			},
			{
				name: "partial graph",
				admission: arena.TournamentPauseAdmission{
					GraphRevision: 2, ExpectedChildren: 4, ObservedChildren: 3, Complete: false,
				},
				want: arena.ErrTournamentPauseGraphPartial,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				id := uuid.New()
				tc.admission.TournamentID = id
				repo := newPauseRepositoryFake(
					lifecycleTournamentRecord(id, domain.ArenaTournamentStatePlayoffs, 9, now),
					tc.admission,
				)
				useCase := arena.NewTournamentPauseUseCase(
					directArenaTransactionManager{}, repo, fixedArenaClock{now: now},
				)
				if _, changed, err := useCase.EnterTechnicalPause(
					t.Context(), technicalPauseCommand(id, 9),
				); !errors.Is(err, tc.want) || changed || repo.pauseCount() != 0 {
					t.Fatalf("pause error = %v, changed = %v, writes = %d", err, changed, repo.pauseCount())
				}
			})
		}
	})
}

type pauseRepositoryFake struct {
	mu         sync.Mutex
	tournament arena.TournamentRecord
	admission  arena.TournamentPauseAdmission
	pauses     map[uuid.UUID]arena.TournamentTechnicalPauseRecord
}

func newPauseRepositoryFake(
	tournament arena.TournamentRecord,
	admission arena.TournamentPauseAdmission,
) *pauseRepositoryFake {
	return &pauseRepositoryFake{
		tournament: *cloneTournamentRecord(tournament), admission: admission,
		pauses: make(map[uuid.UUID]arena.TournamentTechnicalPauseRecord),
	}
}

func (f *pauseRepositoryFake) GetTournament(
	_ context.Context,
	id uuid.UUID,
) (*arena.TournamentRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id != f.tournament.ID {
		return nil, arena.ErrTournamentNotFound
	}
	return cloneTournamentRecord(f.tournament), nil
}

func (f *pauseRepositoryFake) GetTournamentTechnicalPause(
	_ context.Context,
	tournamentID uuid.UUID,
	commandID uuid.UUID,
) (*arena.TournamentTechnicalPauseRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	record, ok := f.pauses[commandID]
	if !ok || tournamentID != f.tournament.ID {
		return nil, arena.ErrTournamentNotFound
	}
	return clonePauseTestRecord(record), nil
}

func (f *pauseRepositoryFake) InspectTournamentPauseAdmission(
	_ context.Context,
	tournamentID uuid.UUID,
) (*arena.TournamentPauseAdmission, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if tournamentID != f.tournament.ID {
		return nil, arena.ErrTournamentNotFound
	}
	admission := f.admission
	return &admission, nil
}

func (f *pauseRepositoryFake) EnterTournamentTechnicalPause(
	_ context.Context,
	in arena.TournamentTechnicalPauseInput,
) (*arena.TournamentTechnicalPauseRecord, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if existing, ok := f.pauses[in.CommandID]; ok {
		return clonePauseTestRecord(existing), false, nil
	}
	if in.TournamentID != f.tournament.ID || in.GraphRevision != f.admission.GraphRevision ||
		!f.admission.Complete || f.admission.ActiveGolden ||
		f.admission.ExpectedChildren != f.admission.ObservedChildren {
		return nil, false, domain.ErrConflict
	}
	if f.tournament.Revision != in.ExpectedRevision || f.tournament.State != in.ExpectedState {
		return nil, false, domain.ErrConflict
	}
	origin := f.tournament.State
	f.tournament.State = domain.ArenaTournamentStateTechnicalPause
	f.tournament.PausedFromState = &origin
	f.tournament.Revision++
	f.tournament.UpdatedAt = in.PausedAt
	record := arena.TournamentTechnicalPauseRecord{
		Tournament: f.tournament, CommandID: in.CommandID, PauseID: in.PauseID,
		ActorID: in.ActorID, Reason: in.Reason, PausedAt: in.PausedAt,
	}
	f.pauses[in.CommandID] = record
	return clonePauseTestRecord(record), true, nil
}

func (f *pauseRepositoryFake) pauseCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.pauses)
}

func completePauseAdmission(tournamentID uuid.UUID) arena.TournamentPauseAdmission {
	return arena.TournamentPauseAdmission{
		TournamentID: tournamentID, GraphRevision: 1, ExpectedChildren: 4, ObservedChildren: 4, Complete: true,
	}
}

func technicalPauseCommand(tournamentID uuid.UUID, revision int64) arena.TournamentTechnicalPauseCommand {
	return arena.TournamentTechnicalPauseCommand{
		TournamentID: tournamentID, ExpectedRevision: revision, CommandID: uuid.New(), PauseID: uuid.New(),
		ActorID: uuid.New(), Reason: "platform maintenance", Confirmed: true,
	}
}

func clonePauseTestRecord(record arena.TournamentTechnicalPauseRecord) *arena.TournamentTechnicalPauseRecord {
	cloned := record
	cloned.Tournament = *cloneTournamentRecord(record.Tournament)
	return &cloned
}

type directArenaTransactionManager struct{}

func (directArenaTransactionManager) Do(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}
