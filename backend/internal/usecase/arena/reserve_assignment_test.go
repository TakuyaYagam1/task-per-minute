package arena_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/taskexec"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestReserveAssignmentRevalidationRecordsAutomaticAndOperatorEvidence(t *testing.T) {
	t.Parallel()

	for _, mode := range []arena.ReserveAssignmentMode{
		arena.ReserveAssignmentModeAutomatic,
		arena.ReserveAssignmentModeOperator,
	} {
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()

			authority, command := reserveAssignmentFixture(t)
			command.Mode = mode
			if mode == arena.ReserveAssignmentModeOperator {
				command.OperatorID = task034ID(80)
				command.Reason = "replace failed isolated instance"
			}
			repository := &reserveAssignmentRepositoryFake{authorities: []arena.ReserveAssignmentAuthority{authority}}

			record, changed, err := arena.NewReserveAssignmentUseCase(repository).
				Promote(t.Context(), command)
			require.NoError(t, err)
			require.True(t, changed)
			require.NoError(t, record.Validate())
			require.Equal(t, mode, record.Evidence.Mode)
			require.Equal(t, command.OperatorID, record.Evidence.OperatorID)
			require.Equal(t, command.Reason, record.Evidence.Reason)
		})
	}
}

func TestReserveAssignmentRevalidationRejectsEveryStaleAuthority(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*arena.ReserveAssignmentAuthority)
	}{
		{
			name: "Arena history",
			mutate: func(authority *arena.ReserveAssignmentAuthority) {
				authority.ArenaHistory = append(authority.ArenaHistory, arena.ArenaTaskReceiptRef{
					ParticipantID: authority.ParticipantIDs[0],
					TaskID:        authority.CandidateSnapshot.TaskID,
					Version:       authority.CandidateSnapshot.Version,
				})
			},
		},
		{
			name: "pool membership",
			mutate: func(authority *arena.ReserveAssignmentAuthority) {
				authority.Pool.Versions[0].Version++
			},
		},
		{
			name: "health",
			mutate: func(authority *arena.ReserveAssignmentAuthority) {
				authority.CandidateHealth.Healthy = false
			},
		},
		{
			name: "public exposure",
			mutate: func(authority *arena.ReserveAssignmentAuthority) {
				authority.CandidateHealth.PubliclyExposed = true
			},
		},
		{
			name: "artifact digest",
			mutate: func(authority *arena.ReserveAssignmentAuthority) {
				authority.CandidateContentDigest = sha256.Sum256([]byte("changed artifact"))
			},
		},
		{
			name: "category",
			mutate: func(authority *arena.ReserveAssignmentAuthority) {
				setReserveCandidateCategory(t, authority, domain.CategoryCrypto)
			},
		},
		{
			name: "participant reservation",
			mutate: func(authority *arena.ReserveAssignmentAuthority) {
				authority.ParticipantReservations[0].Reservation.OwnerID = task034ID(99)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			authority, command := reserveAssignmentFixture(t)
			test.mutate(&authority)
			repository := &reserveAssignmentRepositoryFake{authorities: []arena.ReserveAssignmentAuthority{authority}}
			record, changed, err := arena.NewReserveAssignmentUseCase(repository).
				Promote(t.Context(), command)
			require.Error(t, err)
			require.Nil(t, record)
			require.False(t, changed)
			require.Equal(t, 0, repository.commitCount())
		})
	}
}

func TestReserveAssignmentRevalidationAllowsReasonedCategoryExhaustion(t *testing.T) {
	t.Parallel()

	authority, command := reserveAssignmentFixture(t)
	setReserveCandidateCategory(t, &authority, domain.CategoryCrypto)
	authority.CategoryExhaustion = &arena.ReserveCategoryExhaustionEvidence{
		RequiredCategory: authority.RequiredCategory,
		Reason:           "no eligible Web reserve remains after exact revalidation",
		ProofDigest:      sha256.Sum256([]byte("verified Web exhaustion")),
	}
	repository := &reserveAssignmentRepositoryFake{authorities: []arena.ReserveAssignmentAuthority{authority}}

	record, changed, err := arena.NewReserveAssignmentUseCase(repository).Promote(t.Context(), command)
	require.NoError(t, err)
	require.True(t, changed)
	require.NotNil(t, record.CategoryExhaustion)
	require.Equal(t, domain.CategoryWeb, record.CategoryExhaustion.RequiredCategory)
	require.Equal(t, domain.CategoryCrypto, record.Snapshot.Category)
}

