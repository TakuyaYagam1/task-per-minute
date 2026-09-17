package correction

import (
	"testing"

	"github.com/stretchr/testify/require"

	projectionpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestProjectionKindMappingUsesOnlyDomainCanonicalTopFour(t *testing.T) {
	t.Parallel()

	require.Equal(t, "top_four", projectionpostgres.ProjectionArtifactKind(domain.ArtifactKindTopFour))
	require.Equal(t, "top_four", correctionArtifactKindSQL(domain.ArtifactKindTopFour))

	for _, mapper := range []func(string) (domain.ArtifactKind, bool){
		correctionArtifactKind,
		correctionArtifactKindFromSQL,
	} {
		kind, valid := mapper("top_four")
		require.True(t, valid)
		require.Equal(t, domain.ArtifactKindTopFour, kind)

		_, valid = mapper("top4")
		require.False(t, valid)
	}
	require.False(t, domain.ArtifactKind("top4").IsValid())
}
