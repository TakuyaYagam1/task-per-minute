package progression

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

func TestTournamentProgressionReceiptMapsRecordedPlayoffTransition(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 7, 3, 0, 0, 0, time.UTC)
	row := sqlc.FindTournamentStageProgressionRow{
		CommandID:                   uuid.New(),
		TournamentID:                uuid.New(),
		RosterID:                    uuid.New(),
		Action:                      string(tournamentprogression.ActionStartPlayoffs),
		SourceProjectionRevisionID:  uuid.New(),
		SourceProjectionRevision:    5,
		SourceTournamentRevision:    7,
		SourceTournamentState:       string(domain.TournamentStateSwiss),
		ResultingTournamentRevision: 8,
		ResultingTournamentState:    string(domain.TournamentStatePlayoffs),
		Preset:                      string(domain.TournamentPresetV1),
		RosterSize:                  4,
		TournamentCreatedAt:         pgtype.Timestamptz{Time: now.Add(-time.Hour), Valid: true},
		TournamentUpdatedAt:         pgtype.Timestamptz{Time: now, Valid: true},
		TournamentStartedAt:         pgtype.Timestamptz{Time: now.Add(-30 * time.Minute), Valid: true},
		ExecutedAt:                  pgtype.Timestamptz{Time: now, Valid: true},
	}

	receipt, err := tournamentProgressionReceipt(row)

	require.NoError(t, err)
	require.Equal(t, row.CommandID, receipt.CommandID)
	require.Equal(t, row.TournamentID, receipt.Result.ID)
	require.Equal(t, row.RosterID, receipt.Result.RosterID)
	require.Equal(t, domain.TournamentStatePlayoffs, receipt.Result.State)
	require.Equal(t, int64(8), receipt.Result.Revision)
	require.Equal(t, now, receipt.Result.UpdatedAt)
}

func TestTournamentProgressionReceiptRejectsMismatchedRecordedTransition(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 7, 3, 0, 0, 0, time.UTC)
	_, err := tournamentProgressionReceipt(sqlc.FindTournamentStageProgressionRow{
		CommandID:                   uuid.New(),
		TournamentID:                uuid.New(),
		RosterID:                    uuid.New(),
		Action:                      string(tournamentprogression.ActionStartGolden),
		SourceProjectionRevisionID:  uuid.New(),
		SourceProjectionRevision:    5,
		SourceTournamentRevision:    2,
		SourceTournamentState:       string(domain.TournamentStateSwiss),
		ResultingTournamentRevision: 3,
		ResultingTournamentState:    string(domain.TournamentStatePlayoffs),
		Preset:                      string(domain.TournamentPresetV1),
		RosterSize:                  4,
		TournamentCreatedAt:         pgtype.Timestamptz{Time: now.Add(-time.Hour), Valid: true},
		TournamentUpdatedAt:         pgtype.Timestamptz{Time: now, Valid: true},
		TournamentStartedAt:         pgtype.Timestamptz{Time: now.Add(-30 * time.Minute), Valid: true},
		ExecutedAt:                  pgtype.Timestamptz{Time: now, Valid: true},
	})

	require.ErrorIs(t, err, domain.ErrConflict)
}
