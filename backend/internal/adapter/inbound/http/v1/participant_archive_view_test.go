package v1

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func TestParticipantTaskSnapshotSerializesAvailabilityWithoutInternalURL(t *testing.T) {
	t.Parallel()

	internalURL := "http://seaweed:8333/private/tasks/archive.zip"
	view := inbound.TaskSnapshotView{
		SnapshotID: uuid.New(), TaskID: uuid.New(), Version: 1,
		Kind: domain.AssignmentTaskKindNormal, Title: "Archive task", Description: "Inspect the archive",
		Category: domain.CategoryForensics, Difficulty: domain.DifficultyMedium,
		TimeLimit: 180, Hints: []string{}, SourceFileURL: &internalURL,
	}

	payload, err := participantTaskSnapshot(view)
	require.NoError(t, err)
	require.True(t, payload.SourceFileAvailable)

	encoded, err := json.Marshal(payload)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), internalURL)
	require.NotContains(t, string(encoded), "source_file_url")
	require.Contains(t, string(encoded), "source_file_available")
}

func TestGoldenParticipantTaskSerializesSourceAvailability(t *testing.T) {
	t.Parallel()

	taskURL := "https://golden.example/tasks/archive"
	view := inbound.GoldenParticipantView{
		TournamentID: uuid.New(), ParticipantID: uuid.New(), GroupID: uuid.New(), GroupRevisionID: uuid.New(),
		AttemptID: uuid.New(), State: "active", RuntimeRevision: 1, ReadyWindowID: uuid.New(),
		Task: &inbound.GoldenTaskView{
			AssignmentID: uuid.New(), SnapshotID: uuid.New(), TaskID: uuid.New(),
			Version: 2, Title: "Golden archive", Description: "Inspect the immutable archive",
			Category: "forensics", Difficulty: "medium", TimeLimitSeconds: 180, TaskURL: &taskURL,
		},
	}

	payload, err := goldenParticipantResponse(view, true)
	require.NoError(t, err)
	require.NotNil(t, payload.Task)
	require.True(t, payload.Task.SourceFileAvailable)
	require.Equal(t, int32(2), payload.Task.Version)
	require.Equal(t, "Inspect the immutable archive", payload.Task.Description)
	require.Equal(t, &taskURL, payload.Task.TaskUrl)

	encoded, err := json.Marshal(payload)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"source_file_available":true`)
	require.NotContains(t, string(encoded), "hints")
	require.NotContains(t, string(encoded), "flag")
	require.NotContains(t, string(encoded), "source_file_url")
	require.NotContains(t, string(encoded), "content_digest")
}
