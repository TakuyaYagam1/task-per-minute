package playoff_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
)

func TestFinalSwissReceiptBeforeGoldenIdentities(t *testing.T) {
	t.Parallel()
	fixture := newPlayoffFixture(t, true)
	input := progressionSwissInput(fixture.command)
	input.GoldenGroups = nil
	receipt, err := playoff.PlanFinalSwissReceipt(input)
	require.NoError(t, err)
	require.NoError(t, receipt.Validate())
	require.Empty(t, receipt.GoldenGroups())
	require.NotEmpty(t, receipt.TieGroups())
	require.False(t, receipt.AdvanceDirectly())
	_, err = playoff.PlanFinalSwissProgression(input)
	require.Error(t, err, "the stage planner still requires command-owned Golden identities")
}

func TestFinalSwissReceiptRejectsGoldenIdentityInjection(t *testing.T) {
	t.Parallel()
	fixture := newPlayoffFixture(t, true)
	_, err := playoff.PlanFinalSwissReceipt(progressionSwissInput(fixture.command))
	require.Error(t, err)
}
