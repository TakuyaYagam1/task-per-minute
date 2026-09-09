package swiss_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
	swissmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss/mocks"
	"github.com/stretchr/testify/mock"
)

func TestSwissRoundLock(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 29, 3, 0, 0, 0, time.UTC)

	t.Run("locks the complete exact plan", func(t *testing.T) {
		t.Parallel()

		proof, input := newRoundLockProof(t)
		repository := newRoundLockRepositoryHarness(t, proof)
		useCase := swissusecase.NewRoundLockUseCase(repository.mock, newClock(t, now))

		locked, changed, err := useCase.Lock(t.Context(), swissusecase.RoundLockCommand{Proof: proof})
		if err != nil || !changed {
			t.Fatalf("Lock() error = %v, changed = %v", err, changed)
		}
		if locked.LockedAt == nil || !locked.LockedAt.Equal(now) || locked.Proof.ProofHash != proof.ProofHash {
			t.Fatalf("locked round = %+v", locked)
		}
		if len(locked.Proof.Series) != len(input.Series) || len(locked.Proof.RosterParticipantIDs) != 4 {
			t.Fatalf("locked proof lost derived membership: %+v", locked.Proof)
		}

		reconciled, repeated, err := useCase.Lock(t.Context(), swissusecase.RoundLockCommand{Proof: proof})
		if err != nil || repeated || reconciled.Proof.ProofHash != proof.ProofHash {
			t.Fatalf("Lock(retry) error = %v, changed = %v, record = %+v", err, repeated, reconciled)
		}
	})

	t.Run("accepts zero prior history for the first round", func(t *testing.T) {
		t.Parallel()

		_, input := newRoundLockProof(t)
		input.Revisions.History = 0
		proof, err := swissusecase.NewRoundLockProof(input)
		if err != nil {
			t.Fatalf("NewRoundLockProof(first round) error = %v", err)
		}
		if err := proof.Validate(); err != nil {
			t.Fatalf("first round proof Validate() error = %v", err)
		}
	})

	t.Run("rejects every stale authority revision", func(t *testing.T) {
		t.Parallel()

		authoritative, input := newRoundLockProof(t)
		tests := []struct {
			name   string
			mutate func(*swissusecase.RoundLockRevisions)
		}{
			{name: "round", mutate: func(value *swissusecase.RoundLockRevisions) { value.Round-- }},
			{name: "source projection", mutate: func(value *swissusecase.RoundLockRevisions) { value.SourceProjection-- }},
			{name: "roster", mutate: func(value *swissusecase.RoundLockRevisions) { value.Roster-- }},
			{name: "normal pool", mutate: func(value *swissusecase.RoundLockRevisions) { value.NormalPool-- }},
			{name: "history", mutate: func(value *swissusecase.RoundLockRevisions) { value.History-- }},
			{name: "wave", mutate: func(value *swissusecase.RoundLockRevisions) { value.Wave-- }},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()

				staleInput := cloneRoundLockProofInput(input)
				test.mutate(&staleInput.Revisions)
				stale, err := swissusecase.NewRoundLockProof(staleInput)
				if err != nil {
					t.Fatalf("NewRoundLockProof(stale) error = %v", err)
				}
				repository := newRoundLockRepositoryHarness(t, authoritative)
				useCase := swissusecase.NewRoundLockUseCase(repository.mock, newClock(t, now))
				if _, changed, err := useCase.Lock(
					t.Context(), swissusecase.RoundLockCommand{Proof: stale},
				); !errors.Is(err, domain.ErrConflict) || changed {
					t.Fatalf("Lock(stale %s) error = %v, changed = %v, want conflict", test.name, err, changed)
				}
			})
		}
	})

	t.Run("rejects an edited plan and a started round", func(t *testing.T) {
		t.Parallel()

		authoritative, input := newRoundLockProof(t)
		editedInput := cloneRoundLockProofInput(input)
		editedInput.Series[0].SecondParticipantID = input.Series[1].FirstParticipantID
		editedInput.Series[1].FirstParticipantID = input.Series[0].SecondParticipantID
		edited, err := swissusecase.NewRoundLockProof(editedInput)
		if err != nil {
			t.Fatalf("NewRoundLockProof(edited) error = %v", err)
		}
		repository := newRoundLockRepositoryHarness(t, authoritative)
		useCase := swissusecase.NewRoundLockUseCase(repository.mock, newClock(t, now))
		if _, changed, err := useCase.Lock(
			t.Context(), swissusecase.RoundLockCommand{Proof: edited},
		); !errors.Is(err, domain.ErrConflict) || changed {
			t.Fatalf("Lock(edited) error = %v, changed = %v, want conflict", err, changed)
		}

		startedAt := now.Add(-time.Minute)
		startedRepository := newRoundLockRepositoryHarness(t, authoritative)
		startedRepository.setStartedAt(startedAt)
		startedUseCase := swissusecase.NewRoundLockUseCase(startedRepository.mock, newClock(t, now))
		if _, changed, err := startedUseCase.Lock(
			t.Context(), swissusecase.RoundLockCommand{Proof: authoritative},
		); !errors.Is(err, swissusecase.ErrRoundAlreadyStarted) || changed {
			t.Fatalf("Lock(started) error = %v, changed = %v", err, changed)
		}
	})

	t.Run("serializes concurrent retries", func(t *testing.T) {
		t.Parallel()

		proof, _ := newRoundLockProof(t)
		repository := newRoundLockRepositoryHarness(t, proof)
		useCase := swissusecase.NewRoundLockUseCase(repository.mock, newClock(t, now))
		var changedCount atomic.Int32
		errorsFound := make(chan error, 12)
		var group sync.WaitGroup
		for range 12 {
			group.Add(1)
			go func() {
				defer group.Done()
				_, changed, err := useCase.Lock(t.Context(), swissusecase.RoundLockCommand{Proof: proof})
				if err != nil {
					errorsFound <- err
					return
				}
				if changed {
					changedCount.Add(1)
				}
			}()
		}
		group.Wait()
		close(errorsFound)
		for err := range errorsFound {
			t.Errorf("concurrent Lock() error = %v", err)
		}
		if changedCount.Load() != 1 {
			t.Fatalf("changed count = %d, want 1", changedCount.Load())
		}
	})
}