func TestReserveAssignmentRevalidationReloadsAllAuthorityAfterConflict(t *testing.T) {
	t.Parallel()

	first, command := reserveAssignmentFixture(t)
	second := cloneReserveAssignmentAuthority(first)
	second.Revisions.HistoryRevision++
	second.CandidateHealth.PubliclyExposed = true
	repository := &reserveAssignmentRepositoryFake{
		authorities: []arena.ReserveAssignmentAuthority{first, second},
		conflicts:   1,
	}

	record, changed, err := arena.NewReserveAssignmentUseCase(repository).Promote(t.Context(), command)
	require.ErrorIs(t, err, arena.ErrTaskIneligible)
	require.Nil(t, record)
	require.False(t, changed)
	require.Equal(t, 2, repository.loadCount())
	require.Equal(t, 1, repository.commitCount())
}

func TestReserveAssignmentRevalidationConcurrentCommitHasOneWinner(t *testing.T) {
	t.Parallel()

	authority, firstCommand := reserveAssignmentFixture(t)
	secondCommand := firstCommand
	secondCommand.EvidenceID = task034ID(70)
	repository := &competingReserveAssignmentRepository{authority: authority}
	commands := []arena.ReserveAssignmentCommand{firstCommand, secondCommand}
	results := make(chan error, len(commands))
	var group sync.WaitGroup
	for _, command := range commands {
		group.Add(1)
		go func() {
			defer group.Done()
			_, _, err := arena.NewReserveAssignmentUseCase(repository).
				Promote(context.Background(), command)
			results <- err
		}()
	}
	group.Wait()
	close(results)

	var success, conflict int
	for err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, arena.ErrReserveAssignmentConflict):
			conflict++
		default:
			t.Fatalf("unexpected concurrent result: %v", err)
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, conflict)
}

type reserveAssignmentRepositoryFake struct {
	mu          sync.Mutex
	authorities []arena.ReserveAssignmentAuthority
	conflicts   int
	loads       int
	commits     int
	committed   *arena.ReserveAssignmentRecord
}

func (r *reserveAssignmentRepositoryFake) LoadReserveAssignmentAuthority(
	_ context.Context,
	_ arena.ReserveAssignmentScope,
) (arena.ReserveAssignmentAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	index := min(r.loads, len(r.authorities)-1)
	r.loads++
	return cloneReserveAssignmentAuthority(r.authorities[index]), nil
}

func (r *reserveAssignmentRepositoryFake) CommitReserveAssignment(
	_ context.Context,
	record arena.ReserveAssignmentRecord,
) (*arena.ReserveAssignmentRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commits++
	if r.conflicts > 0 {
		r.conflicts--
		return nil, false, domain.ErrConflict
	}
	if r.committed != nil {
		if r.committed.ProofDigest == record.ProofDigest {
			cloned := *r.committed
			return &cloned, false, nil
		}
		return nil, false, domain.ErrConflict
	}
	committed := record
	r.committed = &committed
	return &committed, true, nil
}

func (r *reserveAssignmentRepositoryFake) loadCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loads
}

func (r *reserveAssignmentRepositoryFake) commitCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commits
}

type competingReserveAssignmentRepository struct {
	mu        sync.Mutex
	authority arena.ReserveAssignmentAuthority
	winner    *arena.ReserveAssignmentRecord
}

func (r *competingReserveAssignmentRepository) LoadReserveAssignmentAuthority(
	_ context.Context,
	_ arena.ReserveAssignmentScope,
) (arena.ReserveAssignmentAuthority, error) {
	return cloneReserveAssignmentAuthority(r.authority), nil
}

func (r *competingReserveAssignmentRepository) CommitReserveAssignment(
	_ context.Context,
	record arena.ReserveAssignmentRecord,
) (*arena.ReserveAssignmentRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.winner != nil {
		return nil, false, domain.ErrConflict
	}
	committed := record
	r.winner = &committed
	return &committed, true, nil
}

