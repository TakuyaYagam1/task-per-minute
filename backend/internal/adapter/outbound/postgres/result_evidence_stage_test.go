package postgres

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestStageScoreGenesisRequiresExactSingleNode(t *testing.T) {
	t.Parallel()
	scope := ResultScope{TournamentID: uuid.New(), RosterID: uuid.New(), SeriesID: uuid.New()}
	revisionID := uuid.New()
	node := sqlc.LockStageScoreGenesisNodesRow{ID: uuid.New(), TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		EntityID: scope.SeriesID, RevisionNumber: 1, ScoreRevisionID: revisionID}
	id, err := stageScoreGenesisNode(scope, revisionID, []sqlc.LockStageScoreGenesisNodesRow{node})
	require.NoError(t, err)
	require.Equal(t, node.ID, id)
	for _, field := range []string{"tournament", "roster", "Series", "revision", "ordinal", "multiple"} {
		t.Run(field, func(t *testing.T) {
			wrong := node
			switch field {
			case "tournament":
				wrong.TournamentID = uuid.New()
			case "roster":
				wrong.RosterID = uuid.New()
			case "Series":
				wrong.EntityID = uuid.New()
			case "revision":
				wrong.ScoreRevisionID = uuid.New()
			case "ordinal":
				wrong.RevisionNumber = 2
			}
			rows := []sqlc.LockStageScoreGenesisNodesRow{wrong}
			if field == "multiple" {
				rows = append(rows, node)
			}
			_, err := stageScoreGenesisNode(scope, revisionID, rows)
			require.ErrorIs(t, err, domain.ErrConflict)
		})
	}
}
