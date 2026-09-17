//go:build integration

package integration_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	leaderboardrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/leaderboard"
	leaderboardusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/leaderboard"
)

func TestLeaderboardRepo_TopStatsUsesCurrentTournamentSolve(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createResultAuditMigrationFixture(ctx, t)
	submissionID, _ := createAcceptedSubmission(ctx, t, fixture)
	createAtomicResultCommit(ctx, t, fixture, submissionID)

	var winnerPlayerID uuid.UUID
	var expectedMillis int64
	err := sharedPool.QueryRow(ctx, `
		SELECT participant.player_id,
			FLOOR(
				EXTRACT(EPOCH FROM submission.received_at - attempt.started_at) * 1000
			)::BIGINT
		FROM participants AS participant
		JOIN submission_events AS submission
			ON submission.participant_id = participant.id
			AND submission.id = $1
		JOIN game_attempts AS attempt ON attempt.id = submission.attempt_id
		WHERE participant.roster_id = $2`,
		submissionID,
		fixture.draft.rosterID,
	).Scan(&winnerPlayerID, &expectedMillis)
	require.NoError(t, err)

	repository := leaderboardrepo.NewLeaderboardPostgres(postgres.NewTxManager(sharedPool))
	rows, err := repository.TopStats(ctx, 50)
	require.NoError(t, err)

	winner, ok := leaderboardRow(rows, winnerPlayerID)
	require.True(t, ok)
	require.Equal(t, 1, winner.Wins)
	require.Equal(t, expectedMillis, winner.AverageSolveTimeMs)
}

func TestLeaderboardRepo_TopStatsIgnoresUnsettledSubmission(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createResultAuditMigrationFixture(ctx, t)
	_, _ = createAcceptedSubmission(ctx, t, fixture)
	var playerID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT player_id
		FROM participants
		WHERE roster_id = $1 AND id = $2`,
		fixture.draft.rosterID,
		fixture.draft.participantIDs[0],
	).Scan(&playerID))

	repository := leaderboardrepo.NewLeaderboardPostgres(postgres.NewTxManager(sharedPool))
	rows, err := repository.TopStats(ctx, 50)
	require.NoError(t, err)
	_, found := leaderboardRow(rows, playerID)
	require.False(t, found)
}

func leaderboardRow(rows []leaderboardusecase.PlayerStats, playerID uuid.UUID) (leaderboardusecase.PlayerStats, bool) {
	for _, row := range rows {
		if row.PlayerID == playerID {
			return row, true
		}
	}
	return leaderboardusecase.PlayerStats{}, false
}
