package postgres

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
)

func TestParticipantSubmissionAttemptFromGeneratedScopedRow(t *testing.T) {
	t.Parallel()

	scope := gamedomain.SubmissionScope{
		WaveID: uuid.New(), AssignmentID: uuid.New(),
		Game: gamedomain.Scope{
			TournamentID: uuid.New(), SeriesID: uuid.New(), SlotID: uuid.New(), GameID: uuid.New(),
		},
	}
	row := sqlc.GetGameAttemptScopedRow{
		ID: scope.Game.GameID, SlotID: scope.Game.SlotID, SeriesID: scope.Game.SeriesID, RosterID: uuid.New(),
	}

	attempt, err := participantSubmissionAttemptFromRow(scope, row)
	require.NoError(t, err)
	require.Equal(t, row.RosterID, attempt.RosterID)

	row.SeriesID = uuid.New()
	_, err = participantSubmissionAttemptFromRow(scope, row)
	require.ErrorIs(t, err, domain.ErrConflict)
}
