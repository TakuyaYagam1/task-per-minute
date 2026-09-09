package assignment_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/taskexec"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	assignmentmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment/mocks"
)

func TestReserveAssignmentRevalidationRecordsAutomaticAndOperatorEvidence(t *testing.T) {
	t.Parallel()

	for _, mode := range []assignmentusecase.ReserveAssignmentMode{
		assignmentusecase.ReserveAssignmentModeAutomatic,
		assignmentusecase.ReserveAssignmentModeOperator,
	} {
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()

			authority, command := reserveAssignmentFixture(t)
			command.Mode = mode
			if mode == assignmentusecase.ReserveAssignmentModeOperator {
				command.OperatorID = task034ID(80)
				command.Reason = "replace failed isolated instance"
			}
			repository, _ := newReserveAssignmentRepository(
				t,
				[]assignmentusecase.ReserveAssignmentAuthority{authority},
				0,
			)

			record, changed, err := assignmentusecase.NewReserveAssignmentUseCase(repository).
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
		mutate func(*assignmentusecase.ReserveAssignmentAuthority)
	}{
		{
			name: "receipt history",
			mutate: func(authority *assignmentusecase.ReserveAssignmentAuthority) {
				authority.ReceiptHistory = append(authority.ReceiptHistory, assignmentusecase.TaskReceiptRef{
					ParticipantID: authority.ParticipantIDs[0],
					TaskID:        authority.CandidateSnapshot.TaskID,
					Version:       authority.CandidateSnapshot.Version,
				})
			},
		},
		{
			name: "pool membership",
			mutate: func(authority *assignmentusecase.ReserveAssignmentAuthority) {
				authority.Pool.Versions[0].Version++
			},
		},
		{
			name: "health",
			mutate: func(authority *assignmentusecase.ReserveAssignmentAuthority) {
				authority.CandidateHealth.Healthy = false
			},
		},
		{
			name: "public exposure",
			mutate: func(authority *assignmentusecase.ReserveAssignmentAuthority) {
				authority.CandidateHealth.PubliclyExposed = true
			},
		},
		{
			name: "artifact digest",
			mutate: func(authority *assignmentusecase.ReserveAssignmentAuthority) {
				authority.CandidateContentDigest = sha256.Sum256([]byte("changed artifact"))
			},
		},
		{
			name: "category",
			mutate: func(authority *assignmentusecase.ReserveAssignmentAuthority) {
				setReserveCandidateCategory(t, authority, domain.CategoryCrypto)
			},
		},
		{
			name: "participant reservation",
			mutate: func(authority *assignmentusecase.ReserveAssignmentAuthority) {
				authority.ParticipantReservations[0].Reservation.TournamentID = task034ID(99)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			authority, command := reserveAssignmentFixture(t)
			test.mutate(&authority)
			repository, state := newReserveAssignmentRepository(
				t,
				[]assignmentusecase.ReserveAssignmentAuthority{authority},
				0,
			)
			record, changed, err := assignmentusecase.NewReserveAssignmentUseCase(repository).
				Promote(t.Context(), command)
			require.Error(t, err)
			require.Nil(t, record)
			require.False(t, changed)
			require.Equal(t, 0, state.commitCount())
		})
	}
}

func TestReserveAssignmentRevalidationAllowsReasonedCategoryExhaustion(t *testing.T) {
	t.Parallel()

	authority, command := reserveAssignmentFixture(t)
	setReserveCandidateCategory(t, &authority, domain.CategoryCrypto)
	authority.CategoryExhaustion = &assignmentusecase.ReserveCategoryExhaustionEvidence{
		RequiredCategory: authority.RequiredCategory,
		Reason:           "no eligible Web reserve remains after exact revalidation",
		ProofDigest:      sha256.Sum256([]byte("verified Web exhaustion")),
	}
	repository, _ := newReserveAssignmentRepository(
		t,
		[]assignmentusecase.ReserveAssignmentAuthority{authority},
		0,
	)

	record, changed, err := assignmentusecase.NewReserveAssignmentUseCase(repository).Promote(t.Context(), command)
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
	repository, state := newReserveAssignmentRepository(
		t,
		[]assignmentusecase.ReserveAssignmentAuthority{first, second},
		1,
	)

	record, changed, err := assignmentusecase.NewReserveAssignmentUseCase(repository).Promote(t.Context(), command)
	require.ErrorIs(t, err, assignmentusecase.ErrTaskIneligible)
	require.Nil(t, record)
	require.False(t, changed)
	require.Equal(t, 2, state.loadCount())
	require.Equal(t, 1, state.commitCount())
}

