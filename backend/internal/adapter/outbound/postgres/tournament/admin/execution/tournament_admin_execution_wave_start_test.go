package execution

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamestart "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/start"
)

func TestWaveStartAuthorityDoesNotSynthesizeGlobalPlanRevision(t *testing.T) {
	t.Parallel()

	header, scope := waveStartAuthorityRow()
	authority, rosterID, err := waveStartAuthorityHeader(header, scope)
	require.NoError(t, err)
	require.Equal(t, header.RosterID, rosterID)
	require.Equal(t, header.Revision, authority.WaveRevision)
}

func TestWaveStartGameAuthorityAllowsDistinctPerGamePlans(t *testing.T) {
	t.Parallel()

	header, scope := waveStartAuthorityRow()
	authority, rosterID, err := waveStartAuthorityHeader(header, scope)
	require.NoError(t, err)
	require.NoError(t, applyWaveStartReadiness(&authority, []sqlc.LockWaveStartReadinessRow{
		{ParticipantID: tournamentExecutionID(101), Ready: true, ReadinessRevision: 2},
		{ParticipantID: tournamentExecutionID(102), Ready: true, ReadinessRevision: 2},
	}))
	row := waveStartGameRow(authority, rosterID)
	_, err = waveStartGameAuthorities(authority, rosterID, []sqlc.LockWaveStartGamesRow{row})
	require.NoError(t, err)

	row.PlanRevisionID = tournamentExecutionID(199)
	_, err = waveStartGameAuthorities(authority, rosterID, []sqlc.LockWaveStartGamesRow{row})
	require.NoError(t, err)
}

