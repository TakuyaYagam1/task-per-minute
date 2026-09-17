package progression

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
)

func TestProgressionSwissCountsUsesExactTerminalHeadsBeforeWaveClose(t *testing.T) {
	roundID, waveID, revisionID := uuid.New(), uuid.New(), uuid.New()
	lockRevision := int64(1)
	rounds := []sqlc.LockTournamentProgressionSwissRoundsRow{{ID: roundID, RoundNumber: 1, Revision: 1, LockRevision: &lockRevision,
		LockedAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}, WaveID: waveID, WaveRevisionID: revisionID, WaveRevision: 2, WaveState: "active"}}
	proofs := []sqlc.SwissRoundLockProof{{RoundID: roundID, WaveID: waveID, ProofMode: "wave_start"}}
	series := make([]sqlc.LockTournamentProgressionSwissSeriesRow, 2)
	for index := range series {
		resultID := nullableUUIDValue(uuid.New())
		series[index] = sqlc.LockTournamentProgressionSwissSeriesRow{RoundID: roundID, SeriesID: uuid.New(),
			FirstParticipantID: uuid.New(), SecondParticipantID: uuid.New(), Format: "bo1", State: "completed",
			CurrentScoreRevisionID: nullableUUIDValue(uuid.New()), CurrentResultRevisionID: resultID, ResultRevisionID: resultID}
	}
	_, terminalRounds, _, terminalSeries, err := progressionSwissCounts(rounds, series, proofs)
	require.NoError(t, err)
	require.Equal(t, 1, terminalRounds)
	require.Equal(t, 2, terminalSeries)
	_, terminalRounds, _, _, err = progressionSwissCounts(rounds, series, nil)
	require.NoError(t, err)
	require.Zero(t, terminalRounds, "an active Wave needs its retained proof")
	wrongProof := proofs[0]
	wrongProof.WaveID = uuid.New()
	_, _, _, _, err = progressionSwissCounts(rounds, series, []sqlc.SwissRoundLockProof{wrongProof})
	require.Error(t, err)
	series[1].State = "active"
	_, terminalRounds, _, terminalSeries, err = progressionSwissCounts(rounds, series, proofs)
	require.NoError(t, err)
	require.Zero(t, terminalRounds)
	require.Equal(t, 1, terminalSeries)
	series[1].State = "completed"
	series[1].ResultRevisionID = nullableUUIDValue(uuid.New())
	_, _, _, _, err = progressionSwissCounts(rounds, series, proofs)
	require.Error(t, err)
}
