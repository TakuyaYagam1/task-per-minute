package taskexec_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/taskexec"
)

func TestTaskExecSnapshotIsImmutable(t *testing.T) {
	t.Parallel()

	taskURL := "https://tasks.example.test/challenge"
	sourceURL := "https://tasks.example.test/source"
	task := validTask(taskURL, sourceURL)
	snapshot, err := taskexec.BuildSnapshot(taskexec.SnapshotInput{
		SnapshotID: uuid.New(), Version: 3, Kind: domain.AssignmentTaskKindNormal, Task: task,
	})
	require.NoError(t, err)
	digest, err := taskexec.SnapshotDigest(snapshot)
	require.NoError(t, err)

	task.Hints[0] = "changed"
	*task.TaskURL = "https://changed.example.test"
	*task.SourceFileURL = "https://changed.example.test/source"

	require.Equal(t, "first", snapshot.Hints[0])
	require.Equal(t, taskURL, *snapshot.TaskURL)
	require.Equal(t, sourceURL, *snapshot.SourceFileURL)
	after, err := taskexec.SnapshotDigest(snapshot)
	require.NoError(t, err)
	require.Equal(t, digest, after)
}

func TestTaskExecCloneTaskCopiesMutableFields(t *testing.T) {
	t.Parallel()

	task := validTask("https://tasks.example.test/challenge", "https://tasks.example.test/source")
	cloned := taskexec.CloneTask(&task)
	cloned.Hints[0] = "clone"
	*cloned.TaskURL = "https://clone.example.test"
	*cloned.SourceFileURL = "https://clone.example.test/source"

	require.Equal(t, "first", task.Hints[0])
	require.NotEqual(t, *cloned.TaskURL, *task.TaskURL)
	require.NotEqual(t, *cloned.SourceFileURL, *task.SourceFileURL)
	require.Nil(t, taskexec.CloneTask(nil))
}

func TestTaskExecFlagValidation(t *testing.T) {
	t.Parallel()

	snapshot, err := taskexec.BuildSnapshot(taskexec.SnapshotInput{
		SnapshotID: uuid.New(), Version: 1, Kind: domain.AssignmentTaskKindNormal,
		Task: validTask("https://tasks.example.test/challenge", "https://tasks.example.test/source"),
	})
	require.NoError(t, err)

	valid, err := taskexec.ValidateSnapshotFlag(taskexec.FlagValidationInput{
		ParticipantID: uuid.New(), Snapshot: snapshot, SubmittedFlag: snapshot.Flag,
	})
	require.NoError(t, err)
	require.True(t, valid)
	require.False(t, taskexec.MatchesFlag(snapshot.Flag, "FLAG{wrong}"))
}

func TestTaskExecSubmissionOrdering(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	firstID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	secondID := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	participantID := uuid.New()
	input := []taskexec.Submission{
		{ID: secondID, ParticipantID: participantID, ReceivedAt: now},
		{ID: uuid.New(), ParticipantID: participantID, ReceivedAt: now.Add(-time.Second)},
		{ID: firstID, ParticipantID: participantID, ReceivedAt: now},
	}

	ordered, err := taskexec.OrderSubmissions(input)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{input[1].ID, firstID, secondID}, []uuid.UUID{
		ordered[0].ID, ordered[1].ID, ordered[2].ID,
	})
	require.Equal(t, secondID, input[0].ID, "ordering must not mutate caller input")
	require.True(t, taskexec.PrecedesDeadline(now, now.Add(time.Second)))
	require.False(t, taskexec.PrecedesDeadline(now, now))
}

func validTask(taskURL, sourceURL string) domain.Task {
	return domain.Task{
		ID: uuid.New(), Title: "Task", Description: "Solve the task.", Category: domain.CategoryWeb,
		Difficulty: domain.DifficultyEasy, TimeLimit: 60, Flag: "FLAG{ok}",
		Hints: []string{"first", "second", "third"}, TaskURL: &taskURL, SourceFileURL: &sourceURL,
	}
}
