package replay

import (
	"errors"
	"testing"

	tournamentadminreplay "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/replay"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func TestTournamentAdminReplayPostgresImplementsWorkflowRepository(t *testing.T) {
	t.Parallel()

	var repository tournamentadminreplay.ReplayWorkflowRepository = NewTournamentAdminReplayPostgres(nil)
	require.NotNil(t, repository)
}

func TestReplayReplacementLookupState(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		err       error
		wantFound bool
		wantErr   bool
	}{
		"existing command":   {wantFound: true},
		"no current command": {err: pgx.ErrNoRows},
		"query failure":      {err: errors.New("database unavailable"), wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			found, err := replayReplacementLookupState(test.err)

			require.Equal(t, test.wantFound, found)
			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}
