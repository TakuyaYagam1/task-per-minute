package terminal

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestFinalPublicationIdentityIsDeterministicAndSourceBound(t *testing.T) {
	t.Parallel()

	resultID := domain.OfficialResultRevisionID(uuid.MustParse("e1a2748e-f5f9-45d2-9adb-8ea579d97ff3"))
	first, err := FinalPublicationIdentity(resultID)
	require.NoError(t, err)
	second, err := FinalPublicationIdentity(resultID)
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.True(t, first.Valid())

	changed, err := FinalPublicationIdentity(domain.OfficialResultRevisionID(uuid.New()))
	require.NoError(t, err)
	require.NotEqual(t, first.ChampionRevisionID, changed.ChampionRevisionID)
	require.NotEqual(t, first.ProjectionRevisionID, changed.ProjectionRevisionID)
	require.NotEqual(t, first.ChampionArtifactID, changed.ChampionArtifactID)

	_, err = FinalPublicationIdentity(domain.OfficialResultRevisionID{})
	require.ErrorIs(t, err, ErrInvalidTerminalStage)
}
