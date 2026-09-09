package postgres

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestIndexProgressionLogicalNodesAcceptsExactOrdinaryAndCorrectionLineage(t *testing.T) {
	t.Parallel()

	ordinarySource := uuid.New()
	correctionSource := uuid.New()
	correctionNode := uuid.New()
	nodes, err := indexProgressionLogicalNodes([]progressionLogicalNode{
		{
			Kind: domain.ArtifactKindGameResult, EntityID: uuid.New(), SourceID: ordinarySource,
			NodeID: ordinarySource, AuthoritySource: "result_commit",
		},
		{
			Kind: domain.ArtifactKindSeriesScore, EntityID: uuid.New(), SourceID: correctionSource,
			NodeID: correctionNode, AuthoritySource: "correction_commit", HasCorrectionBinding: true,
		},
	})

	require.NoError(t, err)
	require.Equal(t, ordinarySource, nodes[ordinarySource].NodeID)
	require.Equal(t, correctionNode, nodes[correctionSource].NodeID)
}

func TestIndexProgressionLogicalNodesRejectsUnboundOrRedirectedLineage(t *testing.T) {
	t.Parallel()

	for _, node := range []progressionLogicalNode{
		{
			Kind: domain.ArtifactKindSeriesResult, EntityID: uuid.New(), SourceID: uuid.New(),
			NodeID: uuid.New(), AuthoritySource: "result_commit",
		},
		{
			Kind: domain.ArtifactKindSeriesScore, EntityID: uuid.New(), SourceID: uuid.New(),
			NodeID: uuid.New(), AuthoritySource: "correction_commit",
		},
	} {
		_, err := indexProgressionLogicalNodes([]progressionLogicalNode{node})
		require.ErrorIs(t, err, domain.ErrConflict)
	}
}
