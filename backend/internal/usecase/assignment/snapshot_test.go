package assignment_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
)

func TestImmutableTaskSnapshotKeepsCatalogContentAndEquivalentIsolatedInstances(t *testing.T) {
	t.Parallel()

	input := immutableTaskSnapshotFixture()
	built, err := assignmentusecase.BuildImmutableTaskSnapshot(input)
	require.NoError(t, err)
	require.NoError(t, built.Validate())

	first, ok := built.InstanceFor(input.Eligibility.ParticipantIDs[0])
	require.True(t, ok)
	second, ok := built.InstanceFor(input.Eligibility.ParticipantIDs[1])
	require.True(t, ok)
	require.True(t, built.HasParticipant(first.ParticipantID))
	require.True(t, built.HasParticipant(second.ParticipantID))
	require.False(t, built.HasParticipant(uuid.New()))
	require.NotEqual(t, first.InstanceID, second.InstanceID)
	require.Equal(t, domain.ParticipantTaskInstanceID(input.AssignmentID, first.ParticipantID), first.InstanceID)
	rebuilt, err := assignmentusecase.BuildImmutableTaskSnapshot(input)
	require.NoError(t, err)
	require.Equal(t, built.Instances(), rebuilt.Instances())
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
		_, err := assignmentusecase.BuildImmutableTaskSnapshot(input)
		require.ErrorIs(t, err, assignmentusecase.ErrTaskIneligible)
	})

	t.Run("missing assignment identity", func(t *testing.T) {
		t.Parallel()

		input := immutableTaskSnapshotFixture()
		input.AssignmentID = uuid.Nil
		_, err := assignmentusecase.BuildImmutableTaskSnapshot(input)
		require.ErrorIs(t, err, assignmentusecase.ErrInvalidImmutableTaskSnapshot)
	})

	t.Run("different runtime profile", func(t *testing.T) {
		t.Parallel()

		input := immutableTaskSnapshotFixture()
		input.Instances[1].RuntimeProfile = "isolated-v2"
		_, err := assignmentusecase.BuildImmutableTaskSnapshot(input)
		require.ErrorIs(t, err, assignmentusecase.ErrInvalidImmutableTaskSnapshot)
	})
}

func immutableTaskSnapshotFixture() assignmentusecase.ImmutableTaskSnapshotInput {
	eligibility := taskEligibilityFixture()
	return assignmentusecase.ImmutableTaskSnapshotInput{
		AssignmentID: task034ID(9),
		SnapshotID:   task034ID(10),
		Kind:         eligibility.Pool.Kind,
		Eligibility:  eligibility,
		Instances: [2]assignmentusecase.TaskInstanceInput{
			{
				ParticipantID:  eligibility.ParticipantIDs[0],
				RuntimeProfile: "isolated-v1",
			},
			{
				ParticipantID:  eligibility.ParticipantIDs[1],
				RuntimeProfile: "isolated-v1",
			},
		},
	}
}
