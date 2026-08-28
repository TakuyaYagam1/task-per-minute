package arena_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestTournamentCreateAndList(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, time.August, 28, 18, 0, 0, 0, time.UTC)
	repo := newTournamentCatalogFake()
	useCase := arena.NewTournamentUseCase(repo, fixedArenaClock{now: createdAt})
	command := arena.TournamentCreateCommand{
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
	assertTournamentRecord(t, created, arena.TournamentRecord{
		ID:         command.TournamentID,
		RosterID:   command.RosterID,
		Preset:     domain.ArenaPresetV1,
		State:      domain.ArenaTournamentStateDraft,
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
	if repo.createCalls != 1 {
		t.Fatalf("repository create calls = %d, want 1", repo.createCalls)
	}

	_, changed, err = useCase.CreateTournament(t.Context(), arena.TournamentCreateCommand{
		TournamentID: command.TournamentID,
		RosterID:     uuid.MustParse("10000000-0000-0000-0000-000000000099"),
	})
	if !errors.Is(err, domain.ErrConflict) || changed {
		t.Fatalf("CreateTournament(conflicting retry) error = %v, changed = %v, want conflict", err, changed)
	}

	registrationID := uuid.MustParse("10000000-0000-0000-0000-000000000003")
	repo.records[registrationID] = arena.TournamentRecord{
		ID:         registrationID,
		RosterID:   uuid.MustParse("10000000-0000-0000-0000-000000000004"),
		Preset:     domain.ArenaPresetV1,
		State:      domain.ArenaTournamentStateRegistration,
		Revision:   4,
		RosterSize: 8,
		CreatedAt:  createdAt.Add(time.Minute),
		UpdatedAt:  createdAt.Add(2 * time.Minute),
	}

	listed, err := useCase.ListTournaments(t.Context(), arena.TournamentListFilter{
		States: []domain.ArenaTournamentState{domain.ArenaTournamentStateRegistration},
	})
	if err != nil {
		t.Fatalf("ListTournaments() error = %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("ListTournaments() length = %d, want 1", len(listed))
	}
	assertTournamentRecord(t, &listed[0], repo.records[registrationID])

	progressed := repo.records[command.TournamentID]
	progressed.State = domain.ArenaTournamentStateRegistration
	progressed.Revision++
	progressed.UpdatedAt = createdAt.Add(3 * time.Minute)
	repo.records[command.TournamentID] = progressed
	retried, changed, err = useCase.CreateTournament(t.Context(), command)
	if err != nil || changed {
		t.Fatalf("CreateTournament(progressed retry) error = %v, changed = %v", err, changed)
	}
	assertTournamentRecord(t, retried, progressed)
}

type fixedArenaClock struct {
	now time.Time
}

func (c fixedArenaClock) Now() time.Time {
	return c.now
}

type tournamentCatalogFake struct {
	records     map[uuid.UUID]arena.TournamentRecord
	createCalls int
}

func newTournamentCatalogFake() *tournamentCatalogFake {
	return &tournamentCatalogFake{records: make(map[uuid.UUID]arena.TournamentRecord)}
}

func (f *tournamentCatalogFake) CreateTournamentDraft(
	_ context.Context,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	createdAt time.Time,
) (*arena.TournamentRecord, *arena.RosterRecord, error) {
	f.createCalls++
	if _, exists := f.records[tournamentID]; exists {
		return nil, nil, domain.ErrConflict
	}
	record := arena.TournamentRecord{
		ID: tournamentID, RosterID: rosterID, Preset: domain.ArenaPresetV1,
		State: domain.ArenaTournamentStateDraft, Revision: 1, CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	f.records[tournamentID] = record
	roster := arena.RosterRecord{
		ID: rosterID, TournamentID: tournamentID, Revision: 1, CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	return cloneTournamentRecord(record), &roster, nil
}

func (f *tournamentCatalogFake) GetTournament(_ context.Context, id uuid.UUID) (*arena.TournamentRecord, error) {
	record, exists := f.records[id]
	if !exists {
		return nil, arena.ErrTournamentNotFound
	}
	return cloneTournamentRecord(record), nil
}

func (f *tournamentCatalogFake) ListTournaments(_ context.Context) ([]arena.TournamentRecord, error) {
	out := make([]arena.TournamentRecord, 0, len(f.records))
	for _, record := range f.records {
		out = append(out, *cloneTournamentRecord(record))
	}
	return out, nil
}

func cloneTournamentRecord(record arena.TournamentRecord) *arena.TournamentRecord {
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

func assertTournamentRecord(tb testing.TB, got *arena.TournamentRecord, want arena.TournamentRecord) {
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
