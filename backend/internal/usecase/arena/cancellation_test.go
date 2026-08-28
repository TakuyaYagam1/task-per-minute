package arena_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestTournamentCancellation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 28, 22, 0, 0, 0, time.UTC)
	cancellableStates := []domain.ArenaTournamentState{
		domain.ArenaTournamentStateDraft,
		domain.ArenaTournamentStateRegistration,
		domain.ArenaTournamentStateRosterLocked,
		domain.ArenaTournamentStateSwiss,
		domain.ArenaTournamentStateGolden,
		domain.ArenaTournamentStatePlayoffs,
		domain.ArenaTournamentStateTechnicalPause,
	}
	for index, state := range cancellableStates {
		t.Run(state.String(), func(t *testing.T) {
			t.Parallel()

			tournamentID := uuid.MustParse(cancellationTournamentIDs[index])
			results := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
			repo := newCancellationRepositoryFake(
				lifecycleTournamentRecord(tournamentID, state, 7, now),
				results,
			)
			useCase := arena.NewTournamentCancellationUseCase(repo, fixedArenaClock{now: now})
			command := arena.TournamentCancellationCommand{
				TournamentID: tournamentID, ExpectedRevision: 7,
				CommandID: uuid.New(), ActorID: uuid.New(), Reason: "operator cancellation", Confirmed: true,
			}
			cancelled, changed, err := useCase.Cancel(t.Context(), command)
			if err != nil || !changed {
				t.Fatalf("Cancel() error = %v, changed = %v", err, changed)
			}
			if cancelled.Tournament.State != domain.ArenaTournamentStateCancelled ||
				cancelled.Tournament.FinishedAt == nil || cancelled.ChampionID != nil {
				t.Fatalf("cancellation record = %+v", *cancelled)
			}
			if !slices.Equal(repo.committedResultIDs(), results) {
				t.Fatalf("committed results changed: got %v, want %v", repo.committedResultIDs(), results)
			}
			if err := repo.participantMutation(tournamentID); !errors.Is(err, domain.ErrConflict) {
				t.Fatalf("participant mutation after cancellation error = %v", err)
			}
			if err := repo.childStart(tournamentID); !errors.Is(err, domain.ErrConflict) {
				t.Fatalf("child start after cancellation error = %v", err)
			}

			retried, changed, err := useCase.Cancel(t.Context(), command)
			if err != nil || changed || retried == nil || retried.AuditEventID != cancelled.AuditEventID ||
				retried.OutboxEventID != cancelled.OutboxEventID {
				t.Fatalf("Cancel(retry) error = %v, changed = %v, record = %+v", err, changed, retried)
			}
			conflicting := command
			conflicting.CommandID = uuid.New()
			if _, changed, err = useCase.Cancel(t.Context(), conflicting); !errors.Is(err, domain.ErrConflict) || changed {
				t.Fatalf("Cancel(conflicting retry) error = %v, changed = %v", err, changed)
			}
		})
	}

	t.Run("completed rejects cancellation", func(t *testing.T) {
		t.Parallel()

		id := uuid.MustParse("42000000-0000-0000-0000-000000000020")
		repo := newCancellationRepositoryFake(
			lifecycleTournamentRecord(id, domain.ArenaTournamentStateCompleted, 8, now),
			nil,
		)
		useCase := arena.NewTournamentCancellationUseCase(repo, fixedArenaClock{now: now})
		_, changed, err := useCase.Cancel(t.Context(), arena.TournamentCancellationCommand{
			TournamentID: id, ExpectedRevision: 8, CommandID: uuid.New(), ActorID: uuid.New(),
			Reason: "cannot cancel completed", Confirmed: true,
		})
		if !errors.Is(err, domain.ErrArenaTournamentTransition) || changed {
			t.Fatalf("Cancel(completed) error = %v, changed = %v", err, changed)
		}
	})
}

var cancellationTournamentIDs = []string{
	"42000000-0000-0000-0000-000000000001",
	"42000000-0000-0000-0000-000000000002",
	"42000000-0000-0000-0000-000000000003",
	"42000000-0000-0000-0000-000000000004",
	"42000000-0000-0000-0000-000000000005",
	"42000000-0000-0000-0000-000000000006",
	"42000000-0000-0000-0000-000000000007",
}

type cancellationRepositoryFake struct {
	mu             sync.Mutex
	tournament     arena.TournamentRecord
	cancellations  map[uuid.UUID]arena.TournamentCancellationRecord
	committed      []uuid.UUID
	mutationsBlock bool
	startsBlock    bool
}

func newCancellationRepositoryFake(
	tournament arena.TournamentRecord,
	committed []uuid.UUID,
) *cancellationRepositoryFake {
	return &cancellationRepositoryFake{
		tournament: *cloneTournamentRecord(tournament), cancellations: make(map[uuid.UUID]arena.TournamentCancellationRecord),
		committed: append([]uuid.UUID(nil), committed...),
	}
}

func (f *cancellationRepositoryFake) GetTournament(
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

func (f *cancellationRepositoryFake) GetTournamentCancellation(
	_ context.Context,
	tournamentID uuid.UUID,
	commandID uuid.UUID,
) (*arena.TournamentCancellationRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	record, ok := f.cancellations[commandID]
	if !ok || tournamentID != f.tournament.ID {
		return nil, arena.ErrTournamentNotFound
	}
	return cloneCancellationTestRecord(record), nil
}

func (f *cancellationRepositoryFake) CancelTournament(
	_ context.Context,
	in arena.TournamentCancellationInput,
) (*arena.TournamentCancellationRecord, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if existing, ok := f.cancellations[in.CommandID]; ok {
		return cloneCancellationTestRecord(existing), false, nil
	}
	if in.TournamentID != f.tournament.ID || f.tournament.Revision != in.ExpectedRevision ||
		f.tournament.State != in.ExpectedState || f.tournament.State.IsTerminal() {
		return nil, false, domain.ErrConflict
	}
	f.tournament.State = domain.ArenaTournamentStateCancelled
	f.tournament.PausedFromState = nil
	f.tournament.Revision++
	f.tournament.UpdatedAt = in.CancelledAt
	f.tournament.FinishedAt = cloneTestTimePointer(&in.CancelledAt)
	f.mutationsBlock = true
	f.startsBlock = true
	record := arena.TournamentCancellationRecord{
		Tournament: f.tournament, CommandID: in.CommandID, ActorID: in.ActorID, Reason: in.Reason,
		AuditEventID: uuid.New(), OutboxEventID: uuid.New(), CancelledAt: in.CancelledAt,
	}
	f.cancellations[in.CommandID] = record
	return cloneCancellationTestRecord(record), true, nil
}

func (f *cancellationRepositoryFake) participantMutation(tournamentID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if tournamentID != f.tournament.ID || f.mutationsBlock {
		return domain.ErrConflict
	}
	return nil
}

func (f *cancellationRepositoryFake) childStart(tournamentID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if tournamentID != f.tournament.ID || f.startsBlock {
		return domain.ErrConflict
	}
	return nil
}

func (f *cancellationRepositoryFake) committedResultIDs() []uuid.UUID {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uuid.UUID(nil), f.committed...)
}

func cloneCancellationTestRecord(record arena.TournamentCancellationRecord) *arena.TournamentCancellationRecord {
	cloned := record
	cloned.Tournament = *cloneTournamentRecord(record.Tournament)
	return &cloned
}
