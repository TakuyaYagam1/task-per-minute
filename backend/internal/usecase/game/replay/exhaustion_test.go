package replay_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/mocks"
	replayusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/replay"
)

func TestReplayReserveExhaustionPausesAfterTwoSameCategoryReserves(t *testing.T) {
	t.Parallel()

	authority, command := replayExhaustionFixture(t)
	repository := newReplayExhaustionRepositoryHarness(t, authority)

	record, changed, err := replayusecase.NewReplayReserveExhaustionUseCase(repository).
		Pause(t.Context(), command)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, record.Validate())
	require.Equal(t, domain.SeriesStateTechnicalPause, record.Series.Series.State)
	require.Equal(t, domain.SeriesStateReplayRequired, *record.Series.ResumeState)
	require.Equal(t, authority.FailedAttempt.Series.Series.Score, record.Series.Series.Score)
	require.Equal(t, authority.FailedAttempt.Failure.CategoryCutoff, record.Category)
	require.Equal(t, domain.AssignmentReserveCount+1, record.ReservePosition)
	require.Nil(t, record.Series.Series.WinnerID)
	require.Equal(t, domain.WaveStateCompleted, record.OldWave.State)
	require.Equal(t, authority.OldWaveClosure.Wave.ID, record.OldWave.ID)
	require.Equal(t, authority.OldWaveClosure.Wave.RevisionID, record.OldWave.RevisionID)
	require.Len(t, record.Series.Series.Slots, len(authority.FailedAttempt.Series.Series.Slots))
	require.Equal(t, 1, repository.writeCount())
}

func TestReplayReserveExhaustionRejectsIncompleteOrStaleEvidence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*replayusecase.ReplayReserveExhaustionAuthority, *replayusecase.ReplayReserveExhaustionCommand)
	}{
		{
			name: "one planned reserve remains",
			mutate: func(
				authority *replayusecase.ReplayReserveExhaustionAuthority,
				_ *replayusecase.ReplayReserveExhaustionCommand,
			) {
				authority.ReserveChain.ActiveIndex--
				authority.FailedAttempt.ActiveSnapshotID =
					authority.ReserveChain.Snapshots[authority.ReserveChain.ActiveIndex].SnapshotID
			},
		},
		{
			name: "active snapshot changed",
			mutate: func(
				_ *replayusecase.ReplayReserveExhaustionAuthority,
				command *replayusecase.ReplayReserveExhaustionCommand,
			) {
				command.ExpectedActiveSnapshotID = replayExhaustionID(901)
			},
		},
		{
			name: "old Wave revision changed",
			mutate: func(
				_ *replayusecase.ReplayReserveExhaustionAuthority,
				command *replayusecase.ReplayReserveExhaustionCommand,
			) {
				command.ExpectedClosureRevisionID = domain.WaveRevisionID(replayExhaustionID(902))
			},
		},
		{
			name: "old Wave reopened",
			mutate: func(
				authority *replayusecase.ReplayReserveExhaustionAuthority,
				_ *replayusecase.ReplayReserveExhaustionCommand,
			) {
				authority.OldWaveClosure.Wave.State = domain.WaveStateActive
			},
		},
		{
			name: "Series already left replay required",
			mutate: func(
				authority *replayusecase.ReplayReserveExhaustionAuthority,
				_ *replayusecase.ReplayReserveExhaustionCommand,
			) {
				authority.FailedAttempt.Series.Series.State = domain.SeriesStateTechnicalPause
				resume := domain.SeriesStateReplayRequired
				authority.FailedAttempt.Series.ResumeState = &resume
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			authority, command := replayExhaustionFixture(t)
			test.mutate(&authority, &command)
			repository := newReplayExhaustionRepositoryHarness(t, authority)

			record, changed, err := replayusecase.NewReplayReserveExhaustionUseCase(repository).
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

	authority, command := replayExhaustionFixture(t)
	repository := newReplayExhaustionRepositoryHarness(t, authority)
	results := make(chan struct {
		changed bool
		err     error
	}, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, changed, err := replayusecase.NewReplayReserveExhaustionUseCase(repository).
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

type replayExhaustionRepositoryState struct {
	mu        sync.Mutex
	authority replayusecase.ReplayReserveExhaustionAuthority
	current   *replayusecase.ReplayReserveExhaustion
	writes    int
}

type replayExhaustionRepositoryHarness struct {
	*gamemocks.MockReplayReserveExhaustionRepository

	state *replayExhaustionRepositoryState
}

func newReplayExhaustionRepositoryHarness(
	t *testing.T,
	authority replayusecase.ReplayReserveExhaustionAuthority,
) *replayExhaustionRepositoryHarness {
	t.Helper()
	state := &replayExhaustionRepositoryState{authority: authority}
	repository := gamemocks.NewMockReplayReserveExhaustionRepository(t)
	repository.EXPECT().
		LoadReplayReserveExhaustionAuthority(mock.Anything, mock.Anything).
		RunAndReturn(state.loadAuthority).
		Maybe()
	repository.EXPECT().
		CommitReplayReserveExhaustion(mock.Anything, mock.Anything).
		RunAndReturn(state.commitExhaustion).
		Maybe()
	return &replayExhaustionRepositoryHarness{
		MockReplayReserveExhaustionRepository: repository,
		state:                                 state,
	}
}

func (s *replayExhaustionRepositoryState) loadAuthority(
	_ context.Context,
	_ replayusecase.ReplayReplacementScope,
) (replayusecase.ReplayReserveExhaustionAuthority, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	authority := s.authority
	if s.current != nil {
		current := *s.current
		authority.Current = &current
	}
	return authority, nil
}

func (s *replayExhaustionRepositoryState) commitExhaustion(
	_ context.Context,
	record replayusecase.ReplayReserveExhaustion,
) (*replayusecase.ReplayReserveExhaustion, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current != nil {
		return nil, false, domain.ErrConflict
	}
	committed := record
	s.current = &committed
	s.writes++
	return &committed, true, nil
}

func (h *replayExhaustionRepositoryHarness) writeCount() int {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	return h.state.writes
}

func replayExhaustionFixture(
	t *testing.T,
) (replayusecase.ReplayReserveExhaustionAuthority, replayusecase.ReplayReserveExhaustionCommand) {
	t.Helper()

	now := time.Date(2026, 8, 30, 22, 0, 0, 0, time.UTC)
	replacement, _ := replayReplacementFixture(t, now, domain.AssignmentReserveCount)
	authority := replayusecase.ReplayReserveExhaustionAuthority{
		Scope:          replacement.Scope,
		Revision:       12,
		FailedAttempt:  replacement.FailedAttempt,
		OldWaveClosure: replacement.OldWaveClosure,
		ReserveChain:   replacement.ReserveChain,
	}
	command := replayusecase.ReplayReserveExhaustionCommand{
		Scope:                     authority.Scope,
		CommandID:                 replayExhaustionID(1),
		ExpectedClosureRevisionID: authority.OldWaveClosure.Wave.RevisionID,
		ExpectedActiveSnapshotID:  authority.FailedAttempt.ActiveSnapshotID,
	}
	return authority, command
}

func replayExhaustionID(number int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("41000000-0000-0000-0000-%012d", number))
}
