package arena_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestImmutableTaskSnapshotKeepsCatalogContentAndEquivalentIsolatedInstances(t *testing.T) {
	t.Parallel()

	input := immutableTaskSnapshotFixture()
	built, err := arena.BuildImmutableTaskSnapshot(input)
	require.NoError(t, err)
	require.NoError(t, built.Validate())

	first, ok := built.InstanceFor(input.Eligibility.ParticipantIDs[0])
	require.True(t, ok)
	second, ok := built.InstanceFor(input.Eligibility.ParticipantIDs[1])
	require.True(t, ok)
	require.NotEqual(t, first.InstanceID, second.InstanceID)
	require.Equal(t, first.RuntimeProfile, second.RuntimeProfile)
	require.Equal(t, first.ContentDigest, second.ContentDigest)
	require.Equal(t, first.ValidationPolicy, second.ValidationPolicy)
	require.Equal(t, first.DeadlineSeconds, second.DeadlineSeconds)

	original := built.Snapshot()
	input.Eligibility.Candidate.Task.Title = "mutated catalog title"
	input.Eligibility.Candidate.Task.Hints[0] = "mutated catalog hint"
	read := built.Snapshot()
	require.Equal(t, original, read)

	read.Hints[0] = "mutated caller hint"
	require.Equal(t, original, built.Snapshot())
}

func TestImmutableTaskSnapshotRejectsIneligibleOrNonEquivalentInput(t *testing.T) {
	t.Parallel()

	t.Run("ineligible version", func(t *testing.T) {
		t.Parallel()

		input := immutableTaskSnapshotFixture()
		input.Eligibility.Candidate.Health.Healthy = false
		_, err := arena.BuildImmutableTaskSnapshot(input)
		require.ErrorIs(t, err, arena.ErrTaskIneligible)
	})

	t.Run("shared instance", func(t *testing.T) {
		t.Parallel()

		input := immutableTaskSnapshotFixture()
		input.Instances[1].InstanceID = input.Instances[0].InstanceID
		_, err := arena.BuildImmutableTaskSnapshot(input)
		require.ErrorIs(t, err, arena.ErrInvalidImmutableTaskSnapshot)
	})

	t.Run("different runtime profile", func(t *testing.T) {
		t.Parallel()

		input := immutableTaskSnapshotFixture()
		input.Instances[1].RuntimeProfile = "arena-isolated-v2"
		_, err := arena.BuildImmutableTaskSnapshot(input)
		require.ErrorIs(t, err, arena.ErrInvalidImmutableTaskSnapshot)
	})
}

func immutableTaskSnapshotFixture() arena.ImmutableTaskSnapshotInput {
	eligibility := taskEligibilityFixture()
	return arena.ImmutableTaskSnapshotInput{
		SnapshotID:  task034ID(10),
		Kind:        eligibility.Pool.Kind,
		Eligibility: eligibility,
		Instances: [2]arena.TaskInstanceInput{
			{
				ParticipantID:  eligibility.ParticipantIDs[0],
				InstanceID:     task034ID(11),
				RuntimeProfile: "arena-isolated-v1",
			},
			{
				ParticipantID:  eligibility.ParticipantIDs[1],
				InstanceID:     task034ID(12),
				RuntimeProfile: "arena-isolated-v1",
			},
		},
	}
}
