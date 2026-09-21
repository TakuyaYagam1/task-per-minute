package top4

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

func TestFinalSwissProjectionCarriesSwissRecordCounters(t *testing.T) {
	participantID := uuid.New()
	normal := []swissusecase.NormalStanding{{
		ParticipantID: participantID,
		Position:      1,
		Points:        3,
		Wins:          2,
		Losses:        1,
		ByeCount:      1,
		PointsLabel:   swissusecase.PointsFinal,
		Seed:          7,
	}}

	final := finalSwissStandings(normal)
	require.Len(t, final, 1)
	require.Equal(t, 2, final[0].Wins)
	require.Equal(t, 1, final[0].Losses)
	require.Equal(t, 1, final[0].ByeCount)

	// Golden consumes the Swiss standings as an ordering source. Converting
	// back must retain the counters and never recalculate them from Golden.
	roundTrip := finalSwissNormalStandings(final)
	require.Equal(t, 2, roundTrip[0].Wins)
	require.Equal(t, 1, roundTrip[0].Losses)
	require.Equal(t, 1, roundTrip[0].ByeCount)
}
