package admin

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestCorrectionCurrentProjectionRevisionsKeepsOnlyExactHead(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.New()
	artifact := domain.ArtifactRef{Kind: domain.ArtifactKindBracket, EntityID: tournamentID}
	createdAt := time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)
	first, err := domain.NewProjectionRevision(
		domain.DerivedRevisionID(uuid.New()), tournamentID, artifact, 1, nil, createdAt,
		[]byte(`{"rounds":["first"]}`),
	)
	require.NoError(t, err)
	previous := first.Revision().ID()
	second, err := domain.NewProjectionRevision(
		domain.DerivedRevisionID(uuid.New()), tournamentID, artifact, 2, &previous, createdAt.Add(time.Second),
		[]byte(`{"rounds":["second"]}`),
	)
	require.NoError(t, err)

	current := correctionCurrentProjectionRevisions([]domain.ProjectionRevision{first, second})
	require.Equal(t, second.Revision(), current[artifact])
}
