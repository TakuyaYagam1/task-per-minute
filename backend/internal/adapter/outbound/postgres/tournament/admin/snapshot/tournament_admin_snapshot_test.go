package snapshot

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
)

func TestNewTournamentAdminSnapshotPostgres(t *testing.T) {
	t.Parallel()

	repository := NewTournamentAdminSnapshotPostgres(nil)
	require.NotNil(t, repository)
	require.NotNil(t, repository.roster)
	require.NotNil(t, repository.drafts)

	_, err := repository.GetOperatorSnapshot(context.Background(), tournamentadmin.SnapshotQuery{
		Operator:     tournamentadmin.OperatorIdentity{ActorID: uuid.New()},
		TournamentID: uuid.New(),
	})
	require.ErrorIs(t, err, domain.ErrValidation)
}
