package playoff_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

func TestPlanFinalSwissProgressionMatchesCanonicalPlanner(t *testing.T) {
	t.Parallel()

	fixture := newPlayoffFixture(t, true)
	input := progressionSwissInput(fixture.command)

	fromBridge, err := playoff.PlanFinalSwissProgression(input)
	require.NoError(t, err)
	fromPlanner, err := playoff.PlanFinalSwissProjection(fixture.command)
	require.NoError(t, err)

	require.Equal(t, fromPlanner.Projection(), fromBridge.Projection())
	require.Equal(t, fromPlanner.Standings(), fromBridge.Standings())
	require.Equal(t, fromPlanner.TieGroups(), fromBridge.TieGroups())
	require.Equal(t, fromPlanner.GoldenGroups(), fromBridge.GoldenGroups())
	require.Equal(t, fromPlanner.Dependencies(), fromBridge.Dependencies())
}

func TestPlanFinalSwissProgressionCopiesNormalizedAuthority(t *testing.T) {
	t.Parallel()

	fixture := newPlayoffFixture(t, false)
	input := progressionSwissInput(fixture.command)
	firstSeries := input.Rounds[0].Series[0].Series()

	projection, err := playoff.PlanFinalSwissProgression(input)
	require.NoError(t, err)
	input.ParticipantIDs[0] = input.ParticipantIDs[1]
	input.Rounds[0].Series[0] = playoff.TerminalSeriesEvidence{}

	require.NoError(t, projection.Validate())
	require.Equal(t, firstSeries.ID, fixture.command.Rounds[0].Series[0].Series().ID)
	require.True(t, projection.AdvanceDirectly())
}

func TestDeriveImpactfulGoldenTieRangesUsesOnlyNormalizedSwissAuthority(t *testing.T) {
	t.Parallel()

	fixture := newPlayoffFixture(t, true)
	input := progressionSwissInput(fixture.command)
	input.GoldenGroups = nil

	ranges, err := playoff.DeriveImpactfulGoldenTieRanges(input)

	require.NoError(t, err)
	require.Equal(t, []playoff.ImpactfulGoldenTieRange{{
		PositionFrom: fixture.command.GoldenGroups[0].PositionFrom,
		PositionTo:   fixture.command.GoldenGroups[0].PositionTo,
	}}, ranges)
}

func TestDeriveImpactfulGoldenTieRangesRejectsCallerSuppliedIdentities(t *testing.T) {
	t.Parallel()

	fixture := newPlayoffFixture(t, true)

	_, err := playoff.DeriveImpactfulGoldenTieRanges(progressionSwissInput(fixture.command))

	require.Error(t, err)
}

func TestDeriveImpactfulGoldenTieRangesReturnsNoneForDirectSwiss(t *testing.T) {
	t.Parallel()

	fixture := newPlayoffFixture(t, false)

	ranges, err := playoff.DeriveImpactfulGoldenTieRanges(progressionSwissInput(fixture.command))

	require.NoError(t, err)
	require.Empty(t, ranges)
}

func progressionSwissInput(command playoff.FinalSwissProjectionCommand) playoff.ProgressionSwissInput {
	rounds := make([]playoff.ProgressionSwissRound, len(command.Rounds))
	for index, round := range command.Rounds {
		rounds[index] = playoff.ProgressionSwissRound{
			RoundID:     round.RoundID,
			RoundNumber: round.RoundNumber,
			RevisionID:  round.RevisionID,
			LockProof:   round.LockProof,
			Series:      append([]playoff.TerminalSeriesEvidence(nil), round.Series...),
			Bye:         round.Bye,
		}
	}
	return playoff.ProgressionSwissInput{
		TournamentID:               command.TournamentID,
		Preset:                     command.Preset,
		ProjectionID:               command.ProjectionID,
		RevisionID:                 command.RevisionID,
		RevisionNo:                 command.RevisionNo,
		PhysicalProjectionRevision: command.PhysicalProjectionRevision,
		Previous:                   command.Previous,
		ParticipantIDs:             append([]uuid.UUID(nil), command.ParticipantIDs...),
		Seeds:                      append([]swissusecase.ParticipantSeed(nil), command.Seeds...),
		Rounds:                     rounds,
		GoldenGroups:               append([]playoff.FinalSwissGoldenGroupIdentity(nil), command.GoldenGroups...),
		CreatedAt:                  command.CreatedAt,
	}
}
