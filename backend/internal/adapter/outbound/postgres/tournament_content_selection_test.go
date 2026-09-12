package postgres

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestTournamentV1ContentBindingFromPoolsRequiresExactNormalAndGoldenPools(t *testing.T) {
	t.Parallel()

	publicationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	normalID := uuid.MustParse("20000000-0000-0000-0000-000000000002")
	goldenID := uuid.MustParse("30000000-0000-0000-0000-000000000003")
	validPool := func(id uuid.UUID, kind domain.AssignmentTaskKind) tournamentV1ContentPublicationPool {
		return tournamentV1ContentPublicationPool{
			publicationID: publicationID, publicationRevision: 7,
			publishedAt: time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC), publishedAtValid: true,
			poolRevisionID: id, kind: string(kind), poolRevision: 7,
		}
	}

	tests := []struct {
		name  string
		pools []tournamentV1ContentPublicationPool
	}{
		{name: "missing pool", pools: []tournamentV1ContentPublicationPool{validPool(normalID, domain.AssignmentTaskKindNormal)}},
		{name: "duplicate normal", pools: []tournamentV1ContentPublicationPool{
			validPool(normalID, domain.AssignmentTaskKindNormal), validPool(goldenID, domain.AssignmentTaskKindNormal),
		}},
		{name: "revision mismatch", pools: []tournamentV1ContentPublicationPool{
			validPool(normalID, domain.AssignmentTaskKindNormal), func() tournamentV1ContentPublicationPool {
				pool := validPool(goldenID, domain.AssignmentTaskKindGolden)
				pool.poolRevision = 6
				return pool
			}(),
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := tournamentV1ContentBindingFromPools(uuid.New(), uuid.New(), test.pools)
			require.ErrorIs(t, err, domain.ErrInvalidContentConfiguration)
		})
	}

	binding, err := tournamentV1ContentBindingFromPools(uuid.New(), uuid.New(), []tournamentV1ContentPublicationPool{
		validPool(goldenID, domain.AssignmentTaskKindGolden), validPool(normalID, domain.AssignmentTaskKindNormal),
	})
	require.NoError(t, err)
	require.Equal(t, publicationID, binding.publicationID)
	require.Equal(t, normalID, binding.normalPoolRevisionID)
	require.Equal(t, goldenID, binding.goldenPoolRevisionID)
}
