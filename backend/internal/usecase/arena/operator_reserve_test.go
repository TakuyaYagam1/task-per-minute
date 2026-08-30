package arena_test

import (
	"context"
	"crypto/sha256"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestOperatorReserveRevalidationResumesReplayRequired(t *testing.T) {
	t.Parallel()

	authority, command := task041OperatorReserveFixture(t)
	repository := &operatorReserveRepositoryFake{authority: authority}

	record, changed, err := arena.NewOperatorReserveUseCase(repository).
		Reserve(t.Context(), command)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, record.Validate())
	require.Equal(t, domain.ArenaSeriesStateReplayRequired, record.Series.Series.State)
	require.Nil(t, record.Series.ResumeState)
	require.Equal(t, authority.Exhaustion.Series.Series.Score, record.Series.Series.Score)
	require.Equal(t, authority.Reserve.RequiredCategory, record.Reserve.Snapshot.Category)
	require.True(t, record.Reserve.CandidateHealth.MutationLocked)
	require.True(t, record.Reserve.CandidateHealth.Healthy)
	require.False(t, record.Reserve.CandidateHealth.PubliclyExposed)
	require.Equal(t, arena.ReserveAssignmentModeOperator, record.Reserve.Evidence.Mode)
	require.Equal(t, command.Reserve.OperatorID, record.Reserve.Evidence.OperatorID)
	require.Nil(t, record.Reserve.CategoryExhaustion)
	require.Equal(t, 1, repository.writeCount())
}

