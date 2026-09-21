package replay_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	replayusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/replay"
)

func TestReplayReserveExhaustionUsesActualChainLength(t *testing.T) {
	t.Parallel()

	for chainLength := 1; chainLength <= 4; chainLength++ {
		t.Run("chain_length_"+strconv.Itoa(chainLength), func(t *testing.T) {
			t.Parallel()

			authority, command := replayExhaustionFixtureForChain(t, chainLength)
			repository := newReplayExhaustionRepositoryHarness(t, authority)

			record, changed, err := replayusecase.NewReplayReserveExhaustionUseCase(repository).
				Pause(t.Context(), command)
			require.NoError(t, err)
			require.True(t, changed)
			require.NoError(t, record.Validate())
			require.Equal(t, chainLength, record.ReservePosition)
			require.Equal(t, domain.SeriesStateTechnicalPause, record.Series.Series.State)
		})
	}
}

func TestReplayReplacementUsesActualChainLength(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name        string
		chainLength int
		activeIndex int
	}{
		{name: "one_reserve", chainLength: 2, activeIndex: 0},
		{name: "first_of_two_reserves", chainLength: 3, activeIndex: 0},
		{name: "second_of_two_reserves", chainLength: 3, activeIndex: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			now := time.Date(2026, 8, 31, 0, 0, test.activeIndex, 0, time.UTC)
			authority, command := replayReplacementFixture(t, now, test.activeIndex)
			authority.ReserveChain.Snapshots = authority.ReserveChain.Snapshots[:test.chainLength]
			repository := newReplayReplacementRepositoryHarness(t, authority)

			replacement, changed, err := replayusecase.NewReplayReplacementUseCase(
				repository,
				newReplayFixedClock(t, now),
			).Replace(t.Context(), command)
			require.NoError(t, err)
			require.True(t, changed)
			require.Equal(t, test.activeIndex+2, replacement.ReservePosition)
			require.Equal(t, authority.ReserveChain.Snapshots[test.activeIndex+1].SnapshotID,
				replacement.Snapshot.SnapshotID)
		})
	}
}

func TestReplayReplacementAcceptsOperatorAppendAfterEveryOriginalChainLength(t *testing.T) {
	t.Parallel()

	for originalLength := 1; originalLength <= 3; originalLength++ {
		t.Run("original_length_"+strconv.Itoa(originalLength), func(t *testing.T) {
			t.Parallel()

			now := time.Date(2026, 8, 31, 1, 0, originalLength, 0, time.UTC)
			authority, command := replayReplacementFixture(t, now, originalLength-1)
			authority.ReserveChain.Snapshots = authority.ReserveChain.Snapshots[:originalLength]
			authority.ReserveChain.Snapshots = append(
				authority.ReserveChain.Snapshots,
				replaySnapshot(t, 20+originalLength),
			)
			repository := newReplayReplacementRepositoryHarness(t, authority)

			replacement, changed, err := replayusecase.NewReplayReplacementUseCase(
				repository,
				newReplayFixedClock(t, now),
			).Replace(t.Context(), command)
			require.NoError(t, err)
			require.True(t, changed)
			require.Equal(t, originalLength+1, replacement.ReservePosition)
			require.Equal(t, authority.ReserveChain.Snapshots[originalLength].SnapshotID,
				replacement.Snapshot.SnapshotID)
		})
	}
}

func replayExhaustionFixtureForChain(
	t *testing.T,
	chainLength int,
) (replayusecase.ReplayReserveExhaustionAuthority, replayusecase.ReplayReserveExhaustionCommand) {
	t.Helper()

	now := time.Date(2026, 8, 31, 2, 0, chainLength, 0, time.UTC)
	originalLength := chainLength
	if originalLength > 3 {
		originalLength = 3
	}
	replacement, _ := replayReplacementFixture(t, now, originalLength-1)
	replacement.ReserveChain.Snapshots = replacement.ReserveChain.Snapshots[:originalLength]
	if chainLength > originalLength {
		replacement.ReserveChain.Snapshots = append(
			replacement.ReserveChain.Snapshots,
			replaySnapshot(t, 30+chainLength),
		)
		replacement.ReserveChain.ActiveIndex = chainLength - 1
		replacement.FailedAttempt.ActiveSnapshotID =
			replacement.ReserveChain.Snapshots[chainLength-1].SnapshotID
	}
	authority := replayusecase.ReplayReserveExhaustionAuthority{
		Scope:          replacement.Scope,
		Revision:       12,
		FailedAttempt:  replacement.FailedAttempt,
		OldWaveClosure: replacement.OldWaveClosure,
		ReserveChain:   replacement.ReserveChain,
	}
	command := replayusecase.ReplayReserveExhaustionCommand{
		Scope:                     authority.Scope,
		CommandID:                 replayExhaustionID(100 + chainLength),
		ExpectedClosureRevisionID: authority.OldWaveClosure.Wave.RevisionID,
		ExpectedActiveSnapshotID:  authority.FailedAttempt.ActiveSnapshotID,
	}
	return authority, command
}
