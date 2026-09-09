package cancellation_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentcancellation "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/cancellation"
	tournamentmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/cancellation/mocks"
)

func TestTournamentCancellation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 28, 22, 0, 0, 0, time.UTC)
	t.Run("confirmation and reason are mandatory", func(t *testing.T) {
		t.Parallel()

		id := uuid.MustParse("42000000-0000-0000-0000-000000000030")
		_, repository := newCancellationRepository(
			t,
			cancellationLifecycleTournamentRecord(id, domain.TournamentStateSwiss, 3, now),
			nil,
			cancellationRepositoryCalls{},
		)
		useCase := tournamentcancellation.NewTournamentCancellationUseCase(
			repository,
			cancellationNewFixedTournamentClock(t, now, 0),
		)
		command := tournamentcancellation.TournamentCancellationCommand{
			TournamentID: id, ExpectedRevision: 3, CommandID: uuid.New(), ActorID: uuid.New(),
			Reason: "operator request",
		}
		if _, changed, err := useCase.Cancel(t.Context(), command); !errors.Is(
			err, tournamentcancellation.ErrTournamentCancellationNotConfirmed,
		) || changed {
			t.Fatalf("unconfirmed cancellation error = %v, changed = %v", err, changed)
		}
		command.Confirmed = true
		command.Reason = " "
		if _, changed, err := useCase.Cancel(t.Context(), command); !errors.Is(err, domain.ErrValidation) || changed {
			t.Fatalf("reasonless cancellation error = %v, changed = %v", err, changed)
		}
	})

	cancellableStates := []domain.TournamentState{
		domain.TournamentStateDraft,
		domain.TournamentStateRegistration,
		domain.TournamentStateRosterLocked,
		domain.TournamentStateSwiss,
		domain.TournamentStateGolden,
		domain.TournamentStatePlayoffs,
		domain.TournamentStateTechnicalPause,
	}
	for index, tournamentState := range cancellableStates {
		t.Run(tournamentState.String(), func(t *testing.T) {
			t.Parallel()

			tournamentID := uuid.MustParse(cancellationTournamentIDs[index])
			results := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
			state, repository := newCancellationRepository(
				t,
				cancellationLifecycleTournamentRecord(tournamentID, tournamentState, 7, now),
				results,
				cancellationRepositoryCalls{get: 3, getCancellation: 2, cancel: 1},
			)
			useCase := tournamentcancellation.NewTournamentCancellationUseCase(
				repository,
				cancellationNewFixedTournamentClock(t, now, 1),
			)
			command := tournamentcancellation.TournamentCancellationCommand{
				TournamentID: tournamentID, ExpectedRevision: 7,
				CommandID: uuid.New(), ActorID: uuid.New(), Reason: "operator cancellation", Confirmed: true,
			}
			cancelled, changed, err := useCase.Cancel(t.Context(), command)
			if err != nil || !changed {
				t.Fatalf("Cancel() error = %v, changed = %v", err, changed)
			}
			if cancelled.Tournament.State != domain.TournamentStateCancelled ||
				cancelled.Tournament.FinishedAt == nil || cancelled.ChampionID != nil {
				t.Fatalf("cancellation record = %+v", *cancelled)
			}
			if !slices.Equal(state.committedResultIDs(), results) {
				t.Fatalf("committed results changed: got %v, want %v", state.committedResultIDs(), results)
			}
			if err := state.participantMutation(tournamentID); !errors.Is(err, domain.ErrConflict) {
				t.Fatalf("participant mutation after cancellation error = %v", err)
			}
			if err := state.childStart(tournamentID); !errors.Is(err, domain.ErrConflict) {
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
		_, repository := newCancellationRepository(
			t,
			cancellationLifecycleTournamentRecord(id, domain.TournamentStateCompleted, 8, now),
			nil,
			cancellationRepositoryCalls{get: 1},
		)
		useCase := tournamentcancellation.NewTournamentCancellationUseCase(
			repository,
			cancellationNewFixedTournamentClock(t, now, 0),
		)
		_, changed, err := useCase.Cancel(t.Context(), tournamentcancellation.TournamentCancellationCommand{
			TournamentID: id, ExpectedRevision: 8, CommandID: uuid.New(), ActorID: uuid.New(),
			Reason: "cannot cancel completed", Confirmed: true,
		})
		if !errors.Is(err, domain.ErrTournamentTransition) || changed {
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

type cancellationRepositoryState struct {
	mu             sync.Mutex
	tournament     tournamentcancellation.CancellationTournamentRecord
	cancellations  map[uuid.UUID]tournamentcancellation.TournamentCancellationRecord
	committed      []uuid.UUID
	mutationsBlock bool
	startsBlock    bool
}

type cancellationRepositoryCalls struct {
	get             int
	getCancellation int
	cancel          int
}

func newCancellationRepository(
	t *testing.T,
	tournamentRecord tournamentcancellation.CancellationTournamentRecord,
	committed []uuid.UUID,
	calls cancellationRepositoryCalls,
) (*cancellationRepositoryState, *tournamentmocks.MockTournamentCancellationRepository) {
	t.Helper()
	state := &cancellationRepositoryState{
		tournament: *cancellationCloneTournamentRecord(tournamentRecord), cancellations: make(map[uuid.UUID]tournamentcancellation.TournamentCancellationRecord),
		committed: append([]uuid.UUID(nil), committed...),
	}
	repository := tournamentmocks.NewMockTournamentCancellationRepository(t)
	if calls.get > 0 {
		repository.EXPECT().
			GetTournament(mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, id uuid.UUID) (*tournamentcancellation.CancellationTournamentRecord, error) {
				state.mu.Lock()
				defer state.mu.Unlock()
				if id != state.tournament.ID {
					return nil, tournamentcancellation.CancellationErrTournamentNotFound
				}
				return cancellationCloneTournamentRecord(state.tournament), nil
			}).
			Times(calls.get)
	}
	if calls.getCancellation > 0 {
		repository.EXPECT().
			GetTournamentCancellation(mock.Anything, mock.Anything, mock.Anything).
			RunAndReturn(func(
				_ context.Context,
				tournamentID uuid.UUID,
				commandID uuid.UUID,
			) (*tournamentcancellation.TournamentCancellationRecord, error) {
				state.mu.Lock()
				defer state.mu.Unlock()
				record, ok := state.cancellations[commandID]
				if !ok || tournamentID != state.tournament.ID {
					return nil, tournamentcancellation.CancellationErrTournamentNotFound
				}
				return cloneCancellationTestRecord(record), nil
			}).
			Times(calls.getCancellation)
	}
	if calls.cancel > 0 {
		repository.EXPECT().
			CancelTournament(mock.Anything, mock.Anything).
			RunAndReturn(func(
				_ context.Context,
				in tournamentcancellation.TournamentCancellationInput,
			) (*tournamentcancellation.TournamentCancellationRecord, bool, error) {
				state.mu.Lock()
				defer state.mu.Unlock()
				if existing, ok := state.cancellations[in.CommandID]; ok {
					return cloneCancellationTestRecord(existing), false, nil
				}
				if in.TournamentID != state.tournament.ID ||
					state.tournament.Revision != in.ExpectedRevision ||
					state.tournament.State != in.ExpectedState || state.tournament.State.IsTerminal() {
					return nil, false, domain.ErrConflict
				}
				state.tournament.State = domain.TournamentStateCancelled
				state.tournament.PausedFromState = nil
				state.tournament.Revision++
				state.tournament.UpdatedAt = in.CancelledAt
				state.tournament.FinishedAt = cancellationCloneTestTimePointer(&in.CancelledAt)
				state.mutationsBlock = true
				state.startsBlock = true
				record := tournamentcancellation.TournamentCancellationRecord{
					Tournament: state.tournament, CommandID: in.CommandID,
					ActorID: in.ActorID, Reason: in.Reason,
					AuditEventID: uuid.New(), OutboxEventID: uuid.New(), CancelledAt: in.CancelledAt,
				}
				state.cancellations[in.CommandID] = record
				return cloneCancellationTestRecord(record), true, nil
			}).
			Times(calls.cancel)
	}
	return state, repository
}

func (s *cancellationRepositoryState) participantMutation(tournamentID uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tournamentID != s.tournament.ID || s.mutationsBlock {
		return domain.ErrConflict
	}
	return nil
}

func (s *cancellationRepositoryState) childStart(tournamentID uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tournamentID != s.tournament.ID || s.startsBlock {
		return domain.ErrConflict
	}
	return nil
}

func (s *cancellationRepositoryState) committedResultIDs() []uuid.UUID {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]uuid.UUID(nil), s.committed...)
}

func cloneCancellationTestRecord(record tournamentcancellation.TournamentCancellationRecord) *tournamentcancellation.TournamentCancellationRecord {
	cloned := record
	cloned.Tournament = *cancellationCloneTournamentRecord(record.Tournament)
	return &cloned
}