type roundLockRepositoryHarness struct {
	mu            sync.Mutex
	mock          *swissmocks.MockRoundLockRepository
	authoritative swissusecase.RoundLockProof
	record        *swissusecase.RoundLockRecord
	startedAt     *time.Time
}

func newRoundLockRepositoryHarness(
	t *testing.T,
	proof swissusecase.RoundLockProof,
) *roundLockRepositoryHarness {
	t.Helper()

	harness := &roundLockRepositoryHarness{authoritative: proof}
	harness.mock = swissmocks.NewMockRoundLockRepository(t)
	harness.mock.EXPECT().
		LockSwissRound(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, input swissusecase.RoundLockInput) (*swissusecase.RoundLockRecord, bool, error) {
			return harness.lock(input)
		}).
		Maybe()
	harness.mock.EXPECT().
		GetSwissRoundLock(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, roundID uuid.UUID) (*swissusecase.RoundLockRecord, error) {
			return harness.get(roundID)
		}).
		Maybe()
	return harness
}

func (h *roundLockRepositoryHarness) lock(
	input swissusecase.RoundLockInput,
) (*swissusecase.RoundLockRecord, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.startedAt != nil || input.Proof.ProofHash != h.authoritative.ProofHash {
		return nil, false, nil
	}
	if h.record != nil {
		return cloneRoundLockRecord(h.record), false, nil
	}
	h.record = &swissusecase.RoundLockRecord{Proof: input.Proof, LockedAt: testTimePointer(input.LockedAt)}
	return cloneRoundLockRecord(h.record), true, nil
}

func (h *roundLockRepositoryHarness) get(
	roundID uuid.UUID,
) (*swissusecase.RoundLockRecord, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if roundID != h.authoritative.RoundID {
		return nil, swissusecase.ErrRoundNotFound
	}
	if h.record != nil {
		return cloneRoundLockRecord(h.record), nil
	}
	return &swissusecase.RoundLockRecord{
		Proof: h.authoritative, StartedAt: cloneTestTimePointer(h.startedAt),
	}, nil
}

func (h *roundLockRepositoryHarness) setStartedAt(value time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.startedAt = testTimePointer(value)
}

func cloneRoundLockRecord(record *swissusecase.RoundLockRecord) *swissusecase.RoundLockRecord {
	if record == nil {
		return nil
	}
	cloned := *record
	cloned.LockedAt = cloneTestTimePointer(record.LockedAt)
	cloned.StartedAt = cloneTestTimePointer(record.StartedAt)
	return &cloned
}
