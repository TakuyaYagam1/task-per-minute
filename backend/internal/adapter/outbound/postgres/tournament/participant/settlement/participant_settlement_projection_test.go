package settlement

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	projection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection/canonical"
)

func TestParticipantSettlementFinalSwissWaveMaterializesFullLedger(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.New()
	roundID := uuid.New()
	roundRevisionID := uuid.New()
	firstSeriesID := uuid.New()
	secondSeriesID := uuid.New()
	firstResultRevisionID := uuid.New()
	finalResultRevisionID := uuid.New()
	firstParticipantID := uuid.New()
	secondParticipantID := uuid.New()
	thirdParticipantID := uuid.New()
	fourthParticipantID := uuid.New()

	ledger := append(
		participantSettlementSeriesLedgerEntries(
			roundID,
			roundRevisionID,
			firstSeriesID,
			firstResultRevisionID,
			firstParticipantID,
			secondParticipantID,
			1,
			2,
		),
		participantSettlementSeriesLedgerEntries(
			roundID,
			roundRevisionID,
			secondSeriesID,
			finalResultRevisionID,
			thirdParticipantID,
			fourthParticipantID,
			3,
			4,
		)...,
	)

	require.True(t, participantSettlementLedgerIncludes(ledger, finalResultRevisionID))
	input, err := participantSettlementMaterializationInput(
		tournamentID,
		[]sqlc.ListTournamentAdminCorrectionProjectionParticipantsRow{
			{ID: firstParticipantID, Seed: 1},
			{ID: secondParticipantID, Seed: 2},
			{ID: thirdParticipantID, Seed: 3},
			{ID: fourthParticipantID, Seed: 4},
		},
		ledger,
	)
	require.NoError(t, err)

	materialized, err := projection.BuildCanonicalMaterialization(input)
	require.NoError(t, err)
	require.Len(t, materialized.Artifacts, 1)
	require.Len(t, materialized.Artifacts[0].Members, 4)

	scoreByParticipant := make(map[uuid.UUID]int64, len(materialized.Artifacts[0].Members))
	for _, member := range materialized.Artifacts[0].Members {
		require.NotNil(t, member.ScoreMilli)
		scoreByParticipant[member.ParticipantID] = *member.ScoreMilli
	}
	require.Equal(t, int64(1000), scoreByParticipant[firstParticipantID])
	require.Equal(t, int64(0), scoreByParticipant[secondParticipantID])
	require.Equal(t, int64(1000), scoreByParticipant[thirdParticipantID])
	require.Equal(t, int64(0), scoreByParticipant[fourthParticipantID])

	dependencies, err := participantSettlementStandingsDependencies(uuid.New(), ledger)
	require.NoError(t, err)
	require.Len(t, dependencies, 2)
}

func TestParticipantSettlementPartialSwissWaveReusesStandingsArtifact(t *testing.T) {
	t.Parallel()

	pendingResultRevisionID := uuid.New()
	ledger := participantSettlementSeriesLedgerEntries(
		uuid.New(),
		uuid.New(),
		uuid.New(),
		uuid.New(),
		uuid.New(),
		uuid.New(),
		1,
		2,
	)

	// Before the final Series writes its two immutable rows, the active Wave
	// cannot enter the materialized source set. The previous artifact is reused.
	require.False(t, participantSettlementLedgerIncludes(ledger, pendingResultRevisionID))
}

func TestParticipantSettlementProjectionLedgerQueryRequiresCompleteActiveWave(t *testing.T) {
	t.Parallel()

	query, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "..", "..", "db", "queries", "participant_settlement.sql"))
	require.NoError(t, err)

	for _, fragment := range []string{
		"eligible_active_wave AS MATERIALIZED",
		"wave.state = 'active'",
		"active_games.state NOT IN ('completed', 'void', 'cancelled', 'superseded')",
		"current_result_revision_id IS NULL",
		"FROM active_ledger",
		"source_kind = 'bye'",
		"SELECT active_ledger.*",
		"INNER JOIN eligible_active_wave ON TRUE",
	} {
		require.Contains(t, string(query), fragment)
	}
}

func participantSettlementSeriesLedgerEntries(
	roundID uuid.UUID,
	roundRevisionID uuid.UUID,
	seriesID uuid.UUID,
	resultRevisionID uuid.UUID,
	winnerID uuid.UUID,
	loserID uuid.UUID,
	winnerSeed int32,
	loserSeed int32,
) []participantSettlementLedgerEntry {
	label := "played"
	accepted := int64(time.Second)
	return []participantSettlementLedgerEntry{
		{
			RoundID: roundID, RoundRevisionID: roundRevisionID, RoundNumber: 1,
			SourceKind:             "series",
			SourceSeriesID:         uuid.NullUUID{UUID: seriesID, Valid: true},
			SeriesResultRevisionID: uuid.NullUUID{UUID: resultRevisionID, Valid: true},
			ResultLabel:            &label, ParticipantID: winnerID,
			OpponentID: uuid.NullUUID{UUID: loserID, Valid: true}, Points: 1,
			EffectiveTimeNs: int64(time.Second), AcceptedSolveTimeNs: &accepted, StableSeed: winnerSeed,
		},
		{
			RoundID: roundID, RoundRevisionID: roundRevisionID, RoundNumber: 1,
			SourceKind:             "series",
			SourceSeriesID:         uuid.NullUUID{UUID: seriesID, Valid: true},
			SeriesResultRevisionID: uuid.NullUUID{UUID: resultRevisionID, Valid: true},
			ResultLabel:            &label, ParticipantID: loserID,
			OpponentID: uuid.NullUUID{UUID: winnerID, Valid: true}, Points: 0,
			EffectiveTimeNs: int64(time.Second), StableSeed: loserSeed,
		},
	}
}
