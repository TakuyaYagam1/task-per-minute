package execution

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestPlanWaveReadinessWaitsForDraftCompletion(t *testing.T) {
	t.Parallel()
	now := executionTestTime()
	authority := executionPlayoffWaveAuthority(t, now)
	command := WaveCommand{
		CommandScope: CommandScope{
			Operator:     OperatorIdentity{ActorID: executionTestID(230)},
			TournamentID: authority.View.Wave.TournamentID, CommandID: executionTestID(231),
		},
		WaveID: authority.View.Wave.ID, ExpectedProjectionRevision: authority.ProjectionRevision,
		Action: WaveActionOpenReadyWindow, Confirmed: true,
	}
	authority.Graph.PendingDrafts = true
	_, err := planWaveMutation(command, authority, now)
	require.ErrorIs(t, err, domain.ErrConflict)
	require.False(t, waveGraphStartable(authority))

	authority.Graph.PendingDrafts = false
	opened, err := planWaveMutation(command, authority, now)
	require.NoError(t, err)
	require.Equal(t, domain.WaveStateReadyWindowOpen, opened.State)
	require.NotNil(t, opened.ReadyWindow)
}
