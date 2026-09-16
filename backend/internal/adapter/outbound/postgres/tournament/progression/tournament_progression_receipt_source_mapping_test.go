package progression

import (
	"crypto/sha256"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestIndexProgressionReceiptSourcesRetainsOlderReceiptAfterCorrection(t *testing.T) {
	t.Parallel()

	olderReceiptID := uuid.New()
	newerReceiptID := uuid.New()
	seriesID := uuid.New()
	older := progressionReceiptSourceRow(olderReceiptID, seriesID, 4, "superseded")
	newer := progressionReceiptSourceRow(newerReceiptID, seriesID, 9, "published")
	newer.ResultRevisionID = uuid.New()
	newer.SourceProjectionRevisionID = uuid.New()
	newer.PhysicalProjectionRevisionID = newer.SourceProjectionRevisionID

	indexed, err := indexProgressionReceiptSources([]sqlc.LockTournamentProgressionFinalSwissReceiptSourceProjectionsRow{
		older, newer,
	})

	require.NoError(t, err)
	olderKey := progressionReceiptSourceKey{
		receiptProjectionRevisionID: olderReceiptID,
		resultKind:                  domain.ArtifactKindSeriesResult,
		entityID:                    seriesID,
		resultRevisionID:            older.ResultRevisionID,
	}
	stored, found := indexed[olderKey]
	require.True(t, found)
	require.EqualValues(t, 4, stored.physicalProjectionRevision)
	require.Equal(t, "superseded", stored.state)
	require.Equal(t, older.ArtifactID, stored.artifacts[domain.ArtifactKindStandings].id)
}

func TestIndexProgressionReceiptSourcesRejectsSplicedArtifactDigest(t *testing.T) {
	t.Parallel()

	row := progressionReceiptSourceRow(uuid.New(), uuid.New(), 4, "published")
	other := sha256.Sum256([]byte("spliced-artifact"))
	row.PayloadDigest = other[:]

	_, err := indexProgressionReceiptSources([]sqlc.LockTournamentProgressionFinalSwissReceiptSourceProjectionsRow{row})

	require.ErrorIs(t, err, domain.ErrConflict)
}

func progressionReceiptSourceRow(
	receiptID, entityID uuid.UUID,
	physicalRevision int64,
	state string,
) sqlc.LockTournamentProgressionFinalSwissReceiptSourceProjectionsRow {
	payload := []byte("exact-source-artifact")
	digest := sha256.Sum256(payload)
	physicalID := uuid.New()
	return sqlc.LockTournamentProgressionFinalSwissReceiptSourceProjectionsRow{
		ReceiptProjectionRevisionID:  receiptID,
		ResultKind:                   string(domain.ArtifactKindSeriesResult),
		EntityID:                     entityID,
		ResultRevisionID:             uuid.New(),
		SourceProjectionRevisionID:   physicalID,
		SourceProjectionRevision:     physicalRevision,
		PhysicalProjectionRevisionID: physicalID,
		PhysicalProjectionRevision:   physicalRevision,
		PhysicalProjectionState:      state,
		PhysicalProjectionCreatedAt: pgtype.Timestamptz{
			Time: time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC), Valid: true,
		},
		ArtifactID: uuid.New(), ArtifactKind: string(domain.ArtifactKindStandings),
		Payload: payload, PayloadDigest: digest[:],
		ArtifactCreatedAt: pgtype.Timestamptz{
			Time: time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC), Valid: true,
		},
	}
}
