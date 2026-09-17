package canonical_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	projection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection/canonical"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

func TestBuildCanonicalSwissRoundsBuildsImmutablePairEvidence(t *testing.T) {
	t.Parallel()

	participants := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	roundID := uuid.New()
	revisionID := uuid.New()
	firstSeriesID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	firstResultRevisionID := uuid.MustParse("10000000-0000-0000-0000-000000000011")
	secondSeriesID := uuid.MustParse("10000000-0000-0000-0000-000000000002")
	secondResultRevisionID := uuid.MustParse("10000000-0000-0000-0000-000000000012")
	rows := []projection.CanonicalSwissPointLedgerEntry{
		canonicalLedgerSeriesRow(roundID, revisionID, 1, firstSeriesID, firstResultRevisionID, participants[0], participants[1], 1, 0, 1),
		canonicalLedgerSeriesRow(roundID, revisionID, 1, firstSeriesID, firstResultRevisionID, participants[1], participants[0], 0, 1, 2),
		canonicalLedgerSeriesRow(roundID, revisionID, 1, secondSeriesID, secondResultRevisionID, participants[2], participants[3], 1, 0, 3),
		canonicalLedgerSeriesRow(roundID, revisionID, 1, secondSeriesID, secondResultRevisionID, participants[3], participants[2], 0, 1, 4),
	}

	rounds, err := projection.BuildCanonicalSwissRounds(rows)
	require.NoError(t, err)
	require.Len(t, rounds, 1)
	require.Equal(t, roundID, rounds[0].RoundID)
	require.Equal(t, revisionID, rounds[0].RevisionID)
	require.Len(t, rounds[0].Series, 2)
	require.Equal(t, participants[0], *rounds[0].Series[0].WinnerID)
	require.Equal(t, swissusecase.SeriesResultPlayed, rounds[0].Series[0].Label)
}

func TestBuildCanonicalSwissRoundsRejectsPartialOfficialRound(t *testing.T) {
	t.Parallel()

	participantID := uuid.New()
	opponentID := uuid.New()
	_, err := projection.BuildCanonicalSwissRounds([]projection.CanonicalSwissPointLedgerEntry{
		canonicalLedgerSeriesRow(
			uuid.New(), uuid.New(), 1, uuid.New(), uuid.New(), participantID, opponentID, 1, 1, 1,
		),
	})
	require.ErrorIs(t, err, projection.ErrInvalidCanonicalMaterialization)
}

//nolint:unparam // The round number stays explicit so multi-round fixtures remain readable.
func canonicalLedgerSeriesRow(
	roundID, revisionID uuid.UUID,
	roundNumber int,
	seriesID, resultRevisionID, participantID, opponentID uuid.UUID,
	points int,
	effectiveSeconds int64,
	seed int,
) projection.CanonicalSwissPointLedgerEntry {
	accepted := time.Duration(effectiveSeconds) * time.Second
	return projection.CanonicalSwissPointLedgerEntry{
		RoundID: roundID, RoundRevisionID: revisionID, RoundNumber: roundNumber,
		SourceKind: swissusecase.PointSourceSeries, SourceSeriesID: seriesID,
		SeriesResultRevisionID: resultRevisionID, ResultLabel: swissusecase.SeriesResultPlayed,
		ParticipantID: participantID, OpponentID: &opponentID, Points: points,
		EffectiveTime:     time.Duration(effectiveSeconds) * time.Second,
		AcceptedSolveTime: &accepted, StableSeed: seed,
	}
}
