package runtime

import (
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
)

func TestGoldenRuntimePlanChainLengthUsesPublishedEdges(t *testing.T) {
	t.Parallel()

	for chainLength := 1; chainLength <= 4; chainLength++ {
		t.Run("chain_length_"+strconv.Itoa(chainLength), func(t *testing.T) {
			t.Parallel()

			groupRevisionID := uuid.New()
			otherGroupRevisionID := uuid.New()
			edges := make([]sqlc.LockTournamentProgressionGoldenExactPlanEdgesRow, 0, chainLength*2)
			for _, currentGroupRevisionID := range []uuid.UUID{otherGroupRevisionID, groupRevisionID} {
				for position := 1; position <= chainLength; position++ {
					edges = append(edges, sqlc.LockTournamentProgressionGoldenExactPlanEdgesRow{
						GroupRevisionID: currentGroupRevisionID, Position: int16(position),
					})
				}
			}

			got, err := goldenRuntimePlanChainLength(edges, groupRevisionID)
			require.NoError(t, err)
			require.Equal(t, chainLength, got)
		})
	}
}

func TestGoldenRuntimePlanChainLengthRejectsBrokenPublishedEdges(t *testing.T) {
	t.Parallel()

	groupRevisionID := uuid.New()
	edges := []sqlc.LockTournamentProgressionGoldenExactPlanEdgesRow{
		{GroupRevisionID: groupRevisionID, Position: 1},
		{GroupRevisionID: groupRevisionID, Position: 3},
	}

	_, err := goldenRuntimePlanChainLength(edges, groupRevisionID)
	require.Error(t, err)
}