func TestWaveStartAuthorityAllowsOnlySwissAndPlayoffs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		state      domain.TournamentState
		wantReject bool
	}{
		{name: "swiss", state: domain.TournamentStateSwiss},
		{name: "playoffs", state: domain.TournamentStatePlayoffs},
		{name: "golden", state: domain.TournamentStateGolden, wantReject: true},
		{name: "completed", state: domain.TournamentStateCompleted, wantReject: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			header, scope := waveStartAuthorityRow()
			header.TournamentState = string(tt.state)
			_, _, err := waveStartAuthorityHeader(header, scope)
			if tt.wantReject {
				require.ErrorIs(t, err, gamestart.ErrWaveStartAuthorityConflict)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestWaveStartGameAuthorityAcceptsOneSeriesPlayoff(t *testing.T) {
	t.Parallel()

	header, scope := waveStartAuthorityRow()
	header.TournamentState = string(domain.TournamentStatePlayoffs)
	authority, rosterID, err := waveStartAuthorityHeader(header, scope)
	require.NoError(t, err)
	require.NoError(t, applyWaveStartReadiness(&authority, []sqlc.LockWaveStartReadinessRow{
		{ParticipantID: tournamentExecutionID(101), Ready: true, ReadinessRevision: 2},
		{ParticipantID: tournamentExecutionID(102), Ready: true, ReadinessRevision: 2},
	}))

	_, err = waveStartGameAuthorities(authority, rosterID, []sqlc.LockWaveStartGamesRow{waveStartGameRow(authority, rosterID)})
	require.NoError(t, err)
}

func TestWaveStartGameAuthorityNormalizesLaterBO3Slot(t *testing.T) {
	t.Parallel()

	header, scope := waveStartAuthorityRow()
	header.TournamentState = string(domain.TournamentStatePlayoffs)
	authority, rosterID, err := waveStartAuthorityHeader(header, scope)
	require.NoError(t, err)
	require.NoError(t, applyWaveStartReadiness(&authority, []sqlc.LockWaveStartReadinessRow{
		{ParticipantID: tournamentExecutionID(101), Ready: true, ReadinessRevision: 2},
		{ParticipantID: tournamentExecutionID(102), Ready: true, ReadinessRevision: 2},
	}))
	row := waveStartGameRow(authority, rosterID)
	row.SeriesFormat = string(domain.SeriesFormatBO3)
	row.SeriesState = string(domain.SeriesStateActive)
	row.FirstParticipantWins = 1
	row.SlotNumber = 2
	row.FirstParticipantWinsBefore = 1

	games, err := waveStartGameAuthorities(authority, rosterID, []sqlc.LockWaveStartGamesRow{row})
	require.NoError(t, err)
	require.Len(t, games, 1)
	require.Equal(t, 1, games[0].Series.Series.Slots[0].Position)
	require.Equal(t, row.SlotID, games[0].Series.Series.Slots[0].ID)
}

func TestWaveStartSQLContractLocksCurrentCommittedPlan(t *testing.T) {
	t.Parallel()

	query, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "..", "..", "db", "queries", "tournament_admin_execution.sql"))
	require.NoError(t, err)
	contents := string(query)
	for _, fragment := range []string{
		"-- name: LockWaveStartAuthority :one",
		"-- name: LockWaveStartGames :many",
		"AND plan.state = 'committed'",
		"plan.active_branch_id = assignment.branch_id",
		"plan.active_draft_branch_id = assignment_branch.exact_draft_branch_id",
		"-- name: StartWaveSeriesCAS :one",
		"-- name: StartWaveGameCAS :one",
		"-- name: DiscloseWaveStartReservationCAS :one",
	} {
		require.Contains(t, contents, fragment)
	}
}

func waveStartAuthorityRow() (sqlc.LockWaveStartAuthorityRow, gamestart.StartScope) {
	now := time.Date(2026, time.September, 6, 14, 0, 0, 0, time.UTC)
	scope := gamestart.StartScope{
		TournamentID: tournamentExecutionID(91), WaveID: tournamentExecutionID(92), WindowID: tournamentExecutionID(93),
	}
	return sqlc.LockWaveStartAuthorityRow{
		TournamentState: string(domain.TournamentStateSwiss), TournamentRevision: 3,
		RosterID: tournamentExecutionID(94), RosterRevision: 2,
		ProjectionRevisionID: tournamentExecutionID(95), ProjectionRevision: 4,
		ID: scope.WaveID, TournamentID: scope.TournamentID,
		RevisionID: tournamentExecutionID(96), Revision: 5, State: string(domain.WaveStateReady),
		CreatedAt: waveStartTimestamp(now.Add(-time.Hour)), UpdatedAt: waveStartTimestamp(now),
		ReadyWindowID: scope.WindowID, ReadyWindowRevisionID: tournamentExecutionID(97),
		ReadyWindowState: string(domain.ReadyWindowStateOpen),
		OpenedAt:         waveStartTimestamp(now.Add(-10 * time.Second)), Deadline: waveStartTimestamp(now.Add(20 * time.Second)),
		ArtifactRevisionID: tournamentExecutionID(99), ArtifactRevision: 4,
	}, scope
}

func waveStartGameRow(authority gamestart.StartAuthority, rosterID uuid.UUID) sqlc.LockWaveStartGamesRow {
	return sqlc.LockWaveStartGamesRow{
		SeriesID: tournamentExecutionID(110), TournamentID: authority.Scope.TournamentID, RosterID: rosterID,
		FirstParticipantID: tournamentExecutionID(101), SecondParticipantID: tournamentExecutionID(102),
		SeriesFormat: string(domain.SeriesFormatBO1), SeriesState: string(domain.SeriesStateReady), SeriesRevision: 2,
		SlotID: tournamentExecutionID(111), SlotNumber: 1, Category: string(domain.CategoryWeb),
		GameID: tournamentExecutionID(112), AttemptNumber: 1, GameState: string(domain.GameStatePlanned), GameRevision: 3,
		AssignmentID: tournamentExecutionID(113), AssignmentRevision: 4,
		PlanRevisionID: tournamentExecutionID(117),
		ReservationID:  tournamentExecutionID(114), ReservationRevision: 5, ReservationState: "committed",
		SnapshotID: tournamentExecutionID(115), TaskID: tournamentExecutionID(116), TaskVersion: 1,
		ContentDigest: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16,
			17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32},
		TimeLimit: 180,
	}
}

func waveStartTimestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}
