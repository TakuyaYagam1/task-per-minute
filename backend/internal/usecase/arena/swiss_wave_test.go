package arena_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestSwissWaveMembership(t *testing.T) {
	t.Parallel()

	lockedAt := time.Date(2026, time.August, 29, 3, 15, 0, 0, time.UTC)

	t.Run("creates one complete Wave after prior official completion", func(t *testing.T) {
		t.Parallel()

		proof, _ := newSwissRoundLockProof(t, 3, 5)
		lock := arena.SwissRoundLockRecord{Proof: proof, LockedAt: timePointer(lockedAt)}
		prior := officialSwissRounds(proof, 2)
		repository := newSwissWaveRepositoryFake(lock, prior)
		useCase := arena.NewSwissWaveUseCase(repository)

		wave, changed, err := useCase.Create(t.Context(), arena.SwissWaveCreateCommand{
			Round: lock, PriorRounds: prior,
		})
		if err != nil || !changed {
			t.Fatalf("Create() error = %v, changed = %v", err, changed)
		}
		if wave.WaveID != proof.WaveID || wave.RoundID != proof.RoundID ||
			len(wave.SeriesIDs) != 2 || len(wave.ByeParticipantIDs) != 1 || len(wave.ParticipantIDs) != 5 {
			t.Fatalf("Wave membership = %+v", wave)
		}
		if !reflect.DeepEqual(wave.ParticipantIDs, proof.RosterParticipantIDs) {
			t.Fatalf("Wave participants = %v, want %v", wave.ParticipantIDs, proof.RosterParticipantIDs)
		}

		reconciled, repeated, err := useCase.Create(t.Context(), arena.SwissWaveCreateCommand{
			Round: lock, PriorRounds: prior,
		})
		if err != nil || repeated || !reflect.DeepEqual(reconciled, wave) {
			t.Fatalf("Create(retry) error = %v, changed = %v, wave = %+v", err, repeated, reconciled)
		}
	})

	t.Run("rejects duplicate or missing current membership", func(t *testing.T) {
		t.Parallel()

		proof, _ := newSwissRoundLockProof(t, 2, 4)
		prior := officialSwissRounds(proof, 1)
		tests := []struct {
			name   string
			mutate func(*arena.SwissRoundLockRecord)
		}{
			{
				name: "duplicate",
				mutate: func(record *arena.SwissRoundLockRecord) {
					record.Proof.Series[1].FirstParticipantID = record.Proof.Series[0].FirstParticipantID
				},
			},
			{
				name: "missing",
				mutate: func(record *arena.SwissRoundLockRecord) {
					record.Proof.Series = record.Proof.Series[:1]
				},
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()

				lock := arena.SwissRoundLockRecord{Proof: proof, LockedAt: timePointer(lockedAt)}
				lock.Proof.Series = append([]arena.SwissLockedSeries(nil), proof.Series...)
				test.mutate(&lock)
				repository := newSwissWaveRepositoryFake(
					arena.SwissRoundLockRecord{Proof: proof, LockedAt: timePointer(lockedAt)}, prior,
				)
				useCase := arena.NewSwissWaveUseCase(repository)
				if _, changed, err := useCase.Create(t.Context(), arena.SwissWaveCreateCommand{
					Round: lock, PriorRounds: prior,
				}); !errors.Is(err, arena.ErrInvalidSwissRoundLockProof) || changed {
					t.Fatalf("Create(%s membership) error = %v, changed = %v", test.name, err, changed)
				}
				if repository.createCalls != 0 {
					t.Fatalf("repository called %d times for invalid membership", repository.createCalls)
				}
			})
		}
	})

	t.Run("requires every prior Series and bye to be official", func(t *testing.T) {
		t.Parallel()

		proof, _ := newSwissRoundLockProof(t, 3, 5)
		lock := arena.SwissRoundLockRecord{Proof: proof, LockedAt: timePointer(lockedAt)}
		complete := officialSwissRounds(proof, 2)
		tests := []struct {
			name   string
			mutate func([]arena.SwissRoundOfficialCompletion) []arena.SwissRoundOfficialCompletion
		}{
			{name: "round missing", mutate: func(rounds []arena.SwissRoundOfficialCompletion) []arena.SwissRoundOfficialCompletion {
				return rounds[:1]
			}},
			{name: "Series missing", mutate: func(rounds []arena.SwissRoundOfficialCompletion) []arena.SwissRoundOfficialCompletion {
				rounds[1].Series = rounds[1].Series[:1]
				return rounds
			}},
			{name: "Series unofficial", mutate: func(rounds []arena.SwissRoundOfficialCompletion) []arena.SwissRoundOfficialCompletion {
				rounds[1].Series[0].ResultRevisionID = domain.ArenaOfficialResultRevisionID{}
				return rounds
			}},
			{name: "bye unofficial", mutate: func(rounds []arena.SwissRoundOfficialCompletion) []arena.SwissRoundOfficialCompletion {
				rounds[1].Bye.RevisionID = uuid.Nil
				return rounds
			}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()

				prior := test.mutate(cloneOfficialSwissRounds(complete))
				repository := newSwissWaveRepositoryFake(lock, complete)
				useCase := arena.NewSwissWaveUseCase(repository)
				if _, changed, err := useCase.Create(t.Context(), arena.SwissWaveCreateCommand{
					Round: lock, PriorRounds: prior,
				}); !errors.Is(err, arena.ErrSwissPriorRoundIncomplete) || changed {
					t.Fatalf("Create(%s) error = %v, changed = %v", test.name, err, changed)
				}
				if repository.createCalls != 0 {
					t.Fatalf("repository called %d times for incomplete prior round", repository.createCalls)
				}
			})
		}
	})

	t.Run("atomically rejects stale official evidence", func(t *testing.T) {
		t.Parallel()

		proof, _ := newSwissRoundLockProof(t, 2, 4)
		lock := arena.SwissRoundLockRecord{Proof: proof, LockedAt: timePointer(lockedAt)}
		authoritative := officialSwissRounds(proof, 1)
		stale := cloneOfficialSwissRounds(authoritative)
		stale[0].Series[0].ResultRevisionID = domain.ArenaOfficialResultRevisionID(task026ID(990))
		repository := newSwissWaveRepositoryFake(lock, authoritative)
		useCase := arena.NewSwissWaveUseCase(repository)

		if _, changed, err := useCase.Create(t.Context(), arena.SwissWaveCreateCommand{
			Round: lock, PriorRounds: stale,
		}); !errors.Is(err, domain.ErrConflict) || changed {
			t.Fatalf("Create(stale official evidence) error = %v, changed = %v", err, changed)
		}
		if repository.record != nil {
			t.Fatalf("stale evidence created Wave %+v", repository.record)
		}
	})

	t.Run("allows the first round without prior evidence", func(t *testing.T) {
		t.Parallel()

		proof, _ := newSwissRoundLockProof(t, 1, 4)
		lock := arena.SwissRoundLockRecord{Proof: proof, LockedAt: timePointer(lockedAt)}
		repository := newSwissWaveRepositoryFake(lock, nil)
		useCase := arena.NewSwissWaveUseCase(repository)

		wave, changed, err := useCase.Create(t.Context(), arena.SwissWaveCreateCommand{Round: lock})
		if err != nil || !changed || len(wave.SeriesIDs) != 2 || len(wave.ByeParticipantIDs) != 0 {
			t.Fatalf("Create(first round) error = %v, changed = %v, wave = %+v", err, changed, wave)
		}
	})
}

