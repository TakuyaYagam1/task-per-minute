package pause_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
)

func TestPauseScopeValidation(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.New()
	scope := pausedomain.GraphScope{
		TournamentID: tournamentID,
		RosterID:     uuid.New(),
		WaveID:       uuid.New(),
		Authority: authoritydomain.Identity{
			TournamentID: tournamentID,
			HolderID:     uuid.New(),
			LeaseID:      uuid.New(),
			Epoch:        1,
			ProcessKind:  authoritydomain.ProcessAuthority,
		},
	}
	require.NoError(t, scope.Validate())

	foreign := scope
	foreign.Authority.TournamentID = uuid.New()
	require.ErrorIs(t, foreign.Validate(), pausedomain.ErrInvalidScope)
}
