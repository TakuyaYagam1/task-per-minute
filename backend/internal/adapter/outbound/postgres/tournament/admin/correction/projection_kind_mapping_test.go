package correction

import (
	"testing"

	"github.com/stretchr/testify/require"

	projectionpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	correctionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction"
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

func TestCorrectionAuthorityCutoffErrorPreservesStableKind(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		code string
		kind correctionusecase.CutoffKind
	}{
		{code: "wave_started", kind: correctionusecase.CutoffWaveStarted},
		{code: "task_delivered", kind: correctionusecase.CutoffTaskDelivered},
		{code: "no_show_recorded", kind: correctionusecase.CutoffNoShowRecorded},
		{code: "forfeit_recorded", kind: correctionusecase.CutoffForfeitRecorded},
		{code: "golden_direct_allocated", kind: correctionusecase.CutoffGoldenAllocated},
	} {
		t.Run(test.code, func(t *testing.T) {
			t.Parallel()

			err := correctionAuthorityCutoffError(test.code)
			require.ErrorIs(t, err, correctionusecase.ErrCutoff)
			require.Equal(t, test.kind, correctionusecase.CutoffKindOf(err))
		})
	}

	require.ErrorIs(t, correctionAuthorityCutoffError("unknown"), domain.ErrInternal)
}