func TestOperatorReserveRevalidationRejectsEveryStaleOrIneligibleCandidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*arena.OperatorReserveAuthority, *arena.OperatorReserveCommand)
	}{
		{
			name: "pool revision",
			mutate: func(authority *arena.OperatorReserveAuthority, _ *arena.OperatorReserveCommand) {
				authority.Reserve.Revisions.PoolRevision++
				authority.Reserve.Pool.Revision++
			},
		},
		{
			name: "history revision",
			mutate: func(authority *arena.OperatorReserveAuthority, _ *arena.OperatorReserveCommand) {
				authority.Reserve.Revisions.HistoryRevision++
			},
		},
		{
			name: "reservation revision",
			mutate: func(authority *arena.OperatorReserveAuthority, _ *arena.OperatorReserveCommand) {
				authority.Reserve.Revisions.ReservationRevision++
			},
		},
		{
			name: "prior Arena receipt",
			mutate: func(authority *arena.OperatorReserveAuthority, command *arena.OperatorReserveCommand) {
				authority.Reserve.ArenaHistory = append(
					authority.Reserve.ArenaHistory,
					arena.ArenaTaskReceiptRef{
						ParticipantID: authority.Reserve.ParticipantIDs[0],
						TaskID:        command.ProposedTaskID,
						Version:       command.ProposedVersion,
					},
				)
			},
		},
		{
			name: "wrong pool membership",
			mutate: func(authority *arena.OperatorReserveAuthority, _ *arena.OperatorReserveCommand) {
				authority.Reserve.Pool.Versions[0].Version++
			},
		},
		{
			name: "unhealthy",
			mutate: func(authority *arena.OperatorReserveAuthority, _ *arena.OperatorReserveCommand) {
				authority.Reserve.CandidateHealth.Healthy = false
			},
		},
		{
			name: "unlocked",
			mutate: func(authority *arena.OperatorReserveAuthority, _ *arena.OperatorReserveCommand) {
				authority.Reserve.CandidateHealth.MutationLocked = false
			},
		},
		{
			name: "publicly exposed",
			mutate: func(authority *arena.OperatorReserveAuthority, _ *arena.OperatorReserveCommand) {
				authority.Reserve.CandidateHealth.PubliclyExposed = true
			},
		},
		{
			name: "artifact changed",
			mutate: func(authority *arena.OperatorReserveAuthority, _ *arena.OperatorReserveCommand) {
				authority.Reserve.CandidateContentDigest = sha256.Sum256([]byte("changed artifact"))
			},
		},
		{
			name: "different category",
			mutate: func(authority *arena.OperatorReserveAuthority, _ *arena.OperatorReserveCommand) {
				setReserveCandidateCategory(t, &authority.Reserve, domain.CategoryCrypto)
			},
		},
		{
			name: "foreign participant reservation",
			mutate: func(authority *arena.OperatorReserveAuthority, _ *arena.OperatorReserveCommand) {
				authority.Reserve.ParticipantReservations[0].Reservation.OwnerID = task041ID(903)
			},
		},
		{
			name: "different proposed version",
			mutate: func(_ *arena.OperatorReserveAuthority, command *arena.OperatorReserveCommand) {
				command.ProposedVersion++
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			authority, command := task041OperatorReserveFixture(t)
			test.mutate(&authority, &command)
			repository := &operatorReserveRepositoryFake{authority: authority}

			record, changed, err := arena.NewOperatorReserveUseCase(repository).
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

	authority, command := task041OperatorReserveFixture(t)
	repository := &operatorReserveRepositoryFake{authority: authority, conflicts: 2}

	record, changed, err := arena.NewOperatorReserveUseCase(repository).
		Reserve(t.Context(), command)
	require.ErrorIs(t, err, arena.ErrOperatorReserveConflict)
	require.Nil(t, record)
	require.False(t, changed)
	require.Equal(t, 0, repository.writeCount())
}

func TestOperatorReserveRevalidationConcurrentDuplicateWritesOnce(t *testing.T) {
	t.Parallel()

	authority, command := task041OperatorReserveFixture(t)
	repository := &operatorReserveRepositoryFake{authority: authority}
	results := make(chan struct {
		changed bool
		err     error
	}, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, changed, err := arena.NewOperatorReserveUseCase(repository).
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

type operatorReserveRepositoryFake struct {
	mu        sync.Mutex
	authority arena.OperatorReserveAuthority
	current   *arena.OperatorReserve
	conflicts int
	writes    int
}

func (r *operatorReserveRepositoryFake) LoadOperatorReserveAuthority(
	_ context.Context,
	_ arena.ReplayReplacementScope,
) (arena.OperatorReserveAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	authority := r.authority
	if r.current != nil {
		current := *r.current
		authority.Current = &current
	}
	return authority, nil
}

func (r *operatorReserveRepositoryFake) CommitOperatorReserve(
	_ context.Context,
	record arena.OperatorReserve,
) (*arena.OperatorReserve, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conflicts > 0 {
		r.conflicts--
		return nil, false, domain.ErrConflict
	}
	if r.current != nil {
		return nil, false, domain.ErrConflict
	}
	committed := record
	r.current = &committed
	r.writes++
	return &committed, true, nil
}

func (r *operatorReserveRepositoryFake) writeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writes
}

func task041OperatorReserveFixture(
	t *testing.T,
) (arena.OperatorReserveAuthority, arena.OperatorReserveCommand) {
	t.Helper()

	exhaustionAuthority, exhaustionCommand := task041ReplayExhaustionFixture(t)
	exhaustionRepository := &replayReserveExhaustionRepositoryFake{authority: exhaustionAuthority}
	exhaustion, changed, err := arena.NewReplayReserveExhaustionUseCase(exhaustionRepository).
		Pause(t.Context(), exhaustionCommand)
	require.NoError(t, err)
	require.True(t, changed)

	reserve, reserveCommand := reserveAssignmentFixture(t)
	reserve.Scope = arena.ReserveAssignmentScope{
		TournamentID: exhaustion.Scope.TournamentID,
		AssignmentID: exhaustion.Scope.AssignmentID,
		AttemptID:    exhaustion.AssignmentAttemptID,
		SlotID:       exhaustion.Scope.SlotID,
	}
	reserve.CurrentSnapshotID = exhaustion.ActiveSnapshotID
	reserve.RequiredCategory = exhaustion.Category
	reserve.ParticipantIDs = []uuid.UUID{
		exhaustion.Series.Series.FirstParticipantID,
		exhaustion.Series.Series.SecondParticipantID,
	}
	for index := range reserve.ParticipantReservations {
		reserve.ParticipantReservations[index].ParticipantID = reserve.ParticipantIDs[index]
		reserve.ParticipantReservations[index].Reservation.OwnerID = exhaustion.Scope.TournamentID
	}
	reserveCommand.Scope = reserve.Scope
	reserveCommand.ExpectedSnapshotID = reserve.CurrentSnapshotID
	reserveCommand.Mode = arena.ReserveAssignmentModeOperator
	reserveCommand.OperatorID = task041ID(20)
	reserveCommand.Reason = "approve one locked same-category replay reserve"

	authority := arena.OperatorReserveAuthority{
		Scope: exhaustion.Scope, Revision: 15, Exhaustion: *exhaustion, Reserve: reserve,
	}
	command := arena.OperatorReserveCommand{
		Scope: authority.Scope, CommandID: task041ID(21),
		ExpectedExhaustionCommandID: exhaustion.CommandID,
		ExpectedRevisions:           reserve.Revisions,
		ProposedTaskID:              reserve.CandidateSnapshot.TaskID,
		ProposedVersion:             reserve.CandidateSnapshot.Version,
		ProposedSnapshotID:          reserve.CandidateSnapshot.SnapshotID,
		Reserve:                     reserveCommand,
	}
	return authority, command
}

var _ arena.OperatorReserveRepository = (*operatorReserveRepositoryFake)(nil)
