package game_test

import (
	"context"
	"crypto/sha256"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
)

func TestOperatorReserveRevalidationResumesReplayRequired(t *testing.T) {
	t.Parallel()

	authority, command := operatorReserveFixture(t)
	repository := newOperatorReserveRepositoryHarness(t, authority)

	record, changed, err := gameusecase.NewOperatorReserveUseCase(repository).
		Reserve(t.Context(), command)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, record.Validate())
	require.Equal(t, domain.SeriesStateReplayRequired, record.Series.Series.State)
	require.Nil(t, record.Series.ResumeState)
	require.Equal(t, authority.Exhaustion.Series.Series.Score, record.Series.Series.Score)
	require.Equal(t, authority.Reserve.RequiredCategory, record.Reserve.Snapshot.Category)
	require.True(t, record.Reserve.CandidateHealth.MutationLocked)
	require.True(t, record.Reserve.CandidateHealth.Healthy)
	require.False(t, record.Reserve.CandidateHealth.PubliclyExposed)
	require.Equal(t, assignmentusecase.ReserveAssignmentModeOperator, record.Reserve.Evidence.Mode)
	require.Equal(t, command.Reserve.OperatorID, record.Reserve.Evidence.OperatorID)
	require.Nil(t, record.Reserve.CategoryExhaustion)
	require.Equal(t, 1, repository.writeCount())

	record.Reserve.ParticipantIDs[0] = uuid.Nil
	repeated, changed, err := gameusecase.NewOperatorReserveUseCase(repository).
		Reserve(t.Context(), command)
	require.NoError(t, err)
	require.False(t, changed)
	require.NoError(t, repeated.Validate())
	require.NotEqual(t, uuid.Nil, repeated.Reserve.ParticipantIDs[0])
	require.Equal(t, 1, repository.writeCount())
}

func TestOperatorReserveRevalidationRejectsEveryStaleOrIneligibleCandidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*gameusecase.OperatorReserveAuthority, *gameusecase.OperatorReserveCommand)
	}{
		{
			name: "pool revision",
			mutate: func(authority *gameusecase.OperatorReserveAuthority, _ *gameusecase.OperatorReserveCommand) {
				authority.Reserve.Revisions.PoolRevision++
				authority.Reserve.Pool.Revision++
			},
		},
		{
			name: "history revision",
			mutate: func(authority *gameusecase.OperatorReserveAuthority, _ *gameusecase.OperatorReserveCommand) {
				authority.Reserve.Revisions.HistoryRevision++
			},
		},
		{
			name: "reservation revision",
			mutate: func(authority *gameusecase.OperatorReserveAuthority, _ *gameusecase.OperatorReserveCommand) {
				authority.Reserve.Revisions.ReservationRevision++
			},
		},
		{
			name: "prior task receipt",
			mutate: func(authority *gameusecase.OperatorReserveAuthority, command *gameusecase.OperatorReserveCommand) {
				authority.Reserve.ReceiptHistory = append(
					authority.Reserve.ReceiptHistory,
					assignmentusecase.TaskReceiptRef{
						ParticipantID: authority.Reserve.ParticipantIDs[0],
						TaskID:        command.ProposedTaskID,
						Version:       command.ProposedVersion,
					},
				)
			},
		},
		{
			name: "wrong pool membership",
			mutate: func(authority *gameusecase.OperatorReserveAuthority, _ *gameusecase.OperatorReserveCommand) {
				authority.Reserve.Pool.Versions[0].Version++
			},
		},
		{
			name: "unhealthy",
			mutate: func(authority *gameusecase.OperatorReserveAuthority, _ *gameusecase.OperatorReserveCommand) {
				authority.Reserve.CandidateHealth.Healthy = false
			},
		},
		{
			name: "unlocked",
			mutate: func(authority *gameusecase.OperatorReserveAuthority, _ *gameusecase.OperatorReserveCommand) {
				authority.Reserve.CandidateHealth.MutationLocked = false
			},
		},
		{
			name: "publicly exposed",
			mutate: func(authority *gameusecase.OperatorReserveAuthority, _ *gameusecase.OperatorReserveCommand) {
				authority.Reserve.CandidateHealth.PubliclyExposed = true
			},
		},
		{
			name: "artifact changed",
			mutate: func(authority *gameusecase.OperatorReserveAuthority, _ *gameusecase.OperatorReserveCommand) {
				authority.Reserve.CandidateContentDigest = sha256.Sum256([]byte("changed artifact"))
			},
		},
		{
			name: "different category",
			mutate: func(authority *gameusecase.OperatorReserveAuthority, _ *gameusecase.OperatorReserveCommand) {
				setOperatorReserveCandidateCategory(t, &authority.Reserve, domain.CategoryCrypto)
			},
		},
		{
			name: "foreign participant reservation",
			mutate: func(authority *gameusecase.OperatorReserveAuthority, _ *gameusecase.OperatorReserveCommand) {
				authority.Reserve.ParticipantReservations[0].Reservation.TournamentID = operatorReserveID(903)
			},
		},
		{
			name: "different proposed version",
			mutate: func(_ *gameusecase.OperatorReserveAuthority, command *gameusecase.OperatorReserveCommand) {
				command.ProposedVersion++
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			authority, command := operatorReserveFixture(t)
			test.mutate(&authority, &command)
			repository := newOperatorReserveRepositoryHarness(t, authority)

			record, changed, err := gameusecase.NewOperatorReserveUseCase(repository).
				Reserve(t.Context(), command)
			require.Error(t, err)
			require.Nil(t, record)
			require.False(t, changed)
			require.Equal(t, 0, repository.writeCount())
		})
	}
}

func TestOperatorReserveRevalidationConflictHasNoWrites(t *testing.T) {
	t.Parallel()

	authority, command := operatorReserveFixture(t)
	repository := newOperatorReserveRepositoryHarness(t, authority)
	repository.state.conflicts = 2

	record, changed, err := gameusecase.NewOperatorReserveUseCase(repository).
		Reserve(t.Context(), command)
	require.ErrorIs(t, err, gameusecase.ErrOperatorReserveConflict)
	require.Nil(t, record)
	require.False(t, changed)
	require.Equal(t, 0, repository.writeCount())
}

func TestOperatorReserveRevalidationConcurrentDuplicateWritesOnce(t *testing.T) {
	t.Parallel()

	authority, command := operatorReserveFixture(t)
	repository := newOperatorReserveRepositoryHarness(t, authority)
	results := make(chan struct {
		changed bool
		err     error
	}, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, changed, err := gameusecase.NewOperatorReserveUseCase(repository).
				Reserve(context.Background(), command)
			results <- struct {
				changed bool
				err     error
			}{changed: changed, err: err}
		}()
	}
	group.Wait()
	close(results)

	var changedCount int
	for result := range results {
		require.NoError(t, result.err)
		if result.changed {
			changedCount++
		}
	}
	require.Equal(t, 1, changedCount)
	require.Equal(t, 1, repository.writeCount())
}
