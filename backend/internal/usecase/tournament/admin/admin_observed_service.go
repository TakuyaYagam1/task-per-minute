package admin

import (
	observedusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/observed"
)

type AdminObservedService = observedusecase.ObservedService

func AdminNewObservedService(next AdminService, clock OperationClock, observer OperationObserver) *AdminObservedService {
	return observedusecase.NewObservedService(next, clock, observer)
}

var _ AdminService = (*AdminObservedService)(nil)
