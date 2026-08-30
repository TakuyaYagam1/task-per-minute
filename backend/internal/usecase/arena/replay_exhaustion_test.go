package arena_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestReplayReserveExhaustionPausesAfterTwoSameCategoryReserves(t *testing.T) {
	t.Parallel()

	authority, command := task041ReplayExhaustionFixture(t)
	repository := &replayReserveExhaustionRepositoryFake{authority: authority}

	record, changed, err := arena.NewReplayReserveExhaustionUseCase(repository).
		Pause(t.Context(), command)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, record.Validate())
	require.Equal(t, domain.ArenaSeriesStateTechnicalPause, record.Series.Series.State)
	require.Equal(t, domain.ArenaSeriesStateReplayRequired, *record.Series.ResumeState)
	require.Equal(t, authority.FailedAttempt.Series.Series.Score, record.Series.Series.Score)
	require.Equal(t, authority.FailedAttempt.Failure.CategoryCutoff, record.Category)
	require.Equal(t, domain.ArenaAssignmentReserveCount+1, record.ReservePosition)
	require.Nil(t, record.Series.Series.WinnerID)
	require.Equal(t, domain.ArenaWaveStateCompleted, record.OldWave.State)
	require.Equal(t, authority.OldWaveClosure.Wave.ID, record.OldWave.ID)
	require.Equal(t, authority.OldWaveClosure.Wave.RevisionID, record.OldWave.RevisionID)
	require.Len(t, record.Series.Series.Slots, len(authority.FailedAttempt.Series.Series.Slots))
	require.Equal(t, 1, repository.writeCount())
}

func TestReplayReserveExhaustionRejectsIncompleteOrStaleEvidence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*arena.ReplayReserveExhaustionAuthority, *arena.ReplayReserveExhaustionCommand)
	}{
		{
			name: "one planned reserve remains",
			mutate: func(authority *arena.ReplayReserveExhaustionAuthority, _ *arena.ReplayReserveExhaustionCommand) {
				authority.ReserveChain.ActiveIndex--
				authority.FailedAttempt.ActiveSnapshotID =
					authority.ReserveChain.Snapshots[authority.ReserveChain.ActiveIndex].SnapshotID
			},
		},
		{
			name: "active snapshot changed",
			mutate: func(_ *arena.ReplayReserveExhaustionAuthority, command *arena.ReplayReserveExhaustionCommand) {
				command.ExpectedActiveSnapshotID = task041ID(901)
			},
		},
		{
			name: "old Wave revision changed",
			mutate: func(_ *arena.ReplayReserveExhaustionAuthority, command *arena.ReplayReserveExhaustionCommand) {
				command.ExpectedClosureRevisionID = domain.ArenaWaveRevisionID(task041ID(902))
			},
		},
		{
			name: "old Wave reopened",
			mutate: func(authority *arena.ReplayReserveExhaustionAuthority, _ *arena.ReplayReserveExhaustionCommand) {
				authority.OldWaveClosure.Wave.State = domain.ArenaWaveStateActive
			},
		},
		{
			name: "Series already left replay required",
			mutate: func(authority *arena.ReplayReserveExhaustionAuthority, _ *arena.ReplayReserveExhaustionCommand) {
				authority.FailedAttempt.Series.Series.State = domain.ArenaSeriesStateTechnicalPause
				resume := domain.ArenaSeriesStateReplayRequired
				authority.FailedAttempt.Series.ResumeState = &resume
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			authority, command := task041ReplayExhaustionFixture(t)
			test.mutate(&authority, &command)
			repository := &replayReserveExhaustionRepositoryFake{authority: authority}

			record, changed, err := arena.NewReplayReserveExhaustionUseCase(repository).
				Pause(t.Context(), command)
			require.Error(t, err)
			require.Nil(t, record)
			require.False(t, changed)
			require.Equal(t, 0, repository.writeCount())
		})
	}
}

func TestReplayReserveExhaustionConcurrentRetryCommitsOnePause(t *testing.T) {
	t.Parallel()

	authority, command := task041ReplayExhaustionFixture(t)
	repository := &replayReserveExhaustionRepositoryFake{authority: authority}
	results := make(chan struct {
		changed bool
		err     error
	}, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, changed, err := arena.NewReplayReserveExhaustionUseCase(repository).
				Pause(context.Background(), command)
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

type replayReserveExhaustionRepositoryFake struct {
	mu        sync.Mutex
	authority arena.ReplayReserveExhaustionAuthority
	current   *arena.ReplayReserveExhaustion
	writes    int
}

func (r *replayReserveExhaustionRepositoryFake) LoadReplayReserveExhaustionAuthority(
	_ context.Context,
	_ arena.ReplayReplacementScope,
) (arena.ReplayReserveExhaustionAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	authority := r.authority
	if r.current != nil {
		current := *r.current
		authority.Current = &current
	}
	return authority, nil
}

func (r *replayReserveExhaustionRepositoryFake) CommitReplayReserveExhaustion(
	_ context.Context,
	record arena.ReplayReserveExhaustion,
) (*arena.ReplayReserveExhaustion, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current != nil {
		return nil, false, domain.ErrConflict
	}
	committed := record
	r.current = &committed
	r.writes++
	return &committed, true, nil
}

func (r *replayReserveExhaustionRepositoryFake) writeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writes
}

func task041ReplayExhaustionFixture(
	t *testing.T,
) (arena.ReplayReserveExhaustionAuthority, arena.ReplayReserveExhaustionCommand) {
	t.Helper()

	now := time.Date(2026, 8, 30, 22, 0, 0, 0, time.UTC)
	replacement, _ := task040ReplayReplacementFixture(t, now, domain.ArenaAssignmentReserveCount)
	authority := arena.ReplayReserveExhaustionAuthority{
		Scope: replacement.Scope, Revision: 12,
		FailedAttempt: replacement.FailedAttempt, OldWaveClosure: replacement.OldWaveClosure,
		ReserveChain: replacement.ReserveChain,
	}
	command := arena.ReplayReserveExhaustionCommand{
		Scope: authority.Scope, CommandID: task041ID(1),
		ExpectedClosureRevisionID: authority.OldWaveClosure.Wave.RevisionID,
		ExpectedActiveSnapshotID:  authority.FailedAttempt.ActiveSnapshotID,
	}
	return authority, command
}

func task041ID(number int) uuid.UUID {
	return uuid.MustParse("41000000-0000-0000-0000-" + formatIDNumber(number))
}

func formatIDNumber(number int) string {
	const width = 12
	value := []byte("000000000000")
	for index := width - 1; index >= 0 && number > 0; index-- {
		value[index] = byte('0' + number%10)
		number /= 10
	}
	return string(value)
}

var _ arena.ReplayReserveExhaustionRepository = (*replayReserveExhaustionRepositoryFake)(nil)