func officialSwissRounds(
	proof arena.SwissRoundLockProof,
	count int,
) []arena.SwissRoundOfficialCompletion {
	rounds := make([]arena.SwissRoundOfficialCompletion, count)
	for roundIndex := range count {
		series := make([]arena.SwissSeriesOfficialCompletion, 0, len(proof.Series))
		for seriesIndex, planned := range proof.Series {
			series = append(series, arena.SwissSeriesOfficialCompletion{
				SeriesID:            task026ID(500 + roundIndex*20 + seriesIndex),
				FirstParticipantID:  planned.FirstParticipantID,
				SecondParticipantID: planned.SecondParticipantID,
				State:               domain.ArenaSeriesStateCompleted,
				ResultRevisionID: domain.ArenaOfficialResultRevisionID(
					task026ID(600 + roundIndex*20 + seriesIndex),
				),
			})
		}
		var bye *arena.SwissByeOfficialCompletion
		if proof.ByeParticipantID != uuid.Nil {
			bye = &arena.SwissByeOfficialCompletion{
				ParticipantID: proof.ByeParticipantID, RevisionID: task026ID(700 + roundIndex),
			}
		}
		rounds[roundIndex] = arena.SwissRoundOfficialCompletion{
			RoundID: task026ID(400 + roundIndex), RoundNumber: roundIndex + 1,
			RevisionID: task026ID(450 + roundIndex), Series: series, Bye: bye,
		}
	}
	return rounds
}