func TestReserveAssignmentRevalidationConcurrentCommitHasOneWinner(t *testing.T) {
	t.Parallel()

	authority, firstCommand := reserveAssignmentFixture(t)
	secondCommand := firstCommand
	secondCommand.EvidenceID = task034ID(70)
	repository, _ := newReserveAssignmentRepository(
		t,
		[]assignmentusecase.ReserveAssignmentAuthority{authority},
		0,
	)
	commands := []assignmentusecase.ReserveAssignmentCommand{firstCommand, secondCommand}
	results := make(chan error, len(commands))
	var group sync.WaitGroup
	for _, command := range commands {
		group.Add(1)
		go func() {
			defer group.Done()
			_, _, err := assignmentusecase.NewReserveAssignmentUseCase(repository).
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
		case errors.Is(err, assignmentusecase.ErrReserveAssignmentConflict):
			conflict++
		default:
			t.Fatalf("unexpected concurrent result: %v", err)
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, conflict)
}

type reserveAssignmentState struct {
	mu          sync.Mutex
	authorities []assignmentusecase.ReserveAssignmentAuthority
	conflicts   int
	loads       int
	commits     int
	committed   *assignmentusecase.ReserveAssignmentRecord
}

func newReserveAssignmentRepository(
	t *testing.T,
	authorities []assignmentusecase.ReserveAssignmentAuthority,
	conflicts int,
) (*assignmentmocks.MockReserveAssignmentRepository, *reserveAssignmentState) {
	t.Helper()

	state := &reserveAssignmentState{authorities: authorities, conflicts: conflicts}
	repository := assignmentmocks.NewMockReserveAssignmentRepository(t)
	repository.EXPECT().
		LoadReserveAssignmentAuthority(mock.Anything, mock.Anything).
		RunAndReturn(func(
			context.Context,
			assignmentusecase.ReserveAssignmentScope,
		) (assignmentusecase.ReserveAssignmentAuthority, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			index := min(state.loads, len(state.authorities)-1)
			state.loads++
			return cloneReserveAssignmentAuthority(state.authorities[index]), nil
		})
	repository.EXPECT().
		CommitReserveAssignment(mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context,
			record assignmentusecase.ReserveAssignmentRecord,
		) (*assignmentusecase.ReserveAssignmentRecord, bool, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			state.commits++
			if state.conflicts > 0 {
				state.conflicts--
				return nil, false, domain.ErrConflict
			}
			if state.committed != nil {
				if state.committed.ProofDigest == record.ProofDigest {
					cloned := *state.committed
					return &cloned, false, nil
				}
				return nil, false, domain.ErrConflict
			}
			committed := record
			state.committed = &committed
			return &committed, true, nil
		}).
		Maybe()
	return repository, state
}

func (r *reserveAssignmentState) loadCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loads
}

func (r *reserveAssignmentState) commitCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commits
}

func reserveAssignmentFixture(
	t *testing.T,
) (assignmentusecase.ReserveAssignmentAuthority, assignmentusecase.ReserveAssignmentCommand) {
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
	scope := assignmentusecase.ReserveAssignmentScope{
		TournamentID: task034ID(61), AssignmentID: task034ID(62),
		AttemptID: task034ID(63), SlotID: task034ID(64),
	}
	reservations := []assignmentusecase.ExactNormalParticipantReservation{
		{
			ParticipantID: eligibility.ParticipantIDs[0], PlayerID: task034ID(65),
			Reservation: reservationFixture(3401, task034ID(65), scope.TournamentID, now),
		},
		{
			ParticipantID: eligibility.ParticipantIDs[1], PlayerID: task034ID(66),
			Reservation: reservationFixture(3402, task034ID(66), scope.TournamentID, now),
		},
	}
	authority := assignmentusecase.ReserveAssignmentAuthority{
		Scope: scope,
		Revisions: assignmentusecase.ReserveAssignmentSourceRevisions{
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
	command := assignmentusecase.ReserveAssignmentCommand{
		Scope: scope, ExpectedSnapshotID: authority.CurrentSnapshotID,
		EvidenceID: task034ID(72), Mode: assignmentusecase.ReserveAssignmentModeAutomatic,
		PromotedAt: now,
	}
	return authority, command
}

func setReserveCandidateCategory(
	t *testing.T,
	authority *assignmentusecase.ReserveAssignmentAuthority,
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
	authority assignmentusecase.ReserveAssignmentAuthority,
) assignmentusecase.ReserveAssignmentAuthority {
	cloned := authority
	cloned.ParticipantIDs = append(cloned.ParticipantIDs[:0:0], authority.ParticipantIDs...)
	cloned.ParticipantReservations = append(
		cloned.ParticipantReservations[:0:0], authority.ParticipantReservations...,
	)
	cloned.Pool.Versions = append(cloned.Pool.Versions[:0:0], authority.Pool.Versions...)
	cloned.ReceiptHistory = append(cloned.ReceiptHistory[:0:0], authority.ReceiptHistory...)
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
