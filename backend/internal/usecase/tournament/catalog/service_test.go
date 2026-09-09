package catalog_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	catalogusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/catalog"
)

func TestUseCaseRejectsMissingDependencies(t *testing.T) {
	t.Parallel()

	application := catalogusecase.NewUseCase(catalogusecase.Dependencies{})
	_, err := application.ListTournaments(t.Context(), inbound.TournamentListCommand{
		Operator: inbound.OperatorIdentity{ActorID: uuid.New()},
	})

	require.ErrorIs(t, err, domain.ErrInternal)
}
