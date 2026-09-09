package postgres

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	swiss "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

func TestProgressionReceiptPointsSelectExactSeries(t *testing.T) {
	receiptID, roundID, first, second := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	evidence := sqlc.LockTournamentProgressionFinalSwissReceiptSeriesEvidenceRow{
		RoundID: roundID, SeriesID: uuid.New(), SeriesResultRevisionID: uuid.New(),
		FirstParticipantID: first, SecondParticipantID: second, SeriesWinnerID: nullableUUIDValue(first),
	}
	label := string(swiss.SeriesResultPlayed)
	ledger := make([]sqlc.LockTournamentProgressionFinalSwissReceiptLedgerRow, 0, 3)
	ledger = append(ledger,
		sqlc.LockTournamentProgressionFinalSwissReceiptLedgerRow{ProjectionRevisionID: receiptID, RoundID: roundID, RoundNumber: 1, SourceKind: string(swiss.PointSourceSeries),
			SourceSeriesID: nullableUUIDValue(evidence.SeriesID), SeriesResultRevisionID: nullableUUIDValue(evidence.SeriesResultRevisionID),
			ResultLabel: &label, ParticipantID: first, OpponentID: nullableUUIDValue(second), Points: swiss.SeriesWinPoints, StableSeed: 1},
		sqlc.LockTournamentProgressionFinalSwissReceiptLedgerRow{ProjectionRevisionID: receiptID, RoundID: roundID, RoundNumber: 1, SourceKind: string(swiss.PointSourceSeries),
			SourceSeriesID: nullableUUIDValue(evidence.SeriesID), SeriesResultRevisionID: nullableUUIDValue(evidence.SeriesResultRevisionID),
			ResultLabel: &label, ParticipantID: second, OpponentID: nullableUUIDValue(first), StableSeed: 2},
	)
	other := ledger[0]
	other.SourceSeriesID = nullableUUIDValue(uuid.New())
	ledger = append(ledger, other)
	result, err := progressionReceiptSeriesPoints(receiptID, evidence, ledger)
	require.NoError(t, err)
	require.Equal(t, evidence.SeriesID, result.SeriesID)
	ledger[0].SeriesResultRevisionID = nullableUUIDValue(uuid.New())
	_, err = progressionReceiptSeriesPoints(receiptID, evidence, ledger)
	require.ErrorIs(t, err, domain.ErrConflict)
}
