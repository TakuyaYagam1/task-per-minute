package arena_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestSwissRoundLock(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 29, 3, 0, 0, 0, time.UTC)

	t.Run("locks the complete exact plan", func(t *testing.T) {
		t.Parallel()

		proof, input := newSwissRoundLockProof(t, 1, 4)
		repository := newSwissRoundLockRepositoryFake(proof)
		useCase := arena.NewSwissRoundLockUseCase(repository, fixedArenaClock{now: now})

		locked, changed, err := useCase.Lock(t.Context(), arena.SwissRoundLockCommand{Proof: proof})
		if err != nil || !changed {
			t.Fatalf("Lock() error = %v, changed = %v", err, changed)
		}
		if locked.LockedAt == nil || !locked.LockedAt.Equal(now) || locked.Proof.ProofHash != proof.ProofHash {
			t.Fatalf("locked round = %+v", locked)
		}
		if len(locked.Proof.Series) != len(input.Series) || len(locked.Proof.RosterParticipantIDs) != 4 {
			t.Fatalf("locked proof lost derived membership: %+v", locked.Proof)
		}

		reconciled, repeated, err := useCase.Lock(t.Context(), arena.SwissRoundLockCommand{Proof: proof})
		if err != nil || repeated || reconciled.Proof.ProofHash != proof.ProofHash {
			t.Fatalf("Lock(retry) error = %v, changed = %v, record = %+v", err, repeated, reconciled)
		}
	})

	t.Run("rejects every stale authority revision", func(t *testing.T) {
		t.Parallel()

		authoritative, input := newSwissRoundLockProof(t, 1, 4)
		tests := []struct {
			name   string
			mutate func(*arena.SwissRoundPlanRevisions)
		}{
			{name: "round", mutate: func(value *arena.SwissRoundPlanRevisions) { value.Round-- }},
			{name: "source", mutate: func(value *arena.SwissRoundPlanRevisions) { value.Source-- }},
			{name: "category", mutate: func(value *arena.SwissRoundPlanRevisions) { value.Category-- }},
			{name: "pool", mutate: func(value *arena.SwissRoundPlanRevisions) { value.Pool-- }},
			{name: "history", mutate: func(value *arena.SwissRoundPlanRevisions) { value.History-- }},
			{name: "reservation", mutate: func(value *arena.SwissRoundPlanRevisions) { value.Reservation-- }},
			{name: "membership", mutate: func(value *arena.SwissRoundPlanRevisions) { value.Membership-- }},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()

				staleInput := cloneSwissRoundLockProofInput(input)
				test.mutate(&staleInput.Revisions)
				stale, err := arena.NewSwissRoundLockProof(staleInput)
				if err != nil {
					t.Fatalf("NewSwissRoundLockProof(stale) error = %v", err)
				}
				repository := newSwissRoundLockRepositoryFake(authoritative)
				useCase := arena.NewSwissRoundLockUseCase(repository, fixedArenaClock{now: now})
				if _, changed, err := useCase.Lock(
					t.Context(), arena.SwissRoundLockCommand{Proof: stale},
				); !errors.Is(err, domain.ErrConflict) || changed {
					t.Fatalf("Lock(stale %s) error = %v, changed = %v, want conflict", test.name, err, changed)
				}
			})
		}
	})

	t.Run("rejects an edited plan and a started round", func(t *testing.T) {
		t.Parallel()

		authoritative, input := newSwissRoundLockProof(t, 1, 4)
		editedInput := cloneSwissRoundLockProofInput(input)
		editedInput.Series[0].SecondParticipantID = input.Series[1].FirstParticipantID
		editedInput.Series[1].FirstParticipantID = input.Series[0].SecondParticipantID
		edited, err := arena.NewSwissRoundLockProof(editedInput)
		if err != nil {
			t.Fatalf("NewSwissRoundLockProof(edited) error = %v", err)
		}
		repository := newSwissRoundLockRepositoryFake(authoritative)
		useCase := arena.NewSwissRoundLockUseCase(repository, fixedArenaClock{now: now})
		if _, changed, err := useCase.Lock(
			t.Context(), arena.SwissRoundLockCommand{Proof: edited},
		); !errors.Is(err, domain.ErrConflict) || changed {
			t.Fatalf("Lock(edited) error = %v, changed = %v, want conflict", err, changed)
		}

		startedAt := now.Add(-time.Minute)
		startedRepository := newSwissRoundLockRepositoryFake(authoritative)
		startedRepository.startedAt = &startedAt
		startedUseCase := arena.NewSwissRoundLockUseCase(startedRepository, fixedArenaClock{now: now})
		if _, changed, err := startedUseCase.Lock(
			t.Context(), arena.SwissRoundLockCommand{Proof: authoritative},
		); !errors.Is(err, arena.ErrSwissRoundAlreadyStarted) || changed {
			t.Fatalf("Lock(started) error = %v, changed = %v", err, changed)
		}
	})

	t.Run("serializes concurrent retries", func(t *testing.T) {
		t.Parallel()

		proof, _ := newSwissRoundLockProof(t, 1, 4)
		repository := newSwissRoundLockRepositoryFake(proof)
		useCase := arena.NewSwissRoundLockUseCase(repository, fixedArenaClock{now: now})
		var changedCount atomic.Int32
		errorsFound := make(chan error, 12)
		var group sync.WaitGroup
		for range 12 {
			group.Add(1)
			go func() {
				defer group.Done()
				_, changed, err := useCase.Lock(t.Context(), arena.SwissRoundLockCommand{Proof: proof})
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

func newSwissRoundLockProof(
	t *testing.T,
	roundNumber int,
	rosterSize int,
) (arena.SwissRoundLockProof, arena.SwissRoundLockProofInput) {
	t.Helper()

	participants := make([]uuid.UUID, rosterSize)
	for index := range participants {
		participants[index] = task026ID(100 + index)
	}
	series := make([]arena.SwissLockedSeries, 0, rosterSize/2)
	for index := 0; index+1 < rosterSize; index += 2 {
		series = append(series, arena.SwissLockedSeries{
			SeriesID: task026ID(200 + index), PairingID: task026ID(300 + index),
			FirstParticipantID: participants[index], SecondParticipantID: participants[index+1],
		})
	}
	byeParticipantID := uuid.Nil
	if rosterSize%2 == 1 {
		byeParticipantID = participants[len(participants)-1]
	}
	input := arena.SwissRoundLockProofInput{
		TournamentID: task026ID(1), RosterID: task026ID(2), RoundID: task026ID(10 + roundNumber),
		Preset: domain.ArenaPresetV1, RoundNumber: roundNumber,
		SourceRevisionID: task026ID(20), CategoryRevisionID: task026ID(21),
		PoolRevisionID: task026ID(22), PlanRevisionID: task026ID(23),
		PreflightRevisionID: task026ID(24), WaveID: task026ID(25 + roundNumber),
		WaveRevisionID: domain.ArenaWaveRevisionID(task026ID(30 + roundNumber)),
		Revisions: arena.SwissRoundPlanRevisions{
			Round: 2, Source: 2, Category: 2, Pool: 2, History: 2, Reservation: 2, Membership: 2,
		},
		RosterParticipantIDs: participants, Series: series, ByeParticipantID: byeParticipantID,
	}
	proof, err := arena.NewSwissRoundLockProof(input)
	if err != nil {
		t.Fatalf("NewSwissRoundLockProof() error = %v", err)
	}
	return proof, input
}

func cloneSwissRoundLockProofInput(input arena.SwissRoundLockProofInput) arena.SwissRoundLockProofInput {
	cloned := input
	cloned.RosterParticipantIDs = append([]uuid.UUID(nil), input.RosterParticipantIDs...)
	cloned.Series = append([]arena.SwissLockedSeries(nil), input.Series...)
	return cloned
}

func task026ID(number int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("26000000-0000-0000-0000-%012d", number))
}

type swissRoundLockRepositoryFake struct {
	mu            sync.Mutex
	authoritative arena.SwissRoundLockProof
	record        *arena.SwissRoundLockRecord
	startedAt     *time.Time
}

func newSwissRoundLockRepositoryFake(proof arena.SwissRoundLockProof) *swissRoundLockRepositoryFake {
	return &swissRoundLockRepositoryFake{authoritative: proof}
}

func (f *swissRoundLockRepositoryFake) LockSwissRound(
	_ context.Context,
	input arena.SwissRoundLockInput,
) (*arena.SwissRoundLockRecord, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.startedAt != nil || input.Proof.ProofHash != f.authoritative.ProofHash {
		return nil, false, nil
	}
	if f.record != nil {
		return cloneSwissRoundLockRecord(f.record), false, nil
	}
	f.record = &arena.SwissRoundLockRecord{Proof: input.Proof, LockedAt: timePointer(input.LockedAt)}
	return cloneSwissRoundLockRecord(f.record), true, nil
}

func (f *swissRoundLockRepositoryFake) GetSwissRoundLock(
	_ context.Context,
	roundID uuid.UUID,
) (*arena.SwissRoundLockRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if roundID != f.authoritative.RoundID {
		return nil, arena.ErrSwissRoundNotFound
	}
	if f.record != nil {
		return cloneSwissRoundLockRecord(f.record), nil
	}
	return &arena.SwissRoundLockRecord{
		Proof: f.authoritative, StartedAt: cloneTestTimePointer(f.startedAt),
	}, nil
}

func cloneSwissRoundLockRecord(record *arena.SwissRoundLockRecord) *arena.SwissRoundLockRecord {
	if record == nil {
		return nil
	}
	cloned := *record
	cloned.LockedAt = cloneTestTimePointer(record.LockedAt)
	cloned.StartedAt = cloneTestTimePointer(record.StartedAt)
	return &cloned
}