func cloneOfficialSwissRounds(
	rounds []arena.SwissRoundOfficialCompletion,
) []arena.SwissRoundOfficialCompletion {
	cloned := make([]arena.SwissRoundOfficialCompletion, len(rounds))
	for index, round := range rounds {
		cloned[index] = round
		cloned[index].Series = append([]arena.SwissSeriesOfficialCompletion(nil), round.Series...)
		if round.Bye != nil {
			bye := *round.Bye
			cloned[index].Bye = &bye
		}
	}
	return cloned
}

type swissWaveRepositoryFake struct {
	mu          sync.Mutex
	lock        arena.SwissRoundLockRecord
	prior       []arena.SwissRoundOfficialCompletion
	record      *arena.SwissWavePlan
	createCalls int
}

func newSwissWaveRepositoryFake(
	lock arena.SwissRoundLockRecord,
	prior []arena.SwissRoundOfficialCompletion,
) *swissWaveRepositoryFake {
	return &swissWaveRepositoryFake{lock: lock, prior: cloneOfficialSwissRounds(prior)}
}

func (f *swissWaveRepositoryFake) CreateSwissWave(
	_ context.Context,
	input arena.SwissWaveCreateInput,
) (*arena.SwissWavePlan, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.createCalls++
	if input.Plan.LockProofHash != f.lock.Proof.ProofHash || !reflect.DeepEqual(input.PriorRounds, f.prior) {
		return nil, false, nil
	}
	if f.record != nil {
		return cloneSwissWavePlan(f.record), false, nil
	}
	record := input.Plan
	f.record = cloneSwissWavePlan(&record)
	return cloneSwissWavePlan(f.record), true, nil
}

func (f *swissWaveRepositoryFake) GetSwissWave(
	_ context.Context,
	waveID uuid.UUID,
) (*arena.SwissWavePlan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.record == nil || f.record.WaveID != waveID {
		return nil, arena.ErrSwissWaveNotFound
	}
	return cloneSwissWavePlan(f.record), nil
}

func cloneSwissWavePlan(plan *arena.SwissWavePlan) *arena.SwissWavePlan {
	if plan == nil {
		return nil
	}
	cloned := *plan
	cloned.SeriesIDs = append([]uuid.UUID(nil), plan.SeriesIDs...)
	cloned.ByeParticipantIDs = append([]uuid.UUID(nil), plan.ByeParticipantIDs...)
	cloned.ParticipantIDs = append([]uuid.UUID(nil), plan.ParticipantIDs...)
	cloned.PriorRoundRevisionIDs = append([]uuid.UUID(nil), plan.PriorRoundRevisionIDs...)
	return &cloned
}
