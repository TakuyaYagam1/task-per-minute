package catalog_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	catalogusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/catalog"
	tournamentmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/catalog/mocks"
)

func TestTournamentCreateAndList(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, time.August, 28, 18, 0, 0, 0, time.UTC)
	state, repository := newTournamentCatalogRepository(t)
	useCase := catalogusecase.NewTournamentUseCase(
		repository,
		catalogNewFixedTournamentClock(t, createdAt, 1),
	)
	command := catalogusecase.TournamentCreateCommand{
		TournamentID: uuid.MustParse("10000000-0000-0000-0000-000000000001"),
		RosterID:     uuid.MustParse("10000000-0000-0000-0000-000000000002"),
	}

	created, changed, err := useCase.CreateTournament(t.Context(), command)
	if err != nil {
		t.Fatalf("CreateTournament() error = %v", err)
	}
	if !changed {
		t.Fatal("CreateTournament() changed = false, want true")
	}
	assertTournamentRecord(t, created, catalogusecase.CatalogTournamentRecord{
		ID:         command.TournamentID,
		RosterID:   command.RosterID,
		Preset:     domain.TournamentPresetV1,
		State:      domain.TournamentStateDraft,
		Revision:   1,
		RosterSize: 0,
		CreatedAt:  createdAt,
		UpdatedAt:  createdAt,
	})

	retried, changed, err := useCase.CreateTournament(t.Context(), command)
	if err != nil {
		t.Fatalf("CreateTournament(retry) error = %v", err)
	}
	if changed {
		t.Fatal("CreateTournament(retry) changed = true, want false")
	}
	assertTournamentRecord(t, retried, *created)
	if state.createCalls != 1 {
		t.Fatalf("repository create calls = %d, want 1", state.createCalls)
	}

	_, changed, err = useCase.CreateTournament(t.Context(), catalogusecase.TournamentCreateCommand{
		TournamentID: command.TournamentID,
		RosterID:     uuid.MustParse("10000000-0000-0000-0000-000000000099"),
	})
	if !errors.Is(err, domain.ErrConflict) || changed {
		t.Fatalf("CreateTournament(conflicting retry) error = %v, changed = %v, want conflict", err, changed)
	}

	registrationID := uuid.MustParse("10000000-0000-0000-0000-000000000003")
	state.records[registrationID] = catalogusecase.CatalogTournamentRecord{
		ID:         registrationID,
		RosterID:   uuid.MustParse("10000000-0000-0000-0000-000000000004"),
		Preset:     domain.TournamentPresetV1,
		State:      domain.TournamentStateRegistration,
		Revision:   4,
		RosterSize: 8,
		CreatedAt:  createdAt.Add(time.Minute),
		UpdatedAt:  createdAt.Add(2 * time.Minute),
	}

	listed, err := useCase.ListTournaments(t.Context(), catalogusecase.TournamentListFilter{
		States: []domain.TournamentState{domain.TournamentStateRegistration},
	})
	if err != nil {
		t.Fatalf("ListTournaments() error = %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("ListTournaments() length = %d, want 1", len(listed))
	}
	assertTournamentRecord(t, &listed[0], state.records[registrationID])

	progressed := state.records[command.TournamentID]
	progressed.State = domain.TournamentStateRegistration
	progressed.Revision++
	progressed.UpdatedAt = createdAt.Add(3 * time.Minute)
	state.records[command.TournamentID] = progressed
	retried, changed, err = useCase.CreateTournament(t.Context(), command)
	if err != nil || changed {
		t.Fatalf("CreateTournament(progressed retry) error = %v, changed = %v", err, changed)
	}
	assertTournamentRecord(t, retried, progressed)
}

type tournamentCatalogState struct {
	records     map[uuid.UUID]catalogusecase.CatalogTournamentRecord
	createCalls int
}

func newTournamentCatalogRepository(
	t *testing.T,
) (*tournamentCatalogState, *tournamentmocks.MockTournamentRepository) {
	t.Helper()
	state := &tournamentCatalogState{records: make(map[uuid.UUID]catalogusecase.CatalogTournamentRecord)}
	repository := tournamentmocks.NewMockTournamentRepository(t)
	repository.EXPECT().
		GetTournament(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, id uuid.UUID) (*catalogusecase.CatalogTournamentRecord, error) {
			record, exists := state.records[id]
			if !exists {
				return nil, catalogusecase.ErrTournamentNotFound
			}
			return catalogCloneTournamentRecord(record), nil
		}).
		Times(4)
	repository.EXPECT().
		CreateTournamentDraft(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context,
			tournamentID uuid.UUID,
			rosterID uuid.UUID,
			createdAt time.Time,
		) (*catalogusecase.CatalogTournamentRecord, *catalogusecase.CatalogRosterRecord, error) {
			state.createCalls++
			if _, exists := state.records[tournamentID]; exists {
				return nil, nil, domain.ErrConflict
			}
			record := catalogusecase.CatalogTournamentRecord{
				ID: tournamentID, RosterID: rosterID, Preset: domain.TournamentPresetV1,
				State: domain.TournamentStateDraft, Revision: 1,
				CreatedAt: createdAt, UpdatedAt: createdAt,
			}
			state.records[tournamentID] = record
			roster := catalogusecase.CatalogRosterRecord{
				ID: rosterID, TournamentID: tournamentID,
			}
			return catalogCloneTournamentRecord(record), &roster, nil
		}).
		Once()
	repository.EXPECT().
		ListTournaments(mock.Anything).
		RunAndReturn(func(context.Context) ([]catalogusecase.CatalogTournamentRecord, error) {
			out := make([]catalogusecase.CatalogTournamentRecord, 0, len(state.records))
			for _, record := range state.records {
				out = append(out, *catalogCloneTournamentRecord(record))
			}
			return out, nil
		}).
		Once()
	return state, repository
}

func catalogCloneTournamentRecord(record catalogusecase.CatalogTournamentRecord) *catalogusecase.CatalogTournamentRecord {
	cloned := record
	if record.PausedFromState != nil {
		state := *record.PausedFromState
		cloned.PausedFromState = &state
	}
	if record.StartedAt != nil {
		value := *record.StartedAt
		cloned.StartedAt = &value
	}
	if record.FinishedAt != nil {
		value := *record.FinishedAt
		cloned.FinishedAt = &value
	}
	return &cloned
}

func assertTournamentRecord(tb testing.TB, got *catalogusecase.CatalogTournamentRecord, want catalogusecase.CatalogTournamentRecord) {
	tb.Helper()
	if got == nil {
		tb.Fatal("tournament record is nil")
		return
	}
	if got.ID != want.ID || got.RosterID != want.RosterID || got.Preset != want.Preset ||
		got.State != want.State || got.Revision != want.Revision || got.RosterSize != want.RosterSize ||
		!got.CreatedAt.Equal(want.CreatedAt) || !got.UpdatedAt.Equal(want.UpdatedAt) {
		tb.Fatalf("tournament record = %+v, want %+v", *got, want)
	}
}
