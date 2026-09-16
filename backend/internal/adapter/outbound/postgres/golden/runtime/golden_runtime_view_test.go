package runtime

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestGoldenParticipantRuntimeViewDisclosesSnapshotContentOnlyAfterStartAndParticipation(t *testing.T) {
	t.Parallel()

	playerID := uuid.New()
	startedAt := time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC)
	row := goldenRuntimeViewRowForTest(playerID)
	row.StartedAt = pgtype.Timestamptz{Time: startedAt, Valid: true}
	row.Deadline = pgtype.Timestamptz{Time: startedAt.Add(180 * time.Second), Valid: true}
	row.ParticipantEligible = true

	view, err := goldenParticipantRuntimeView(playerID, []sqlc.ListGoldenRuntimeViewRow{row})
	require.NoError(t, err)
	require.NotNil(t, view.Task)
	require.Equal(t, row.TaskVersion, int32(view.Task.Version))
	require.Equal(t, row.Description, view.Task.Description)
	require.Equal(t, row.TaskUrl, view.Task.TaskURL)
	require.Equal(t, row.SourceFileAvailable, view.Task.SourceFileAvailable)

	row.StartedAt = pgtype.Timestamptz{}
	view, err = goldenParticipantRuntimeView(playerID, []sqlc.ListGoldenRuntimeViewRow{row})
	require.NoError(t, err)
	require.Nil(t, view.Task)

	row.StartedAt = pgtype.Timestamptz{Time: startedAt, Valid: true}
	row.ParticipantEligible = false
	view, err = goldenParticipantRuntimeView(playerID, []sqlc.ListGoldenRuntimeViewRow{row})
	require.NoError(t, err)
	require.Nil(t, view.Task)
}

func TestGoldenParticipantRuntimeViewDeniesUnknownPlayer(t *testing.T) {
	t.Parallel()

	playerID := uuid.New()
	_, err := goldenParticipantRuntimeView(playerID, []sqlc.ListGoldenRuntimeViewRow{
		goldenRuntimeViewRowForTest(uuid.New()),
	})
	require.ErrorIs(t, err, domain.ErrTournamentNotFound)
}

func goldenRuntimeViewRowForTest(playerID uuid.UUID) sqlc.ListGoldenRuntimeViewRow {
	taskURL := "http://task.example.test:8080/challenge"
	return sqlc.ListGoldenRuntimeViewRow{
		TournamentID: uuid.New(), RosterID: uuid.New(), GroupID: uuid.New(),
		GroupRevisionID: uuid.New(), AttemptID: uuid.New(), State: "prepared",
		ReadyWindowID: uuid.New(), MembershipID: uuid.New(), ParticipantID: uuid.New(),
		PlayerID: playerID, AssignmentID: uuid.New(), SnapshotID: uuid.New(), TaskID: uuid.New(),
		TaskVersion: 3, Title: "Golden task", Description: "Immutable statement",
		Category: "web", Difficulty: "easy", TimeLimit: 180, TaskUrl: &taskURL,
		SourceFileAvailable: true,
	}
}
