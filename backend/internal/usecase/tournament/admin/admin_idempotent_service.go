package admin

import (
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency"
	idempotentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/idempotent"
)

type AdminIdempotentService = idempotentusecase.IdempotentService

func AdminNewIdempotentService(
	next AdminService,
	catalog usecase.TournamentUseCase,
	coordinator *idempotency.Coordinator,
) (*AdminIdempotentService, error) {
	return idempotentusecase.NewIdempotentService(next, catalog, coordinator)
}

var _ AdminService = (*AdminIdempotentService)(nil)