func reserveAssignmentFixture(
	t *testing.T,
) (arena.ReserveAssignmentAuthority, arena.ReserveAssignmentCommand) {
	t.Helper()

	eligibility := taskEligibilityFixture()
	snapshot, err := taskexec.BuildSnapshot(taskexec.SnapshotInput{
		SnapshotID: task034ID(60), Version: eligibility.Candidate.Version,
		Kind: eligibility.Pool.Kind, Task: eligibility.Candidate.Task,
	})
	require.NoError(t, err)
	digest, err := taskexec.SnapshotDigest(snapshot)
	require.NoError(t, err)
	now := time.Date(2026, 8, 30, 19, 0, 0, 0, time.UTC)
	scope := arena.ReserveAssignmentScope{
		TournamentID: task034ID(61), AssignmentID: task034ID(62),
		AttemptID: task034ID(63), SlotID: task034ID(64),
	}
	reservations := []arena.ExactNormalParticipantReservation{
		{
			ParticipantID: eligibility.ParticipantIDs[0], PlayerID: task034ID(65),
			Reservation: arenaReservation(3401, task034ID(65), scope.TournamentID, now),
		},
		{
			ParticipantID: eligibility.ParticipantIDs[1], PlayerID: task034ID(66),
			Reservation: arenaReservation(3402, task034ID(66), scope.TournamentID, now),
		},
	}
	authority := arena.ReserveAssignmentAuthority{
		Scope: scope,
		Revisions: arena.ReserveAssignmentSourceRevisions{
			AssignmentRevision: 4,
			PoolRevisionID:     eligibility.Pool.ID, PoolRevision: eligibility.Pool.Revision,
			HistoryRevisionID: task034ID(67), HistoryRevision: 5,
			ArtifactRevisionID: task034ID(68), ArtifactRevision: 6,
			ReservationRevisionID: task034ID(69), ReservationRevision: 7,
			CategoryRevisionID: task034ID(71), CategoryRevision: 8,
		},
		CurrentSnapshotID: task034ID(59), RequiredCategory: eligibility.Category,
		ParticipantIDs: eligibility.ParticipantIDs, ParticipantReservations: reservations,
		Pool: eligibility.Pool, CandidateHealth: eligibility.Candidate.Health,
		CandidateSnapshot: snapshot, CandidateContentDigest: digest,
	}
	command := arena.ReserveAssignmentCommand{
		Scope: scope, ExpectedSnapshotID: authority.CurrentSnapshotID,
		EvidenceID: task034ID(72), Mode: arena.ReserveAssignmentModeAutomatic,
		PromotedAt: now,
	}
	return authority, command
}

func setReserveCandidateCategory(
	t *testing.T,
	authority *arena.ReserveAssignmentAuthority,
	category domain.Category,
) {
	t.Helper()
	task := task034Task(4, category)
	snapshot, err := taskexec.BuildSnapshot(taskexec.SnapshotInput{
		SnapshotID: authority.CandidateSnapshot.SnapshotID,
		Version:    authority.CandidateSnapshot.Version,
		Kind:       authority.CandidateSnapshot.Kind,
		Task:       task,
	})
	require.NoError(t, err)
	digest, err := taskexec.SnapshotDigest(snapshot)
	require.NoError(t, err)
	authority.CandidateSnapshot = snapshot
	authority.CandidateContentDigest = digest
}

func cloneReserveAssignmentAuthority(
	authority arena.ReserveAssignmentAuthority,
) arena.ReserveAssignmentAuthority {
	cloned := authority
	cloned.ParticipantIDs = append(cloned.ParticipantIDs[:0:0], authority.ParticipantIDs...)
	cloned.ParticipantReservations = append(
		cloned.ParticipantReservations[:0:0], authority.ParticipantReservations...,
	)
	cloned.Pool.Versions = append(cloned.Pool.Versions[:0:0], authority.Pool.Versions...)
	cloned.ArenaHistory = append(cloned.ArenaHistory[:0:0], authority.ArenaHistory...)
	cloned.CandidateSnapshot.Hints = append(
		cloned.CandidateSnapshot.Hints[:0:0], authority.CandidateSnapshot.Hints...,
	)
	if authority.CategoryExhaustion != nil {
		exhaustion := *authority.CategoryExhaustion
		exhaustion.EligibleSameCategory = append(
			exhaustion.EligibleSameCategory[:0:0], authority.CategoryExhaustion.EligibleSameCategory...,
		)
		cloned.CategoryExhaustion = &exhaustion
	}
	return cloned
}

var _ arena.ReserveAssignmentRepository = (*reserveAssignmentRepositoryFake)(nil)
var _ arena.ReserveAssignmentRepository = (*competingReserveAssignmentRepository)(nil)
